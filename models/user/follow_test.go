// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user_test

import (
	"testing"

	"gitea.dev/models/db"
	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
)

func TestIsFollowing(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	assert.True(t, user_model.IsFollowing(t.Context(), 4, 2))
	assert.False(t, user_model.IsFollowing(t.Context(), 2, 4))
	assert.False(t, user_model.IsFollowing(t.Context(), 5, unittest.NonexistentID))
	assert.False(t, user_model.IsFollowing(t.Context(), unittest.NonexistentID, 5))
	assert.False(t, user_model.IsFollowing(t.Context(), unittest.NonexistentID, unittest.NonexistentID))
}

func TestFollowUserOutboxIsSemanticAndIdempotent(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	_, _ = db.DeleteByBean(t.Context(), &rincontrol_model.OutboxEvent{ActorUserID: user.ID, TargetUserID: target.ID, EventType: "user.follow.changed"})
	assert.False(t, user_model.IsFollowing(t.Context(), user.ID, target.ID))

	assert.NoError(t, user_model.FollowUser(t.Context(), user, target))
	assert.NoError(t, user_model.FollowUser(t.Context(), user, target))
	assert.EqualValues(t, 1, countFollowOutbox(t, user.ID, target.ID))
	assert.NoError(t, user_model.UnfollowUser(t.Context(), user.ID, target.ID))
	assert.NoError(t, user_model.UnfollowUser(t.Context(), user.ID, target.ID))
	assert.EqualValues(t, 2, countFollowOutbox(t, user.ID, target.ID))
}

func countFollowOutbox(t *testing.T, actorID, targetID int64) int64 {
	t.Helper()
	count, err := db.GetEngine(t.Context()).Where("event_type = ? AND actor_user_id = ? AND target_user_id = ?", "user.follow.changed", actorID, targetID).Count(new(rincontrol_model.OutboxEvent))
	assert.NoError(t, err)
	return count
}
