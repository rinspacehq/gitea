// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRepositoryPushOutboxEvent(t *testing.T) {
	when := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	event, err := BuildRepositoryPushOutboxEvent(RepositoryPushEvent{
		RepositoryID: 42,
		Ref:          "refs/heads/main",
		Before:       strings.Repeat("a", 40),
		After:        strings.Repeat("b", 40),
		OccurredAt:   when,
	})
	require.NoError(t, err)
	assert.Equal(t, "gitea-push:42:f921bd05e68b03740c450e565e0e6173e546193170b2dd404ddb6f153e9b5bf3:"+strings.Repeat("b", 40), event.EventID)
	assert.Equal(t, repositoryPushEventType, event.EventType)
	assert.Zero(t, event.ActorUserID)
	require.NoError(t, ValidateOutboxEvent(event))

	var envelope repositoryPushEnvelope
	require.NoError(t, json.Unmarshal([]byte(event.Payload), &envelope))
	assert.Equal(t, event.EventID, envelope.CorrelationID)
	assert.Equal(t, envelope.Data.After, envelope.Data.Commit)
	assert.Equal(t, when.Format(time.RFC3339Nano), envelope.OccurredAt)
}

func TestBuildRepositoryPushOutboxEventRejectsInvalidIdentity(t *testing.T) {
	base := RepositoryPushEvent{
		RepositoryID: 42, Ref: "refs/heads/main",
		Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40),
	}
	tests := []struct {
		name   string
		mutate func(*RepositoryPushEvent)
	}{
		{"zero repository", func(value *RepositoryPushEvent) { value.RepositoryID = 0 }},
		{"tag ref", func(value *RepositoryPushEvent) { value.Ref = "refs/tags/v1" }},
		{"zero after", func(value *RepositoryPushEvent) { value.After = strings.Repeat("0", 40) }},
		{"uppercase after", func(value *RepositoryPushEvent) { value.After = strings.Repeat("B", 40) }},
		{"mixed hash widths", func(value *RepositoryPushEvent) { value.Before = strings.Repeat("a", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			_, err := BuildRepositoryPushOutboxEvent(value)
			assert.Error(t, err)
		})
	}
}

func TestEnqueueRepositoryPushEventIsIdempotent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("event_type = ?", repositoryPushEventType).Delete(new(OutboxEvent))
	require.NoError(t, err)
	input := RepositoryPushEvent{
		RepositoryID: 77,
		Ref:          "refs/heads/publication",
		Before:       strings.Repeat("0", 64),
		After:        strings.Repeat("c", 64),
		OccurredAt:   time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	}
	require.NoError(t, EnqueueRepositoryPushEvent(t.Context(), input))
	input.Before = strings.Repeat("d", 64)
	input.OccurredAt = input.OccurredAt.Add(time.Minute)
	require.NoError(t, EnqueueRepositoryPushEvent(t.Context(), input))

	count, err := db.GetEngine(t.Context()).Where("event_type = ? AND repository_id = ?", repositoryPushEventType, input.RepositoryID).Count(new(OutboxEvent))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	stored := new(OutboxEvent)
	has, err := db.GetEngine(t.Context()).Where("event_type = ? AND repository_id = ?", repositoryPushEventType, input.RepositoryID).Get(stored)
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, ValidateOutboxEvent(stored))
	assert.Greater(t, len(stored.EventID), 96)
}
