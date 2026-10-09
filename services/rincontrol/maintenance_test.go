// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaintenanceAPIAuthenticationIdempotencyAndConflicts(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.DeliveryControl))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	key := []byte("0123456789abcdef0123456789abcdef-maintenance")
	server := &MaintenanceServer{SigningKey: key, Clock: func() time.Time { return now }}
	pauseHandler := server.authenticate(http.HandlerFunc(server.pause))
	statusHandler := server.authenticate(http.HandlerFunc(server.status))
	resumeHandler := server.authenticate(http.HandlerFunc(server.resume))
	path := "/internal/v1/rin-control/delivery/pause"
	body := []byte(`{"changeId":"RCP-API-LEASE-01","reasonCode":"credential_rotation","pausedUntil":"2027-01-15T08:10:00Z"}`)

	unauthorized := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	unauthorizedResult := httptest.NewRecorder()
	pauseHandler.ServeHTTP(unauthorizedResult, unauthorized)
	assert.Equal(t, http.StatusUnauthorized, unauthorizedResult.Code)

	first := signedMaintenanceRequest(t, key, http.MethodPost, path, body, "nonce-api-request-0001", now)
	firstResult := httptest.NewRecorder()
	pauseHandler.ServeHTTP(firstResult, first)
	require.Equal(t, http.StatusOK, firstResult.Code)
	var firstStatus maintenanceStatus
	require.NoError(t, json.Unmarshal(firstResult.Body.Bytes(), &firstStatus))
	assert.Equal(t, "enabled", firstStatus.Capture)
	assert.Equal(t, rincontrol_model.DeliveryStatePaused, firstStatus.DeliveryState)
	assert.NotEmpty(t, firstStatus.PausedUntil)

	replay := signedMaintenanceRequest(t, key, http.MethodPost, path, body, "nonce-api-request-0002", now)
	replayResult := httptest.NewRecorder()
	pauseHandler.ServeHTTP(replayResult, replay)
	require.Equal(t, http.StatusOK, replayResult.Code)
	var replayStatus maintenanceStatus
	require.NoError(t, json.Unmarshal(replayResult.Body.Bytes(), &replayStatus))
	assert.Equal(t, firstStatus.Version, replayStatus.Version)

	conflictBody := []byte(`{"changeId":"RCP-API-LEASE-01","reasonCode":"credential_rotation","pausedUntil":"2027-01-15T08:10:01Z"}`)
	conflict := signedMaintenanceRequest(t, key, http.MethodPost, path, conflictBody, "nonce-api-request-0003", now)
	conflictResult := httptest.NewRecorder()
	pauseHandler.ServeHTTP(conflictResult, conflict)
	assert.Equal(t, http.StatusConflict, conflictResult.Code)

	statusPath := "/internal/v1/rin-control/delivery/status"
	statusRequest := signedMaintenanceRequest(t, key, http.MethodGet, statusPath, nil, "nonce-api-request-0004", now)
	statusResult := httptest.NewRecorder()
	statusHandler.ServeHTTP(statusResult, statusRequest)
	assert.Equal(t, http.StatusOK, statusResult.Code)
	assert.Equal(t, "no-store", statusResult.Header().Get("Cache-Control"))

	resumePath := "/internal/v1/rin-control/delivery/resume"
	resumeBody := []byte(`{"changeId":"RCP-API-LEASE-01"}`)
	resume := signedMaintenanceRequest(t, key, http.MethodPost, resumePath, resumeBody, "nonce-api-request-0005", now)
	resumeResult := httptest.NewRecorder()
	resumeHandler.ServeHTTP(resumeResult, resume)
	require.Equal(t, http.StatusOK, resumeResult.Code)
	var resumed maintenanceStatus
	require.NoError(t, json.Unmarshal(resumeResult.Body.Bytes(), &resumed))
	assert.Equal(t, rincontrol_model.DeliveryStateEnabled, resumed.DeliveryState)
	assert.Empty(t, resumed.ChangeID)

	replayedNonce := signedMaintenanceRequest(t, key, http.MethodPost, resumePath, resumeBody, "nonce-api-request-0005", now)
	replayedNonceResult := httptest.NewRecorder()
	resumeHandler.ServeHTTP(replayedNonceResult, replayedNonce)
	assert.Equal(t, http.StatusUnauthorized, replayedNonceResult.Code)
}

