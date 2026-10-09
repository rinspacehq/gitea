// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitea.dev/models/db"
	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDispatcherSignsRetriesAndConfirms(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.OutboxEvent))
	require.NoError(t, err)
	key := []byte("0123456789abcdef0123456789abcdef")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		assert.Equal(t, "gitea", request.Header.Get("X-Rin-Service"))
		assert.True(t, validSignature(request, key, body))
		if requests.Add(1) == 1 {
			http.Error(response, "temporary", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	now := time.Now().UTC().Truncate(time.Second)
	dispatcher := &Dispatcher{Endpoint: server.URL + "/internal/v1/events/gitea?version=1", SigningKey: key, Clock: func() time.Time { return now }}
	require.NoError(t, rincontrol_model.EnqueueSocialEvent(t.Context(), rincontrol_model.SocialEvent{EventType: "user.follow.changed", ActorUserID: 5, TargetUserID: 6, Active: true}))

	processed, err := dispatcher.ProcessOnce(t.Context())
	assert.True(t, processed)
	assert.ErrorContains(t, err, "http_503")
	now = now.Add(time.Second)
	processed, err = dispatcher.ProcessOnce(t.Context())
	require.NoError(t, err)
	assert.True(t, processed)
	processed, err = dispatcher.ProcessOnce(t.Context())
	require.NoError(t, err)
	assert.False(t, processed)
	assert.EqualValues(t, 2, requests.Load())

	stored := new(rincontrol_model.OutboxEvent)
	has, err := db.GetEngine(t.Context()).Where("event_type = ?", "user.follow.changed").Get(stored)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, rincontrol_model.OutboxStateDelivered, stored.State)
}

func TestDispatcherConfigurationFailsClosed(t *testing.T) {
	t.Setenv(endpointEnvironment, "http://control.test/internal/v1/events/gitea")
	t.Setenv(keyEnvironment, "short")
	t.Setenv(healthKeyEnvironment, strings.Repeat("h", 32))
	assert.Error(t, Init())
	assert.Error(t, (&Dispatcher{Endpoint: "file:///tmp/events", SigningKey: make([]byte, 32)}).Validate())
}

func TestDispatcherConfigurationRequiresPurposeSpecificHealthKey(t *testing.T) {
	key := strings.Repeat("g", 32)
	t.Setenv(endpointEnvironment, "http://control.test/internal/v1/events/gitea")
	t.Setenv(keyEnvironment, key)
	t.Setenv(healthKeyEnvironment, key)
	assert.ErrorContains(t, Init(), "purpose-specific")
}

func TestHeartbeatProducerUsesPublicationOutbox(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.OutboxEvent))
	require.NoError(t, err)
	now := time.Unix(1_800_000_030, 0).UTC()
	producer := &HeartbeatProducer{ProducerVersion: "release-a", Clock: func() time.Time { return now }}
	require.NoError(t, producer.ProcessOnce(t.Context()))
	stored := new(rincontrol_model.OutboxEvent)
	has, err := db.GetEngine(t.Context()).Where("event_type = ?", rincontrol_model.PublicationHeartbeatEventType).Get(stored)
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, rincontrol_model.ValidateOutboxEvent(stored))
}

func TestDispatcherDeliversRepositoryPushEvent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.OutboxEvent))
	require.NoError(t, err)
	key := []byte("0123456789abcdef0123456789abcdef")
	var delivered map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		assert.True(t, validSignature(request, key, body))
		require.NoError(t, json.Unmarshal(body, &delivered))
		response.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	require.NoError(t, rincontrol_model.EnqueueRepositoryPushEvent(t.Context(), rincontrol_model.RepositoryPushEvent{
		RepositoryID: 42, Ref: "refs/heads/main",
		Before: strings.Repeat("a", 40), After: strings.Repeat("b", 40), OccurredAt: now,
	}))
	dispatcher := &Dispatcher{Endpoint: server.URL + "/internal/v1/events/gitea", SigningKey: key, Clock: func() time.Time { return now }}
	processed, err := dispatcher.ProcessOnce(t.Context())
	require.NoError(t, err)
	require.True(t, processed)
	assert.Equal(t, "repository.push.completed", delivered["type"])
	data, ok := delivered["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "refs/heads/main", data["ref"])
	assert.Equal(t, strings.Repeat("b", 40), data["commit"])
}

func validSignature(request *http.Request, key, body []byte) bool {
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		request.Method, request.URL.RequestURI(), request.Header.Get("X-Rin-Timestamp"), request.Header.Get("X-Rin-Nonce"), hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	provided, err := hex.DecodeString(request.Header.Get("X-Rin-Signature"))
	return err == nil && hmac.Equal(provided, mac.Sum(nil))
}
