// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationHeartbeatIsDeterministicAndCoalesced(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(OutboxEvent))
	require.NoError(t, err)
	now := time.Unix(1_800_000_017, 0).UTC()
	event, err := BuildPublicationHeartbeatOutboxEvent(now, strings.Repeat("a", 40))
	require.NoError(t, err)
	assert.Equal(t, "gitea-publication-heartbeat:1800000000", event.EventID)
	require.NoError(t, ValidateOutboxEvent(event))

	require.NoError(t, EnqueuePublicationHeartbeat(t.Context(), now, "release-a"))
	require.NoError(t, EnqueuePublicationHeartbeat(t.Context(), now.Add(30*time.Second), "release-a"))
	count, err := db.GetEngine(t.Context()).Where("event_type = ?", PublicationHeartbeatEventType).Count(new(OutboxEvent))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)

	stored := new(OutboxEvent)
	has, err := db.GetEngine(t.Context()).Where("event_type = ?", PublicationHeartbeatEventType).Get(stored)
	require.NoError(t, err)
	require.True(t, has)
	ok, err := ConfirmOutboxDelivery(t.Context(), stored.ID, "missing", now)
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = db.GetEngine(t.Context()).ID(stored.ID).Cols("state", "delivered_unix").Update(&OutboxEvent{State: OutboxStateDelivered, DeliveredUnix: timeutilTimestamp(now)})
	require.NoError(t, err)
	require.NoError(t, EnqueuePublicationHeartbeat(t.Context(), now.Add(30*time.Second), "release-a"))
	count, err = db.GetEngine(t.Context()).Where("event_type = ?", PublicationHeartbeatEventType).Count(new(OutboxEvent))
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
}

func TestPublicationOutboxHealthExcludesSocialEvents(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(OutboxEvent))
	require.NoError(t, err)
	now := time.Unix(1_800_000_030, 0).UTC()
	require.NoError(t, EnqueuePublicationHeartbeat(t.Context(), now, "release-a"))
	require.NoError(t, EnqueueSocialEvent(t.Context(), SocialEvent{EventType: "repository.star.changed", ActorUserID: 7, RepositoryID: 8, Active: true}))
	_, err = db.GetEngine(t.Context()).Exec("UPDATE rin_control_outbox SET created_unix = ?, next_attempt_unix = ? WHERE event_type = ?", timeutilTimestamp(now.Add(-45*time.Second)), timeutilTimestamp(now), PublicationHeartbeatEventType)
	require.NoError(t, err)
	health, err := GetPublicationOutboxHealth(t.Context(), now)
	require.NoError(t, err)
	assert.EqualValues(t, 1, health.Pending)
	assert.EqualValues(t, 0, health.Dead)
	assert.Equal(t, 45*time.Second, health.OldestAge)
	require.NotNil(t, health.LastCapturedAt)
}
