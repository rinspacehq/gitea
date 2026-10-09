// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo_test

import (
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/organization"
	perm_model "gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	t.Run("GetIssuePostersWithSearch", testUserRepoGetIssuePostersWithSearch)
	t.Run("Assignees", testUserRepoAssignees)
	t.Run("AssigneesNoTeamUnit", testRepoAssigneesNoTeamUnit)
}

func testUserRepoAssignees(t *testing.T) {
	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	users, err := repo_model.GetRepoAssignees(t.Context(), repo2)
	assert.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, int64(2), users[0].ID)

	repo21 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 21})
	users, err = repo_model.GetRepoAssignees(t.Context(), repo21)
	assert.NoError(t, err)
	if assert.Len(t, users, 4) {
		assert.ElementsMatch(t, []int64{10, 15, 16, 18}, []int64{users[0].ID, users[1].ID, users[2].ID, users[3].ID})
	}

	// do not return deactivated users
	assert.NoError(t, user_model.UpdateUserCols(t.Context(), &user_model.User{ID: 15, IsActive: false}, "is_active"))
	users, err = repo_model.GetRepoAssignees(t.Context(), repo21)
	assert.NoError(t, err)
	if assert.Len(t, users, 3) {
		assert.NotContains(t, []int64{users[0].ID, users[1].ID, users[2].ID}, 15)
	}
}

func testRepoAssigneesNoTeamUnit(t *testing.T) {
	ctx := t.Context()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 32})
	require.NoError(t, repo.LoadOwner(ctx))
	require.True(t, repo.Owner.IsOrganization())

	require.NoError(t, db.TruncateBeans(ctx, &organization.Team{}, &organization.TeamUser{}, &organization.TeamRepo{}, &organization.TeamUnit{}, &access_model.Access{}))

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	team := &organization.Team{OrgID: repo.OwnerID, LowerName: "admin-team", AccessMode: perm_model.AccessModeAdmin}
	require.NoError(t, db.Insert(ctx, team))
	require.NoError(t, db.Insert(ctx, &organization.TeamUser{OrgID: repo.OwnerID, TeamID: team.ID, UID: user.ID}))
	require.NoError(t, db.Insert(ctx, &organization.TeamRepo{OrgID: repo.OwnerID, TeamID: team.ID, RepoID: repo.ID}))
	require.NoError(t, db.Insert(ctx, &organization.TeamUnit{OrgID: repo.OwnerID, TeamID: team.ID, Type: unit.TypePullRequests, AccessMode: perm_model.AccessModeNone}))

	users, err := repo_model.GetRepoAssignees(ctx, repo)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.ElementsMatch(t, []int64{4}, []int64{users[0].ID})
}

func testUserRepoGetIssuePostersWithSearch(t *testing.T) {
	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})

	users, err := repo_model.GetIssuePostersWithSearch(t.Context(), repo2, false, "USER")
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "user2", users[0].Name)

	users, err = repo_model.GetIssuePostersWithSearch(t.Context(), repo2, false, "TW%O")
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "user2", users[0].Name)
}

func TestStarredWatchedReposExcludeNonPublicOwners(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	const viewerID = 2
	// repo1: public repo under a public owner; repo38: public repo under a limited org (not publicly reachable)
	const publicOwnerRepo, limitedOwnerRepo = 1, 38

	require.NoError(t, db.Insert(t.Context(), &repo_model.Star{UID: viewerID, RepoID: publicOwnerRepo}))
	require.NoError(t, db.Insert(t.Context(), &repo_model.Star{UID: viewerID, RepoID: limitedOwnerRepo}))
	require.NoError(t, db.Insert(t.Context(), &repo_model.Watch{UserID: viewerID, RepoID: publicOwnerRepo, Mode: repo_model.WatchModeNormal}))
	require.NoError(t, db.Insert(t.Context(), &repo_model.Watch{UserID: viewerID, RepoID: limitedOwnerRepo, Mode: repo_model.WatchModeNormal}))

	listOpts := db.ListOptions{Page: 1, PageSize: 50}

	starred, err := repo_model.GetStarredRepos(t.Context(), &repo_model.StarredReposOptions{
		ListOptions: listOpts, StarrerID: viewerID, IncludePrivate: false,
	})
	require.NoError(t, err)
	assert.NotContains(t, repoIDs(starred), int64(limitedOwnerRepo), "a public repo under a limited owner must be hidden from a public star listing")
	assert.Contains(t, repoIDs(starred), int64(publicOwnerRepo), "a public repo under a public owner stays visible")

	watched, _, err := repo_model.GetWatchedRepos(t.Context(), &repo_model.WatchedReposOptions{
		ListOptions: listOpts, WatcherID: viewerID, IncludePrivate: false,
	})
	require.NoError(t, err)
	assert.NotContains(t, repoIDs(watched), int64(limitedOwnerRepo), "a public repo under a limited owner must be hidden from a public watch listing")
	assert.Contains(t, repoIDs(watched), int64(publicOwnerRepo), "a public repo under a public owner stays visible")
}

