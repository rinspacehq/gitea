// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
	"gitea.dev/modules/timeutil"
)

const repositoryPushEventType = "repository.push.completed"

type RepositoryPushEvent struct {
	RepositoryID int64
	Ref          string
	Before       string
	After        string
	OccurredAt   time.Time
}

type repositoryPushEnvelope struct {
	Schema        string                  `json:"schema"`
	EventID       string                  `json:"event_id"`
	Type          string                  `json:"type"`
	OccurredAt    string                  `json:"occurred_at"`
	Producer      string                  `json:"producer"`
	CorrelationID string                  `json:"correlation_id"`
	Subject       repositoryPushSubject   `json:"subject"`
	Data          repositoryPushEventData `json:"data"`
}

type repositoryPushSubject struct {
	RepositoryID int64 `json:"repositoryId"`
}

type repositoryPushEventData struct {
	RepositoryID int64  `json:"repositoryId"`
	Ref          string `json:"ref"`
	Before       string `json:"before"`
	After        string `json:"after"`
	Commit       string `json:"commit"`
}

func RepositoryPushEventID(repositoryID int64, ref, after string) (string, error) {
	if repositoryID <= 0 {
		return "", errors.New("repository ID must be positive")
	}
	if err := validateRepositoryPushRef(ref); err != nil {
		return "", err
	}
	if !isRepositoryObjectID(after) || isZeroRepositoryObjectID(after) {
		return "", errors.New("after must be a non-zero lowercase object ID")
	}
	sum := sha256.Sum256([]byte(ref))
	return "gitea-push:" + strconv.FormatInt(repositoryID, 10) + ":" + hex.EncodeToString(sum[:]) + ":" + after, nil
}

func BuildRepositoryPushOutboxEvent(input RepositoryPushEvent) (*OutboxEvent, error) {
	if !isRepositoryObjectID(input.Before) {
		return nil, errors.New("before must be a lowercase object ID")
	}
	if len(input.Before) != len(input.After) {
		return nil, errors.New("before and after object IDs must have equal length")
	}
	eventID, err := RepositoryPushEventID(input.RepositoryID, input.Ref, input.After)
	if err != nil {
		return nil, err
	}
	occurredAt := input.OccurredAt.UTC()
	if input.OccurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	envelope := repositoryPushEnvelope{
		Schema: EventSchemaV1, EventID: eventID, Type: repositoryPushEventType,
		OccurredAt: occurredAt.Format(time.RFC3339Nano), Producer: "gitea", CorrelationID: eventID,
		Subject: repositoryPushSubject{RepositoryID: input.RepositoryID},
		Data: repositoryPushEventData{
			RepositoryID: input.RepositoryID, Ref: input.Ref, Before: input.Before,
			After: input.After, Commit: input.After,
		},
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return &OutboxEvent{
		EventID: eventID, EventType: repositoryPushEventType,
		AggregateType: "repository", AggregateID: input.RepositoryID, RepositoryID: input.RepositoryID,
		Payload: string(payload), State: OutboxStatePending,
		NextAttemptUnix: timeutil.TimeStamp(occurredAt.Unix()),
	}, nil
}

// EnqueueRepositoryPushEvent durably inserts one event per repository/ref/tip.
// Repeated notifications for the same deterministic identity are successful.
func EnqueueRepositoryPushEvent(ctx context.Context, input RepositoryPushEvent) error {
	event, err := BuildRepositoryPushOutboxEvent(input)
	if err != nil {
		return err
	}
	if err := ValidateOutboxEvent(event); err != nil {
		return err
	}
	if err := db.Insert(ctx, event); err == nil {
		return nil
	} else {
		stored := new(OutboxEvent)
		has, getErr := db.GetEngine(ctx).Where("event_id = ?", event.EventID).Get(stored)
		if getErr != nil || !has {
			return err
		}
		if validateErr := validateRepositoryPushOutboxEvent(stored); validateErr != nil || stored.RepositoryID != event.RepositoryID {
			return ErrOutboxIdempotencyConflict
		}
		return nil
	}
}

func validateRepositoryPushRef(ref string) error {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) || len(ref) > 1024 {
		return errors.New("ref must be a branch ref of at most 1024 bytes")
	}
	suffix := ref[len(prefix):]
	if suffix == "" {
		return errors.New("ref has an empty branch name")
	}
	for _, r := range ref {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`\~^:?*[`, r) {
			return errors.New("ref contains a forbidden character")
		}
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") {
		return errors.New("ref contains a forbidden sequence")
	}
	if strings.HasSuffix(suffix, "/") || strings.HasSuffix(suffix, ".") {
		return errors.New("ref branch name has a forbidden suffix")
	}
	for _, component := range strings.Split(suffix, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return errors.New("ref branch name has an invalid component")
		}
	}
	return nil
}

func isRepositoryObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && !bytes.ContainsAny([]byte(value), "ABCDEF")
}

func isZeroRepositoryObjectID(value string) bool {
	for i := range len(value) {
		if value[i] != '0' {
			return false
		}
	}
	return true
}

func validateRepositoryPushOutboxEvent(event *OutboxEvent) error {
	if event.EventType != repositoryPushEventType || event.AggregateType != "repository" || event.AggregateID <= 0 || event.RepositoryID != event.AggregateID || event.ActorUserID != 0 || event.TargetUserID != 0 {
		return errors.New("repository push outbox identity is invalid")
	}
	var envelope repositoryPushEnvelope
	decoder := json.NewDecoderDisallowUnknownFields(strings.NewReader(event.Payload))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode repository push envelope: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("repository push envelope contains trailing JSON")
	}
	if envelope.Schema != EventSchemaV1 || envelope.EventID != event.EventID || envelope.Type != repositoryPushEventType || envelope.Producer != "gitea" || envelope.CorrelationID != event.EventID || envelope.Subject.RepositoryID != event.RepositoryID || envelope.Data.RepositoryID != event.RepositoryID || envelope.Data.After != envelope.Data.Commit {
		return errors.New("repository push envelope identity is inconsistent")
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.OccurredAt); err != nil {
		return errors.New("repository push envelope time is invalid")
	}
	expectedID, err := RepositoryPushEventID(event.RepositoryID, envelope.Data.Ref, envelope.Data.After)
	if err != nil || expectedID != event.EventID || !isRepositoryObjectID(envelope.Data.Before) || len(envelope.Data.Before) != len(envelope.Data.After) {
		return errors.New("repository push envelope data is invalid")
	}
	return nil
}
