// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/web"
)

const (
	maintenanceKeyEnvironment = "RIN_CONTROL_GITEA_MAINTENANCE_HMAC_KEY"
	healthKeyEnvironment      = "RIN_CONTROL_GITEA_HEALTH_HMAC_KEY"
	maintenanceService        = "control-plane-maintenance"
	healthService             = "control-plane-monitor"
	maintenanceSchema         = "rin-control-delivery-maintenance/v1"
	healthSchema              = "rin-publication-path-health/v1"
	maxMaintenanceBody        = 4 << 10
)

type MaintenanceServer struct {
	SigningKey []byte
	HealthKey  []byte
	Clock      func() time.Time

	nonceMu sync.Mutex
	nonces  map[string]int64
}

type publicationHealth struct {
	SchemaVersion    string  `json:"schemaVersion"`
	Capture          string  `json:"capture"`
	DeliveryState    string  `json:"deliveryState"`
	ChangeID         string  `json:"changeId,omitempty"`
	ReasonCode       string  `json:"reasonCode,omitempty"`
	PausedUntil      string  `json:"pausedUntil,omitempty"`
	Pending          int64   `json:"pending"`
	Delivering       int64   `json:"delivering"`
	Dead             int64   `json:"dead"`
	OldestAgeSeconds int64   `json:"oldestAgeSeconds"`
	LastCapturedAt   *string `json:"lastCapturedAt,omitempty"`
	LastDeliveredAt  *string `json:"lastDeliveredAt,omitempty"`
	CheckedAt        string  `json:"checkedAt"`
}

type maintenanceStatus struct {
	SchemaVersion string `json:"schemaVersion"`
	Capture       string `json:"capture"`
	DeliveryState string `json:"deliveryState"`
	ChangeID      string `json:"changeId,omitempty"`
	ReasonCode    string `json:"reasonCode,omitempty"`
	PausedUntil   string `json:"pausedUntil,omitempty"`
	Version       int64  `json:"version"`
}

type pauseMaintenanceRequest struct {
	ChangeID    string `json:"changeId"`
	ReasonCode  string `json:"reasonCode"`
	PausedUntil string `json:"pausedUntil"`
}

type resumeMaintenanceRequest struct {
	ChangeID string `json:"changeId"`
}

func MaintenanceRoutes() *web.Router {
	server := &MaintenanceServer{SigningKey: []byte(os.Getenv(maintenanceKeyEnvironment)), HealthKey: []byte(os.Getenv(healthKeyEnvironment))}
	router := web.NewRouter()
	router.AfterRouting(server.authenticate)
	router.Get("/delivery/status", server.status)
	router.Post("/delivery/pause", server.pause)
	router.Post("/delivery/resume", server.resume)
	router.Get("/publication/health", server.publicationHealth)
	return router
}

