// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	rincontrol_model "gitea.dev/models/rincontrol"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/log"
)

const (
	endpointEnvironment = "RIN_CONTROL_EVENT_ENDPOINT"
	keyEnvironment      = "RIN_CONTROL_GITEA_EVENT_HMAC_KEY"
)

type Dispatcher struct {
	Endpoint      string
	SigningKey    []byte
	HTTPClient    *http.Client
	PollInterval  time.Duration
	LeaseDuration time.Duration
	Clock         func() time.Time
}

type HeartbeatProducer struct {
	ProducerVersion string
	Interval        time.Duration
	Clock           func() time.Time
}

func (producer *HeartbeatProducer) ProcessOnce(ctx context.Context) error {
	return rincontrol_model.EnqueuePublicationHeartbeat(ctx, producer.now(), producer.ProducerVersion)
}

func (producer *HeartbeatProducer) Run(ctx context.Context) {
	interval := producer.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := producer.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("Rin Control publication heartbeat enqueue failed: error_code=heartbeat_enqueue_failed")
			}
			timer.Reset(interval)
		}
	}
}

func (producer *HeartbeatProducer) now() time.Time {
	if producer.Clock != nil {
		return producer.Clock().UTC()
	}
	return time.Now().UTC()
}

func (dispatcher *Dispatcher) Validate() error {
	endpoint, err := url.Parse(dispatcher.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return errors.New("Rin Control event endpoint is invalid")
	}
	if len(dispatcher.SigningKey) < 32 {
		return errors.New("Rin Control Gitea event HMAC key must contain at least 32 bytes")
	}
	return nil
}

func (dispatcher *Dispatcher) ProcessOnce(ctx context.Context) (bool, error) {
	if err := dispatcher.Validate(); err != nil {
		return false, err
	}
	now := dispatcher.now()
	leaseDuration := dispatcher.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 30 * time.Second
	}
	event, err := rincontrol_model.ClaimDueOutboxEvent(ctx, now, leaseDuration)
	if err != nil || event == nil {
		return false, err
	}
	code := dispatcher.deliver(ctx, event)
	if code == "" {
		confirmed, err := rincontrol_model.ConfirmOutboxDelivery(ctx, event.ID, event.LeaseToken, dispatcher.now())
		if err != nil {
			return true, err
		}
		if !confirmed {
			return true, errors.New("Rin Control outbox delivery lease was lost before confirmation")
		}
		return true, nil
	}
	failed, err := rincontrol_model.FailOutboxDelivery(ctx, event, code, dispatcher.now())
	if err != nil {
		return true, err
	}
	if !failed {
		return true, errors.New("Rin Control outbox delivery lease was lost after failure")
	}
	log.Warn("Rin Control event delivery failed event_id=%s event_type=%s outbox_id=%d attempt=%d error_code=delivery_failed cause=%s", event.EventID, event.EventType, event.ID, event.Attempts, code)
	return true, fmt.Errorf("Rin Control event delivery failed: %s", code)
}

func (dispatcher *Dispatcher) deliver(ctx context.Context, event *rincontrol_model.OutboxEvent) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, dispatcher.Endpoint, bytes.NewBufferString(event.Payload))
	if err != nil {
		return "request_invalid"
	}
	request.Header.Set("Content-Type", "application/json")
	if err := signRequest(request, dispatcher.SigningKey, []byte(event.Payload), dispatcher.now()); err != nil {
		return "signing_failed"
	}
	client := dispatcher.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return "transport_failed"
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Sprintf("http_%d", response.StatusCode)
	}
	return ""
}

func (dispatcher *Dispatcher) Run(ctx context.Context) {
	interval := dispatcher.PollInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			processed, err := dispatcher.ProcessOnce(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("Rin Control outbox dispatch failed: %v", err)
			}
			if processed && err == nil {
				timer.Reset(0)
			} else {
				timer.Reset(interval)
			}
		}
	}
}

// Init starts the dispatcher only when both required settings are present. A
// partial configuration fails startup instead of silently dropping events.
func Init() error {
	if _, err := rincontrol_model.GetPublicationDeliveryControl(context.Background(), time.Now().UTC()); err != nil {
		return fmt.Errorf("initialize Rin Control publication delivery state: %w", err)
	}
	endpoint := strings.TrimSpace(os.Getenv(endpointEnvironment))
	key := []byte(os.Getenv(keyEnvironment))
	if endpoint == "" && len(key) == 0 {
		return nil
	}
	dispatcher := &Dispatcher{Endpoint: endpoint, SigningKey: key}
	if err := dispatcher.Validate(); err != nil {
		return err
	}
	healthKey := []byte(os.Getenv(healthKeyEnvironment))
	maintenanceKey := []byte(os.Getenv(maintenanceKeyEnvironment))
	if len(healthKey) < 32 || bytes.Equal(healthKey, key) || (len(maintenanceKey) > 0 && bytes.Equal(healthKey, maintenanceKey)) {
		return errors.New("Rin Control Gitea health HMAC key must be purpose-specific and contain at least 32 bytes")
	}
	go graceful.GetManager().RunWithShutdownContext(dispatcher.Run)
	producerVersion := strings.TrimSpace(os.Getenv("RINSPACE_REVISION"))
	if producerVersion == "" {
		producerVersion = "unknown"
	}
	heartbeat := &HeartbeatProducer{ProducerVersion: producerVersion}
	go graceful.GetManager().RunWithShutdownContext(heartbeat.Run)
	return nil
}

func signRequest(request *http.Request, key, body []byte, now time.Time) error {
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	timestamp := strconv.FormatInt(now.UTC().Unix(), 10)
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{request.Method, request.URL.RequestURI(), timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("X-Rin-Service", "gitea")
	request.Header.Set("X-Rin-Timestamp", timestamp)
	request.Header.Set("X-Rin-Nonce", nonce)
	request.Header.Set("X-Rin-Signature", hex.EncodeToString(mac.Sum(nil)))
	return nil
}

func (dispatcher *Dispatcher) now() time.Time {
	if dispatcher.Clock != nil {
		return dispatcher.Clock().UTC()
	}
	return time.Now().UTC()
}
