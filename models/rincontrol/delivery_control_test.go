// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"errors"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationDeliveryMaintenanceLeaseValidationAndIdempotency(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(DeliveryControl))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	valid := PauseDeliveryRequest{ChangeID: "RCP-LEASE-0001", ReasonCode: "credential_rotation", Until: now.Add(10 * time.Minute)}

	for _, request := range []PauseDeliveryRequest{
		{},
		{ChangeID: "bad", ReasonCode: valid.ReasonCode, Until: valid.Until},
		{ChangeID: valid.ChangeID, ReasonCode: "arbitrary", Until: valid.Until},
		{ChangeID: valid.ChangeID, ReasonCode: valid.ReasonCode, Until: now},
		{ChangeID: valid.ChangeID, ReasonCode: valid.ReasonCode, Until: now.Add(MaxDeliveryPause + time.Second)},
	} {
		_, err := PausePublicationDelivery(t.Context(), request, now)
		require.Error(t, err)
	}

	paused, err := PausePublicationDelivery(t.Context(), valid, now)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatePaused, paused.State)
	assert.EqualValues(t, 2, paused.Version)
	replayed, err := PausePublicationDelivery(t.Context(), valid, now.Add(time.Second))
	require.NoError(t, err)
	assert.Equal(t, paused.Version, replayed.Version)

	conflicting := valid
	conflicting.Until = valid.Until.Add(time.Second)
	_, err = PausePublicationDelivery(t.Context(), conflicting, now)
	assert.ErrorIs(t, err, ErrDeliveryControlConflict)
	_, err = ResumePublicationDelivery(t.Context(), "RCP-OTHER-0001", now)
	assert.ErrorIs(t, err, ErrDeliveryControlConflict)

	resumed, err := ResumePublicationDelivery(t.Context(), valid.ChangeID, now)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStateEnabled, resumed.State)
	assert.Zero(t, resumed.PausedUntilUnix)
	replayedResume, err := ResumePublicationDelivery(t.Context(), valid.ChangeID, now.Add(time.Second))
	require.NoError(t, err)
	assert.Equal(t, resumed.Version, replayedResume.Version)
	_, err = PausePublicationDelivery(t.Context(), valid, now)
	assert.ErrorIs(t, err, ErrDeliveryControlConflict)
}

func TestPublicationDeliveryPauseSkipsOnlyPublicationAndAutoResumes(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(DeliveryControl))
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).Where("1 = 1").Delete(new(OutboxEvent))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	publication, err := BuildRepositoryPushOutboxEvent(RepositoryPushEvent{
		RepositoryID: 42, Ref: "refs/heads/main", Before: strings.Repeat("a", 40),
		After: strings.Repeat("b", 40), OccurredAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(), publication))
	require.NoError(t, EnqueueSocialEvent(t.Context(), SocialEvent{
		EventType: "repository.star.changed", ActorUserID: 7, RepositoryID: 42, Active: true,
	}))
	_, err = db.GetEngine(t.Context()).Where("1 = 1").Cols("next_attempt_unix").
		Update(&OutboxEvent{NextAttemptUnix: timeutil.TimeStamp(now.Unix())})
	require.NoError(t, err)

	_, err = PausePublicationDelivery(t.Context(), PauseDeliveryRequest{
		ChangeID: "RCP-LEASE-0002", ReasonCode: "protocol_isolation", Until: now.Add(5 * time.Minute),
	}, now)
	require.NoError(t, err)
	claimed, err := ClaimDueOutboxEvent(t.Context(), now, 10*time.Second)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "repository.star.changed", claimed.EventType)
	confirmed, err := ConfirmOutboxDelivery(t.Context(), claimed.ID, claimed.LeaseToken, now)
	require.NoError(t, err)
	assert.True(t, confirmed)
	blocked, err := ClaimDueOutboxEvent(t.Context(), now, 10*time.Second)
	require.NoError(t, err)
	assert.Nil(t, blocked)

	control, err := GetPublicationDeliveryControl(t.Context(), now.Add(5*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, DeliveryStateEnabled, control.State)
	assert.Equal(t, "auto_resume", control.LastAction)
	recovered, err := ClaimDueOutboxEvent(t.Context(), now.Add(5*time.Minute), 10*time.Second)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	assert.Equal(t, repositoryPushEventType, recovered.EventType)
}

func TestPublicationDeliveryConcurrentChangeIDConflict(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(DeliveryControl))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	_, err = PausePublicationDelivery(t.Context(), PauseDeliveryRequest{
		ChangeID: "RCP-LEASE-0003", ReasonCode: "incident_containment", Until: now.Add(time.Minute),
	}, now)
	require.NoError(t, err)
	_, err = PausePublicationDelivery(t.Context(), PauseDeliveryRequest{
		ChangeID: "RCP-LEASE-0004", ReasonCode: "incident_containment", Until: now.Add(time.Minute),
	}, now)
	assert.True(t, errors.Is(err, ErrDeliveryControlConflict))
}