func TestMaintenanceAPIRejectsUnboundedAndMalformedRequests(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.DeliveryControl))
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0).UTC()
	key := []byte("0123456789abcdef0123456789abcdef-maintenance")
	server := &MaintenanceServer{SigningKey: key, Clock: func() time.Time { return now }}
	handler := server.authenticate(http.HandlerFunc(server.pause))
	path := "/internal/v1/rin-control/delivery/pause"

	malformed := []byte(`{"changeId":"RCP-API-LEASE-02","reasonCode":"arbitrary","pausedUntil":"never"}`)
	request := signedMaintenanceRequest(t, key, http.MethodPost, path, malformed, "nonce-api-request-1001", now)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	assert.Equal(t, http.StatusBadRequest, result.Code)

	oversized := bytes.Repeat([]byte("x"), maxMaintenanceBody+1)
	request = signedMaintenanceRequest(t, key, http.MethodPost, path, oversized, "nonce-api-request-1002", now)
	result = httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	assert.Equal(t, http.StatusRequestEntityTooLarge, result.Code)

	server.SigningKey = []byte("short")
	request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
	result = httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	assert.Equal(t, http.StatusServiceUnavailable, result.Code)
}

func TestPublicationHealthUsesReadOnlyMonitorIdentity(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.OutboxEvent))
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).Where("1 = 1").Delete(new(rincontrol_model.DeliveryControl))
	require.NoError(t, err)
	now := time.Unix(1_800_000_030, 0).UTC()
	healthKey := []byte("0123456789abcdef0123456789abcdef-health")
	server := &MaintenanceServer{SigningKey: []byte("0123456789abcdef0123456789abcdef-maintenance"), HealthKey: healthKey, Clock: func() time.Time { return now }}
	require.NoError(t, rincontrol_model.EnqueuePublicationHeartbeat(t.Context(), now, "release-a"))
	path := "/repos/internal/v1/rin-control/publication/health"
	handler := http.StripPrefix("/repos", server.authenticate(http.HandlerFunc(server.publicationHealth)))

	wrongIdentity := signedServiceRequest(t, healthKey, maintenanceService, http.MethodGet, path, nil, "nonce-health-request-0001", now)
	wrongResult := httptest.NewRecorder()
	handler.ServeHTTP(wrongResult, wrongIdentity)
	assert.Equal(t, http.StatusUnauthorized, wrongResult.Code)

	request := signedServiceRequest(t, healthKey, healthService, http.MethodGet, path, nil, "nonce-health-request-0002", now)
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	require.Equal(t, http.StatusOK, result.Code)
	assert.NotContains(t, result.Body.String(), "payload")
	assert.NotContains(t, result.Body.String(), "repository")
	var health publicationHealth
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &health))
	assert.Equal(t, "enabled", health.Capture)
	assert.Equal(t, rincontrol_model.DeliveryStateEnabled, health.DeliveryState)
	assert.EqualValues(t, 1, health.Pending)
}

func signedMaintenanceRequest(t *testing.T, key []byte, method, path string, body []byte, nonce string, now time.Time) *http.Request {
	return signedServiceRequest(t, key, maintenanceService, method, path, body, nonce, now)
}

func signedServiceRequest(t *testing.T, key []byte, service, method, path string, body []byte, nonce string, now time.Time) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	bodyHash := sha256.Sum256(body)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	canonical := strings.Join([]string{method, path, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-Rin-Service", service)
	request.Header.Set("X-Rin-Timestamp", timestamp)
	request.Header.Set("X-Rin-Nonce", nonce)
	request.Header.Set("X-Rin-Signature", hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("Content-Type", "application/json")
	return request
}
