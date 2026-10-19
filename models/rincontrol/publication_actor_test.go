// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"strings"
	"testing"
	"time"

	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/require"
)

func TestRepositoryPushAttributionDoesNotInferLegacyActor(t *testing.T) {
	input := RepositoryPushEvent{RepositoryID: 42, Ref: "refs/heads/main", Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40)}
	for _, actor := range []int64{0, 17} {
		input.PusherUserID = actor
		event, err := BuildRepositoryPushOutboxEvent(input)
		require.NoError(t, err)
		require.NoError(t, ValidateOutboxEvent(event))
		var payload repositoryPushEnvelope
		require.NoError(t, json.Unmarshal([]byte(event.Payload), &payload))
		if actor == 0 {
			require.Nil(t, payload.Data.PusherUserID)
			require.NotContains(t, event.Payload, "pusherUserId")
		} else {
			require.Equal(t, actor, *payload.Data.PusherUserID)
			event.ActorUserID = 18
			require.Error(t, ValidateOutboxEvent(event))
		}
	}
	input.PusherUserID = -1
	_, err := BuildRepositoryPushOutboxEvent(input)
	require.Error(t, err)
}

func TestRepositoryPushAttributionReplayNeverReassignsActor(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	input := RepositoryPushEvent{RepositoryID: 424242, Ref: "refs/heads/main", Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40), PusherUserID: 17, OccurredAt: time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)}
	require.NoError(t, EnqueueRepositoryPushEvent(t.Context(), input))
	input.OccurredAt = input.OccurredAt.Add(time.Minute)
	require.NoError(t, EnqueueRepositoryPushEvent(t.Context(), input))
	input.PusherUserID = 18
	require.ErrorIs(t, EnqueueRepositoryPushEvent(t.Context(), input), ErrOutboxIdempotencyConflict)
	input.PusherUserID = 17
	input.Before = strings.Repeat("c", 40)
	require.ErrorIs(t, EnqueueRepositoryPushEvent(t.Context(), input), ErrOutboxIdempotencyConflict)
}