func (server *MaintenanceServer) publicationHealth(response http.ResponseWriter, request *http.Request) {
	now := server.now()
	control, err := rincontrol_model.GetPublicationDeliveryControl(request.Context(), now)
	if err != nil {
		log.Error("Rin Control publication health state failed: %v", err)
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	backlog, err := rincontrol_model.GetPublicationOutboxHealth(request.Context(), now)
	if err != nil {
		log.Error("Rin Control publication health backlog failed: %v", err)
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	status := publicationHealth{
		SchemaVersion: healthSchema, Capture: "enabled", DeliveryState: control.State,
		Pending: backlog.Pending, Delivering: backlog.Delivering, Dead: backlog.Dead,
		OldestAgeSeconds: int64(backlog.OldestAge / time.Second), CheckedAt: now.Format(time.RFC3339),
	}
	if control.State == rincontrol_model.DeliveryStatePaused {
		status.ChangeID, status.ReasonCode = control.ChangeID, control.ReasonCode
		status.PausedUntil = time.Unix(int64(control.PausedUntilUnix), 0).UTC().Format(time.RFC3339)
	}
	if backlog.LastCapturedAt != nil {
		value := backlog.LastCapturedAt.Format(time.RFC3339)
		status.LastCapturedAt = &value
	}
	if backlog.LastDeliveredAt != nil {
		value := backlog.LastDeliveredAt.Format(time.RFC3339)
		status.LastDeliveredAt = &value
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(status)
}

func (server *MaintenanceServer) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		key, service := server.SigningKey, maintenanceService
		if strings.HasSuffix(request.URL.Path, "/internal/v1/rin-control/publication/health") {
			key, service = server.HealthKey, healthService
		}
		if len(key) < 32 {
			http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, maxMaintenanceBody+1))
		if err != nil || len(body) > maxMaintenanceBody {
			http.Error(response, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !server.validSignature(request, body, key, service) {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (server *MaintenanceServer) status(response http.ResponseWriter, request *http.Request) {
	control, err := rincontrol_model.GetPublicationDeliveryControl(request.Context(), server.now())
	if err != nil {
		log.Error("Rin Control delivery maintenance status failed: %v", err)
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	server.writeStatus(response, control)
}

func (server *MaintenanceServer) pause(response http.ResponseWriter, request *http.Request) {
	var input pauseMaintenanceRequest
	if err := decodeMaintenanceJSON(request, &input); err != nil {
		http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	pausedUntil, err := time.Parse(time.RFC3339, input.PausedUntil)
	if err != nil {
		http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	control, err := rincontrol_model.PausePublicationDelivery(request.Context(), rincontrol_model.PauseDeliveryRequest{
		ChangeID: input.ChangeID, ReasonCode: input.ReasonCode, Until: pausedUntil,
	}, server.now())
	if err != nil {
		server.writeMutationError(response, err)
		return
	}
	server.writeStatus(response, control)
}

func (server *MaintenanceServer) resume(response http.ResponseWriter, request *http.Request) {
	var input resumeMaintenanceRequest
	if err := decodeMaintenanceJSON(request, &input); err != nil {
		http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	control, err := rincontrol_model.ResumePublicationDelivery(request.Context(), input.ChangeID, server.now())
	if err != nil {
		server.writeMutationError(response, err)
		return
	}
	server.writeStatus(response, control)
}

func (server *MaintenanceServer) writeMutationError(response http.ResponseWriter, err error) {
	if errors.Is(err, rincontrol_model.ErrDeliveryControlConflict) {
		http.Error(response, http.StatusText(http.StatusConflict), http.StatusConflict)
		return
	}
	if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "deadline") {
		http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	log.Error("Rin Control delivery maintenance mutation failed: %v", err)
	http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (server *MaintenanceServer) writeStatus(response http.ResponseWriter, control *rincontrol_model.DeliveryControl) {
	status := maintenanceStatus{
		SchemaVersion: maintenanceSchema, Capture: "enabled", DeliveryState: control.State, Version: control.Version,
	}
	if control.State == rincontrol_model.DeliveryStatePaused {
		status.ChangeID = control.ChangeID
		status.ReasonCode = control.ReasonCode
		status.PausedUntil = time.Unix(int64(control.PausedUntilUnix), 0).UTC().Format(time.RFC3339)
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(status)
}

func (server *MaintenanceServer) validSignature(request *http.Request, body, key []byte, expectedService string) bool {
	if request.Header.Get("X-Rin-Service") != expectedService {
		return false
	}
	timestamp, err := strconv.ParseInt(request.Header.Get("X-Rin-Timestamp"), 10, 64)
	if err != nil || timestamp <= 0 {
		return false
	}
	now := server.now().Unix()
	if timestamp < now-60 || timestamp > now+60 {
		return false
	}
	nonce := request.Header.Get("X-Rin-Nonce")
	if len(nonce) < 16 || len(nonce) > 128 {
		return false
	}
	signature, err := hex.DecodeString(request.Header.Get("X-Rin-Signature"))
	if err != nil || len(signature) != sha256.Size {
		return false
	}
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		request.Method, request.RequestURI, strconv.FormatInt(timestamp, 10), nonce, hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	return server.acceptNonce(nonce, now)
}

func (server *MaintenanceServer) acceptNonce(nonce string, now int64) bool {
	server.nonceMu.Lock()
	defer server.nonceMu.Unlock()
	if server.nonces == nil {
		server.nonces = make(map[string]int64)
	}
	for value, acceptedAt := range server.nonces {
		if acceptedAt < now-60 {
			delete(server.nonces, value)
		}
	}
	if _, exists := server.nonces[nonce]; exists || len(server.nonces) >= 10_000 {
		return false
	}
	server.nonces[nonce] = now
	return true
}

func (server *MaintenanceServer) now() time.Time {
	if server.Clock != nil {
		return server.Clock().UTC()
	}
	return time.Now().UTC()
}

func decodeMaintenanceJSON(request *http.Request, target any) error {
	decoder := json.NewDecoderDisallowUnknownFields(request.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("maintenance request contains trailing JSON")
	}
	return nil
}
