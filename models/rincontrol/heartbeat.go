// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

const (
	PublicationHeartbeatEventType = "publication.pipeline.heartbeat"
	publicationHeartbeatSchema    = "rin-publication-path-heartbeat/v1"
	publicationHeartbeatInterval  = 30 * time.Second
)

type publicationHeartbeatEnvelope struct {
	Schema        string                   `json:"schema"`
	EventID       string                   `json:"event_id"`
	Type          string                   `json:"type"`
	OccurredAt    string                   `json:"occurred_at"`
	Producer      string                   `json:"producer"`
	CorrelationID string                   `json:"correlation_id"`
	Subject       publicationHeartbeatSub  `json:"subject"`
	Data          publicationHeartbeatData `json:"data"`
}

type publicationHeartbeatSub struct {
	Path string `json:"path"`
}

type publicationHeartbeatData struct {
	SchemaVersion   string `json:"schemaVersion"`
	ProducerVersion string `json:"producerVersion"`
	Bucket          int64  `json:"bucket"`
}

func PublicationHeartbeatEventID(now time.Time) (string, int64, error) {
	if now.IsZero() {
		return "", 0, errors.New("publication heartbeat time is required")
	}
	bucket := now.UTC().Unix() / int64(publicationHeartbeatInterval/time.Second) * int64(publicationHeartbeatInterval/time.Second)
	return "gitea-publication-heartbeat:" + strconv.FormatInt(bucket, 10), bucket, nil
}

func BuildPublicationHeartbeatOutboxEvent(now time.Time, producerVersion string) (*OutboxEvent, error) {
	eventID, bucket, err := PublicationHeartbeatEventID(now)
	if err != nil {
		return nil, err
	}
	producerVersion = strings.TrimSpace(producerVersion)
	if producerVersion == "" || len(producerVersion) > 128 {
		return nil, errors.New("publication heartbeat producer version is invalid")
	}
	occurredAt := time.Unix(bucket, 0).UTC()
	envelope := publicationHeartbeatEnvelope{
		Schema: EventSchemaV1, EventID: eventID, Type: PublicationHeartbeatEventType,
		OccurredAt: occurredAt.Format(time.RFC3339Nano), Producer: "gitea", CorrelationID: eventID,
		Subject: publicationHeartbeatSub{Path: "publication"},
		Data:    publicationHeartbeatData{SchemaVersion: publicationHeartbeatSchema, ProducerVersion: producerVersion, Bucket: bucket},
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return &OutboxEvent{
		EventID: eventID, EventType: PublicationHeartbeatEventType,
		AggregateType: "pipeline", AggregateID: 1, Payload: string(payload),
		State: OutboxStatePending, NextAttemptUnix: timeutil.TimeStamp(bucket),
	}, nil
}

// EnqueuePublicationHeartbeat keeps at most one undelivered heartbeat fact in
// normal operation. A pause or outage therefore cannot create a historical
// heartbeat backlog while the latest undelivered fact still traverses the same
// durable publication outbox as repository pushes.
func EnqueuePublicationHeartbeat(ctx context.Context, now time.Time, producerVersion string) error {
	event, err := BuildPublicationHeartbeatOutboxEvent(now, producerVersion)
	if err != nil {
		return err
	}
	exists, err := db.GetEngine(ctx).
		Where("event_type = ? AND state IN (?, ?, ?)", PublicationHeartbeatEventType, OutboxStatePending, OutboxStateDelivering, OutboxStateDead).
		Exist(new(OutboxEvent))
	if err != nil || exists {
		return err
	}
	if err := db.Insert(ctx, event); err == nil {
		return nil
	}
	stored := new(OutboxEvent)
	has, getErr := db.GetEngine(ctx).Where("event_id = ?", event.EventID).Get(stored)
	if getErr != nil || !has {
		return err
	}
	if validateErr := validatePublicationHeartbeatOutboxEvent(stored); validateErr != nil || stored.Payload != event.Payload {
		return ErrOutboxIdempotencyConflict
	}
	return nil
}

func validatePublicationHeartbeatOutboxEvent(event *OutboxEvent) error {
	if event.EventType != PublicationHeartbeatEventType || event.AggregateType != "pipeline" || event.AggregateID != 1 || event.ActorUserID != 0 || event.TargetUserID != 0 || event.RepositoryID != 0 {
		return errors.New("publication heartbeat outbox identity is invalid")
	}
	var envelope publicationHeartbeatEnvelope
	decoder := json.NewDecoder(strings.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode publication heartbeat envelope: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("publication heartbeat envelope contains trailing JSON")
	}
	expectedID, bucket, err := PublicationHeartbeatEventID(time.Unix(envelope.Data.Bucket, 0))
	if err != nil || envelope.Schema != EventSchemaV1 || envelope.EventID != event.EventID || expectedID != event.EventID || bucket != envelope.Data.Bucket || envelope.Type != PublicationHeartbeatEventType || envelope.Producer != "gitea" || envelope.CorrelationID != event.EventID || envelope.Subject.Path != "publication" || envelope.Data.SchemaVersion != publicationHeartbeatSchema || strings.TrimSpace(envelope.Data.ProducerVersion) == "" || len(envelope.Data.ProducerVersion) > 128 {
		return errors.New("publication heartbeat envelope identity is inconsistent")
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, envelope.OccurredAt)
	if err != nil || occurredAt.UTC().Unix() != bucket {
		return errors.New("publication heartbeat envelope time is invalid")
	}
	return nil
}
