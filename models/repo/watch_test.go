// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo_test

import (
	"testing"

	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsWatching(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	assert.True(t, repo_model.IsWatching(t.Context(), 1, 1))
	assert.True(t, repo_model.IsWatching(t.Context(), 4, 1))
	assert.True(t, repo_model.IsWatching(t.Context(), 11, 1))

	assert.False(t, repo_model.IsWatching(t.Context(), 1, 5))
	assert.False(t, repo_model.IsWatching(t.Context(), 8, 1))
	assert.False(t, repo_model.IsWatching(t.Context(), unittest.NonexistentID, unittest.NonexistentID))
}

func TestGetWatchers(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	watches, err := repo_model.GetWatchers(t.Context(), repo.ID)
	assert.NoError(t, err)
	// One watchers are inactive, thus minus 1
	assert.Len(t, watches, repo.NumWatches-1)
	for _, watch := range watches {
		assert.Equal(t, repo.ID, watch.RepoID)
	}

	watches, err = repo_model.GetWatchers(t.Context(), unittest.NonexistentID)
	assert.NoError(t, err)
	assert.Empty(t, watches)
}

func TestRepository_GetWatchers(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	watchers, err := repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, repo.NumWatches)
	for _, watcher := range watchers {
		unittest.AssertExistsAndLoadBean(t, &repo_model.Watch{UserID: watcher.ID, RepoID: repo.ID})
	}

	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 9})
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Empty(t, watchers)
}

func TestWatchIfAuto(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user12 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 12})

	watchers, err := repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, repo.NumWatches)

	setting.Service.AutoWatchOnChanges = false

	prevCount := repo.NumWatches

	// Must not add watch
	assert.NoError(t, repo_model.WatchIfAuto(t.Context(), 8, 1, true))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount)

	// Should not add watch
	assert.NoError(t, repo_model.WatchIfAuto(t.Context(), 10, 1, true))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount)

	setting.Service.AutoWatchOnChanges = true

	// Must not add watch
	assert.NoError(t, repo_model.WatchIfAuto(t.Context(), 8, 1, true))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount)

	// Should not add watch
	assert.NoError(t, repo_model.WatchIfAuto(t.Context(), 12, 1, false))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount)

	// Should add watch
	assert.NoError(t, repo_model.WatchIfAuto(t.Context(), 12, 1, true))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount+1)

	// Should remove watch, inhibit from adding auto
	assert.NoError(t, repo_model.WatchRepo(t.Context(), user12, repo, false))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount)

	// Must not add watch
	assert.NoError(t, repo_model.WatchIfAuto(t.Context(), 12, 1, true))
	watchers, err = repo_model.GetRepoWatchers(t.Context(), repo.ID, db.ListOptions{Page: 1})
	assert.NoError(t, err)
	assert.Len(t, watchers, prevCount)
}

func TestClearRepoWatches(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	const repoID int64 = 1
	watchers, err := repo_model.GetRepoWatchersIDs(t.Context(), repoID)
	require.NoError(t, err)
	require.NotEmpty(t, watchers)

	assert.NoError(t, repo_model.ClearRepoWatches(t.Context(), repoID))

	watchers, err = repo_model.GetRepoWatchersIDs(t.Context(), repoID)
	assert.NoError(t, err)
	assert.Empty(t, watchers)

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
	assert.Zero(t, repo.NumWatches)
}

func TestWatchRepoOutboxAndAtomicRollback(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 5})
	_, _ = db.DeleteByBean(t.Context(), &rincontrol_model.OutboxEvent{ActorUserID: user.ID, RepositoryID: repo.ID, EventType: "repository.watch.changed"})
	assert.False(t, repo_model.IsWatching(t.Context(), user.ID, repo.ID))

	assert.NoError(t, repo_model.WatchRepo(t.Context(), user, repo, true))
	assert.True(t, repo_model.IsWatching(t.Context(), user.ID, repo.ID))
	assert.EqualValues(t, 1, countSocialOutbox(t, "repository.watch.changed", user.ID, repo.ID))
	assert.NoError(t, repo_model.WatchRepo(t.Context(), user, repo, true))
	assert.EqualValues(t, 1, countSocialOutbox(t, "repository.watch.changed", user.ID, repo.ID))
	assert.NoError(t, repo_model.WatchRepo(t.Context(), user, repo, false))
	assert.EqualValues(t, 2, countSocialOutbox(t, "repository.watch.changed", user.ID, repo.ID))

	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 5})
	before := repo.NumWatches
	_, err := db.Exec(t.Context(), "CREATE TRIGGER fail_rin_outbox BEFORE INSERT ON rin_control_outbox BEGIN SELECT RAISE(FAIL, 'injected outbox failure'); END")
	require.NoError(t, err)
	defer func() { _, _ = db.Exec(t.Context(), "DROP TRIGGER IF EXISTS fail_rin_outbox") }()
	assert.Error(t, repo_model.WatchRepo(t.Context(), user, repo, true))
	assert.False(t, repo_model.IsWatching(t.Context(), user.ID, repo.ID))
	reloaded := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 5})
	assert.Equal(t, before, reloaded.NumWatches)
}
