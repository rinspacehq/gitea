// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo_test

import (
	"slices"
	"testing"

	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"

	"github.com/stretchr/testify/require"
)

func TestRinspacePresentationSearchAndContentOwnerList(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	t.Cleanup(func() {
		_, _ = db.DeleteByBean(ctx, &repo_model.RinspaceRepositoryOwner{RepoID: 4})
		_, _ = db.DeleteByBean(ctx, &repo_model.RinspaceRepositoryOwner{RepoID: 1})
		_, _ = db.DeleteByID[repo_model.RinspaceRepositoryPresentation](ctx, 4)
		_, _ = db.DeleteByID[repo_model.RinspaceRepositoryPresentation](ctx, 1)
	})

	applied, err := repo_model.UpsertRinspaceRepositoryPresentation(ctx, &repo_model.RinspaceRepositoryPresentation{
		RepoID: 4, Title: "Unique Rinspace Quantum Notes", ContentType: repo_model.RinspaceContentArticle, Version: 2,
	}, []int64{4, 4})
	require.NoError(t, err)
	require.True(t, applied)

	repos, count, err := repo_model.SearchRepository(ctx, repo_model.SearchRepoOptions{
		Keyword: "quantum notes", ListOptions: db.ListOptions{Page: 1, PageSize: 10},
		Collaborate: optional.Some(false),
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Equal(t, "repo4/[-Unique Rinspace Quantum Notes]", repos[0].RinspaceDisplayName())
	require.Equal(t, "user5/repo4", repos[0].FullName())

	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	owned, _, err := repo_model.SearchRepository(ctx, repo_model.SearchRepoOptions{
		Actor: user4, OwnerID: user4.ID, RinspaceContentOwnerID: user4.ID, Private: true,
		Collaborate: optional.Some(false), ListOptions: db.ListOptions{Page: 1, PageSize: 100},
	})
	require.NoError(t, err)
	require.True(t, slices.Contains(owned.IDs(), int64(4)))

	_, err = repo_model.UpsertRinspaceRepositoryPresentation(ctx, &repo_model.RinspaceRepositoryPresentation{
		RepoID: 1, Title: "Tag must stay out", ContentType: repo_model.RinspaceContentTagWiki, Version: 1,
	}, []int64{4})
	require.NoError(t, err)
	owned, _, err = repo_model.SearchRepository(ctx, repo_model.SearchRepoOptions{
		Actor: user4, OwnerID: user4.ID, RinspaceContentOwnerID: user4.ID, Private: true,
		Collaborate: optional.Some(false), ListOptions: db.ListOptions{Page: 1, PageSize: 100},
	})
	require.NoError(t, err)
	require.False(t, slices.Contains(owned.IDs(), int64(1)))

	_, err = repo_model.UpsertRinspaceRepositoryPresentation(ctx, &repo_model.RinspaceRepositoryPresentation{
		RepoID: 3, Title: "Private projected secret", ContentType: repo_model.RinspaceContentBook, Version: 1,
	}, []int64{30})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.DeleteByBean(ctx, &repo_model.RinspaceRepositoryOwner{RepoID: 3})
		_, _ = db.DeleteByID[repo_model.RinspaceRepositoryPresentation](ctx, 3)
	})
	user30 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 30})
	owned, _, err = repo_model.SearchRepository(ctx, repo_model.SearchRepoOptions{
		Actor: user30, OwnerID: user30.ID, RinspaceContentOwnerID: user30.ID, Private: true,
		Collaborate: optional.Some(false), ListOptions: db.ListOptions{Page: 1, PageSize: 100},
	})
	require.NoError(t, err)
	require.False(t, slices.Contains(owned.IDs(), int64(3)), "content ownership must not grant repository read permission")
	secretSearch, _, err := repo_model.SearchRepository(ctx, repo_model.SearchRepoOptions{
		Keyword: "projected secret", Collaborate: optional.Some(false), ListOptions: db.ListOptions{Page: 1, PageSize: 10},
	})
	require.NoError(t, err)
	require.Empty(t, secretSearch)

	applied, err = repo_model.UpsertRinspaceRepositoryPresentation(ctx, &repo_model.RinspaceRepositoryPresentation{
		RepoID: 4, Title: "stale title", ContentType: repo_model.RinspaceContentArticle, Version: 1,
	}, []int64{5})
	require.NoError(t, err)
	require.False(t, applied)
	repo4 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo4.LoadRinspacePresentation(ctx))
	require.Equal(t, "Unique Rinspace Quantum Notes", repo4.RinspaceTitle)
	require.Equal(t, []int64{4}, repo4.RinspaceContentOwnerIDs)
}
