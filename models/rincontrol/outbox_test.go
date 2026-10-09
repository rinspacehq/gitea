// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutboxClaimConfirmRetryAndRecovery(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(OutboxEvent))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	require.NoError(t, EnqueueSocialEvent(t.Context(), SocialEvent{EventType: "repository.star.changed", ActorUserID: 12, RepositoryID: 34, Active: true}))
	_, err = db.GetEngine(t.Context()).Where("1 = 1").Cols("next_attempt_unix").Update(&OutboxEvent{NextAttemptUnix: timeutilTimestamp(now)})
	require.NoError(t, err)

	first, err := ClaimDueOutboxEvent(t.Context(), now, 10*time.Second)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, 1, first.Attempts)
	assert.Nil(t, mustClaim(t, now.Add(time.Second)))

	ok, err := FailOutboxDelivery(t.Context(), first, "http_503", now)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, mustClaim(t, now))
	second := mustClaim(t, now.Add(time.Second))
	require.NotNil(t, second)
	assert.Equal(t, 2, second.Attempts)

	// A crashed process leaves a lease behind; another process recovers it only
	// after expiration and the stale process can no longer acknowledge it.
	assert.Nil(t, mustClaim(t, now.Add(10*time.Second)))
	recovered := mustClaim(t, now.Add(12*time.Second))
	require.NotNil(t, recovered)
	assert.NotEqual(t, second.LeaseToken, recovered.LeaseToken)
	ok, err = ConfirmOutboxDelivery(t.Context(), second.ID, second.LeaseToken, now.Add(12*time.Second))
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = ConfirmOutboxDelivery(t.Context(), recovered.ID, recovered.LeaseToken, now.Add(12*time.Second))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Nil(t, mustClaim(t, now.Add(time.Hour)))
}

func TestOutboxDeadLetterAndBacklog(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(OutboxEvent))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	event := &OutboxEvent{
		EventID: "dead-test", EventType: "repository.watch.changed", AggregateType: "repository", AggregateID: 8,
		ActorUserID: 7, RepositoryID: 8, Payload: `{}`, State: OutboxStatePending, Attempts: MaxDeliveryAttempts - 1,
		NextAttemptUnix: timeutilTimestamp(now),
	}
	require.NoError(t, db.Insert(t.Context(), event))
	claimed := mustClaim(t, now)
	require.NotNil(t, claimed)
	assert.Equal(t, MaxDeliveryAttempts, claimed.Attempts)
	ok, err := FailOutboxDelivery(t.Context(), claimed, "permanent", now)
	require.NoError(t, err)
	assert.True(t, ok)
	stored := new(OutboxEvent)
	has, err := db.GetEngine(t.Context()).ID(event.ID).Get(stored)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, OutboxStateDead, stored.State)
	backlog, err := GetOutboxBacklog(t.Context(), now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, backlog.Dead)
}

func mustClaim(t *testing.T, now time.Time) *OutboxEvent {
	t.Helper()
	event, err := ClaimDueOutboxEvent(t.Context(), now, 10*time.Second)
	require.NoError(t, err)
	return event
}

func timeutilTimestamp(value time.Time) timeutil.TimeStamp {
	return timeutil.TimeStamp(value.Unix())
}