func TestPrivateSocialRelationsFollowCurrentAccessWithoutMutation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	viewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo_model.StarRepo(t.Context(), viewer, repo, true))
	star := unittest.AssertExistsAndLoadBean(t, &repo_model.Star{UID: viewer.ID, RepoID: repo.ID})
	watch := unittest.AssertExistsAndLoadBean(t, &repo_model.Watch{UserID: viewer.ID, RepoID: repo.ID})
	repo.IsPrivate = true
	require.NoError(t, repo_model.UpdateRepositoryColsNoAutoTime(t.Context(), repo, "is_private"))

	starOpts := &repo_model.StarredReposOptions{ListOptions: db.ListOptions{Page: 1, PageSize: 20}, StarrerID: viewer.ID, IncludePrivate: true, Actor: viewer}
	watchOpts := &repo_model.WatchedReposOptions{ListOptions: db.ListOptions{Page: 1, PageSize: 20}, WatcherID: viewer.ID, IncludePrivate: true, Actor: viewer}
	starred, err := repo_model.GetStarredRepos(t.Context(), starOpts)
	require.NoError(t, err)
	assert.NotContains(t, repoIDs(starred), repo.ID)
	starredTotal, err := repo_model.CountStarredRepos(t.Context(), starOpts)
	require.NoError(t, err)
	assert.Equal(t, int64(len(starred)), starredTotal)
	watched, total, err := repo_model.GetWatchedRepos(t.Context(), watchOpts)
	require.NoError(t, err)
	assert.NotContains(t, repoIDs(watched), repo.ID)
	assert.Equal(t, int64(len(watched)), total)

	require.NoError(t, db.Insert(t.Context(), &access_model.Access{UserID: viewer.ID, RepoID: repo.ID, Mode: perm_model.AccessModeRead}))
	starred, err = repo_model.GetStarredRepos(t.Context(), starOpts)
	require.NoError(t, err)
	assert.Contains(t, repoIDs(starred), repo.ID)
	starredTotal, err = repo_model.CountStarredRepos(t.Context(), starOpts)
	require.NoError(t, err)
	assert.Equal(t, int64(len(starred)), starredTotal)
	watched, _, err = repo_model.GetWatchedRepos(t.Context(), watchOpts)
	require.NoError(t, err)
	assert.Contains(t, repoIDs(watched), repo.ID)

	_, err = db.DeleteByBean(t.Context(), &access_model.Access{UserID: viewer.ID, RepoID: repo.ID})
	require.NoError(t, err)
	unittest.AssertExistsAndLoadBean(t, &repo_model.Star{ID: star.ID, CreatedUnix: star.CreatedUnix})
	unittest.AssertExistsAndLoadBean(t, &repo_model.Watch{ID: watch.ID, CreatedUnix: watch.CreatedUnix})
	starred, err = repo_model.GetStarredRepos(t.Context(), starOpts)
	require.NoError(t, err)
	assert.NotContains(t, repoIDs(starred), repo.ID)
}

func repoIDs(repos []*repo_model.Repository) []int64 {
	ids := make([]int64, len(repos))
	for i, r := range repos {
		ids[i] = r.ID
	}
	return ids
}
