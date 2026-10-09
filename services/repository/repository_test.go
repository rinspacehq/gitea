// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachLinkedTypeAndRepoID(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	testCases := []struct {
		name             string
		attachID         int64
		expectedUnitType unit.Type
		expectedRepoID   int64
	}{
		{"LinkedIssue", 1, unit.TypeIssues, 1},
		{"LinkedComment", 3, unit.TypePullRequests, 1},
		{"LinkedRelease", 9, unit.TypeReleases, 1},
		{"Notlinked", 10, unit.TypeInvalid, 0},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			attach, err := repo_model.GetAttachmentByID(t.Context(), tc.attachID)
			assert.NoError(t, err)
			unitType, repoID, err := GetAttachmentLinkedTypeAndRepoID(t.Context(), attach)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedUnitType, unitType)
			assert.Equal(t, tc.expectedRepoID, repoID)
		})
	}
}

func TestUpdateRepositoryVisibilityChanged(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	// Get sample repo and change visibility
	repo, err := repo_model.GetRepositoryByID(t.Context(), 9)
	assert.NoError(t, err)
	repo.IsPrivate = true

	// Update it
	err = updateRepository(t.Context(), repo, true)
	assert.NoError(t, err)

	// Check visibility of action has become private
	act := activities_model.Action{}
	_, err = db.GetEngine(t.Context()).ID(3).Get(&act)

	assert.NoError(t, err)
	assert.True(t, act.IsPrivate)
}

func TestRepository_HasWiki(t *testing.T) {
	unittest.PrepareTestEnv(t)
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.True(t, HasWiki(t.Context(), repo1))

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	assert.False(t, HasWiki(t.Context(), repo2))
}

func TestMakeRepoPrivatePreservesSocialRelationships(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.False(t, repo.IsPrivate)

	watchers, err := repo_model.GetRepoWatchersIDs(t.Context(), repo.ID)
	require.NoError(t, err)
	require.NotEmpty(t, watchers)
	starrer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.NoError(t, repo_model.StarRepo(t.Context(), starrer, repo, true))
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	stars := repo.NumStars
	watches := repo.NumWatches
	watch := unittest.AssertExistsAndLoadBean(t, &repo_model.Watch{RepoID: repo.ID})
	star := unittest.AssertExistsAndLoadBean(t, &repo_model.Star{RepoID: repo.ID})

	assert.NoError(t, MakeRepoPrivate(t.Context(), repo, true))

	watchers, err = repo_model.GetRepoWatchersIDs(t.Context(), repo.ID)
	assert.NoError(t, err)
	assert.NotEmpty(t, watchers)

	updatedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.True(t, updatedRepo.IsPrivate)
	assert.Equal(t, watches, updatedRepo.NumWatches)
	assert.Equal(t, stars, updatedRepo.NumStars)
	unittest.AssertExistsAndLoadBean(t, &repo_model.Watch{ID: watch.ID, CreatedUnix: watch.CreatedUnix})
	unittest.AssertExistsAndLoadBean(t, &repo_model.Star{ID: star.ID, CreatedUnix: star.CreatedUnix})
	require.NoError(t, MakeRepoPrivate(t.Context(), updatedRepo, false))
	restoredAction := unittest.AssertExistsAndLoadBean(t, &activities_model.Action{RepoID: repo.ID})
	assert.False(t, restoredAction.IsPrivate)
	unittest.AssertExistsAndLoadBean(t, &repo_model.Watch{ID: watch.ID, CreatedUnix: watch.CreatedUnix})
	unittest.AssertExistsAndLoadBean(t, &repo_model.Star{ID: star.ID, CreatedUnix: star.CreatedUnix})
}

// TestUpdateRepositoryClearsWatchesOnVisibilityChange ensures the shared updateRepository
// helper (used by the API EditRepo path) also clears watches when a repo goes private.
func TestUpdateRepositoryPreservesSocialRelationshipsOnVisibilityChange(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.False(t, repo.IsPrivate)

	watchers, err := repo_model.GetRepoWatchersIDs(t.Context(), repo.ID)
	require.NoError(t, err)
	require.NotEmpty(t, watchers)
	stars := repo.NumStars
	watches := repo.NumWatches

	repo.IsPrivate = true
	require.NoError(t, updateRepository(t.Context(), repo, true))

	watchers, err = repo_model.GetRepoWatchersIDs(t.Context(), repo.ID)
	assert.NoError(t, err)
	assert.NotEmpty(t, watchers)

	updatedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.Equal(t, watches, updatedRepo.NumWatches)
	assert.Equal(t, stars, updatedRepo.NumStars)
}
