// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
	"gitea.dev/modules/timeutil"
)

const EventSchemaV1 = "rin-control-event/v1"

const (
	OutboxStatePending    = "pending"
	OutboxStateDelivering = "delivering"
	OutboxStateDelivered  = "delivered"
	OutboxStateDead       = "dead"
	MaxDeliveryAttempts   = 10
)

var ErrOutboxIdempotencyConflict = errors.New("Rin Control outbox event identity conflicts with stored payload")

type OutboxEvent struct {
	ID              int64              `xorm:"pk autoincr"`
	EventID         string             `xorm:"UNIQUE VARCHAR(240) NOT NULL"`
	EventType       string             `xorm:"INDEX VARCHAR(64) NOT NULL"`
	AggregateType   string             `xorm:"INDEX(s) VARCHAR(16) NOT NULL"`
	AggregateID     int64              `xorm:"INDEX(s) NOT NULL"`
	ActorUserID     int64              `xorm:"INDEX NOT NULL"`
	TargetUserID    int64              `xorm:"INDEX NOT NULL DEFAULT 0"`
	RepositoryID    int64              `xorm:"INDEX NOT NULL DEFAULT 0"`
	Payload         string             `xorm:"LONGTEXT NOT NULL"`
	State           string             `xorm:"INDEX VARCHAR(16) NOT NULL DEFAULT 'pending'"`
	Attempts        int                `xorm:"NOT NULL DEFAULT 0"`
	NextAttemptUnix timeutil.TimeStamp `xorm:"INDEX NOT NULL DEFAULT 0"`
	LeaseToken      string             `xorm:"VARCHAR(96) NOT NULL DEFAULT ''"`
	LeaseUntilUnix  timeutil.TimeStamp `xorm:"INDEX NOT NULL DEFAULT 0"`
	LastErrorCode   string             `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
	CreatedUnix     timeutil.TimeStamp `xorm:"created INDEX"`
	UpdatedUnix     timeutil.TimeStamp `xorm:"updated"`
	DeliveredUnix   timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
}

func (*OutboxEvent) TableName() string { return "rin_control_outbox" }

func init() {
	db.RegisterModel(new(OutboxEvent))
}

type SocialEvent struct {
	EventType    string
	ActorUserID  int64
	TargetUserID int64
	RepositoryID int64
	Active       bool
}

func EnqueueSocialEvent(ctx context.Context, event SocialEvent) error {
	aggregateType, aggregateID := "repository", event.RepositoryID
	switch event.EventType {
	case "repository.star.changed", "repository.watch.changed":
		if event.ActorUserID <= 0 || event.RepositoryID <= 0 || event.TargetUserID != 0 {
			return errors.New("repository social event identity is invalid")
		}
	case "user.follow.changed":
		aggregateType, aggregateID = "user", event.TargetUserID
		if event.ActorUserID <= 0 || event.TargetUserID <= 0 || event.RepositoryID != 0 || event.ActorUserID == event.TargetUserID {
			return errors.New("user follow event identity is invalid")
		}
	default:
		return fmt.Errorf("unsupported Rin Control social event %q", event.EventType)
	}
	eventID, err := newEventID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	envelope := map[string]any{
		"schema": EventSchemaV1, "event_id": eventID, "type": event.EventType,
		"occurred_at": now.Format(time.RFC3339Nano), "producer": "gitea",
		"correlation_id": eventID,
		"subject":        map[string]any{"aggregateType": aggregateType, "aggregateId": aggregateID},
		"data":           map[string]any{"actorUserId": event.ActorUserID, "targetUserId": event.TargetUserID, "repositoryId": event.RepositoryID, "active": event.Active},
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return db.Insert(ctx, &OutboxEvent{
		EventID: eventID, EventType: event.EventType, AggregateType: aggregateType, AggregateID: aggregateID,
		ActorUserID: event.ActorUserID, TargetUserID: event.TargetUserID, RepositoryID: event.RepositoryID,
		Payload: string(payload), State: OutboxStatePending, NextAttemptUnix: timeutil.TimeStamp(now.Unix()),
	})
}

// ClaimDueOutboxEvent leases one due event in a short transaction. The
// conditional update makes competing Gitea processes safe without holding a
// database lock during the network request.
func ClaimDueOutboxEvent(ctx context.Context, now time.Time, leaseDuration time.Duration) (*OutboxEvent, error) {
	if leaseDuration <= 0 {
		return nil, errors.New("Rin Control outbox lease duration must be positive")
	}
	leaseToken, err := newEventID()
	if err != nil {
		return nil, err
	}
	var claimed *OutboxEvent
	err = db.WithTx(ctx, func(ctx context.Context) error {
		control, err := effectivePublicationDeliveryControl(ctx, now.UTC())
		if err != nil {
			return err
		}
		var candidate OutboxEvent
		unixNow := timeutil.TimeStamp(now.UTC().Unix())
		candidateQuery := db.GetEngine(ctx).
			Where("(state = ? AND next_attempt_unix <= ?) OR (state = ? AND lease_until_unix <= ?)", OutboxStatePending, unixNow, OutboxStateDelivering, unixNow)
		if control.State == DeliveryStatePaused {
			candidateQuery = candidateQuery.NotIn("event_type", repositoryPushEventType, PublicationHeartbeatEventType)
		}
		has, err := candidateQuery.Asc("id").Get(&candidate)
		if err != nil || !has {
			return err
		}
		leaseUntil := timeutil.TimeStamp(now.UTC().Add(leaseDuration).Unix())
		result, err := db.GetEngine(ctx).ID(candidate.ID).
			Where("(state = ? AND next_attempt_unix <= ?) OR (state = ? AND lease_until_unix <= ?)", OutboxStatePending, unixNow, OutboxStateDelivering, unixNow).
			Cols("state", "attempts", "lease_token", "lease_until_unix", "last_error_code").
			Update(&OutboxEvent{State: OutboxStateDelivering, Attempts: candidate.Attempts + 1, LeaseToken: leaseToken, LeaseUntilUnix: leaseUntil})
		if err != nil {
			return err
		}
		if result == 0 {
			return nil
		}
		candidate.State = OutboxStateDelivering
		candidate.Attempts++
		candidate.LeaseToken = leaseToken
		candidate.LeaseUntilUnix = leaseUntil
		candidate.LastErrorCode = ""
		claimed = &candidate
		return nil
	})
	return claimed, err
}

// ConfirmOutboxDelivery completes only the lease held by the caller.
func ConfirmOutboxDelivery(ctx context.Context, eventID int64, leaseToken string, now time.Time) (bool, error) {
	result, err := db.GetEngine(ctx).ID(eventID).Where("state = ? AND lease_token = ?", OutboxStateDelivering, leaseToken).
		Cols("state", "lease_token", "lease_until_unix", "delivered_unix").
		Update(&OutboxEvent{State: OutboxStateDelivered, LeaseToken: "", LeaseUntilUnix: 0, DeliveredUnix: timeutil.TimeStamp(now.UTC().Unix())})
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// FailOutboxDelivery releases a lease for exponential retry or moves an event
// to the dead-letter state after the bounded attempt count.
func FailOutboxDelivery(ctx context.Context, event *OutboxEvent, errorCode string, now time.Time) (bool, error) {
	if event == nil || event.ID <= 0 || event.LeaseToken == "" {
		return false, errors.New("Rin Control outbox lease is incomplete")
	}
	state := OutboxStatePending
	nextAttempt := timeutil.TimeStamp(now.UTC().Add(deliveryBackoff(event.Attempts)).Unix())
	if event.Attempts >= MaxDeliveryAttempts {
		state = OutboxStateDead
		nextAttempt = 0
	}
	result, err := db.GetEngine(ctx).ID(event.ID).Where("state = ? AND lease_token = ?", OutboxStateDelivering, event.LeaseToken).
		Cols("state", "next_attempt_unix", "lease_token", "lease_until_unix", "last_error_code").
		Update(&OutboxEvent{State: state, NextAttemptUnix: nextAttempt, LeaseToken: "", LeaseUntilUnix: 0, LastErrorCode: normalizeErrorCode(errorCode)})
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func deliveryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func normalizeErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "delivery_failed"
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

type OutboxBacklog struct {
	Pending       int64
	Delivering    int64
	Dead          int64
	RetryAttempts int64
	OldestAge     time.Duration
}

type PublicationOutboxHealth struct {
	Pending         int64
	Delivering      int64
	Dead            int64
	OldestAge       time.Duration
	LastCapturedAt  *time.Time
	LastDeliveredAt *time.Time
}

func GetPublicationOutboxHealth(ctx context.Context, now time.Time) (PublicationOutboxHealth, error) {
	var result PublicationOutboxHealth
	rows, err := db.GetEngine(ctx).QueryInterface(`SELECT
COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS pending,
COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS delivering,
COALESCE(SUM(CASE WHEN state = ? THEN 1 ELSE 0 END), 0) AS dead,
COALESCE(MIN(CASE WHEN state IN (?, ?) THEN created_unix ELSE NULL END), 0) AS oldest_unix,
COALESCE(MAX(created_unix), 0) AS last_captured_unix,
COALESCE(MAX(CASE WHEN state = ? THEN delivered_unix ELSE NULL END), 0) AS last_delivered_unix
FROM rin_control_outbox WHERE event_type IN (?, ?)`,
		OutboxStatePending, OutboxStateDelivering, OutboxStateDead,
		OutboxStatePending, OutboxStateDelivering, OutboxStateDelivered,
		repositoryPushEventType, PublicationHeartbeatEventType)
	if err != nil {
		return PublicationOutboxHealth{}, err
	}
	if len(rows) != 1 {
		return result, nil
	}
	result.Pending = interfaceInt64(rows[0]["pending"])
	result.Delivering = interfaceInt64(rows[0]["delivering"])
	result.Dead = interfaceInt64(rows[0]["dead"])
	oldest := interfaceInt64(rows[0]["oldest_unix"])
	if oldest > 0 && now.UTC().Unix() > oldest {
		result.OldestAge = time.Duration(now.UTC().Unix()-oldest) * time.Second
	}
	if captured := interfaceInt64(rows[0]["last_captured_unix"]); captured > 0 {
		value := time.Unix(captured, 0).UTC()
		result.LastCapturedAt = &value
	}
	if delivered := interfaceInt64(rows[0]["last_delivered_unix"]); delivered > 0 {
		value := time.Unix(delivered, 0).UTC()
		result.LastDeliveredAt = &value
	}
	return result, nil
}

func GetOutboxBacklog(ctx context.Context, now time.Time) (OutboxBacklog, error) {
	var result OutboxBacklog
	for state, target := range map[string]*int64{
		OutboxStatePending: &result.Pending, OutboxStateDelivering: &result.Delivering, OutboxStateDead: &result.Dead,
	} {
		count, err := db.GetEngine(ctx).Where("state = ?", state).Count(new(OutboxEvent))
		if err != nil {
			return OutboxBacklog{}, err
		}
		*target = count
	}
	rows, err := db.GetEngine(ctx).QueryInterface("SELECT COALESCE(SUM(attempts), 0) AS retry_attempts, COALESCE(MIN(created_unix), 0) AS oldest_unix FROM rin_control_outbox WHERE state IN (?, ?)", OutboxStatePending, OutboxStateDelivering)
	if err != nil {
		return OutboxBacklog{}, err
	}
	if len(rows) == 1 {
		result.RetryAttempts = interfaceInt64(rows[0]["retry_attempts"])
		oldest := interfaceInt64(rows[0]["oldest_unix"])
		if oldest > 0 && now.UTC().Unix() > oldest {
			result.OldestAge = time.Duration(now.UTC().Unix()-oldest) * time.Second
		}
	}
	return result, nil
}

func interfaceInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case []byte:
		var parsed int64
		_, _ = fmt.Sscan(string(typed), &parsed)
		return parsed
	case string:
		var parsed int64
		_, _ = fmt.Sscan(typed, &parsed)
		return parsed
	default:
		return 0
	}
}

func newEventID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "gitea-social-" + hex.EncodeToString(value), nil
}

func ValidateOutboxEvent(event *OutboxEvent) error {
	if event == nil || strings.TrimSpace(event.EventID) == "" || len(event.EventID) > 240 || event.Payload == "" || event.AggregateID <= 0 {
		return errors.New("Rin Control outbox event is incomplete")
	}
	if event.State != OutboxStatePending && event.State != OutboxStateDelivering && event.State != OutboxStateDelivered && event.State != OutboxStateDead {
		return errors.New("Rin Control outbox state is invalid")
	}
	if event.EventType == repositoryPushEventType {
		return validateRepositoryPushOutboxEvent(event)
	}
	if event.EventType == PublicationHeartbeatEventType {
		return validatePublicationHeartbeatOutboxEvent(event)
	}
	switch event.EventType {
	case "repository.star.changed", "repository.watch.changed":
		if event.AggregateType != "repository" || event.AggregateID != event.RepositoryID || event.RepositoryID <= 0 || event.ActorUserID <= 0 || event.TargetUserID != 0 {
			return errors.New("Rin Control repository social outbox identity is invalid")
		}
	case "user.follow.changed":
		if event.AggregateType != "user" || event.AggregateID != event.TargetUserID || event.TargetUserID <= 0 || event.ActorUserID <= 0 || event.RepositoryID != 0 || event.ActorUserID == event.TargetUserID {
			return errors.New("Rin Control follow outbox identity is invalid")
		}
	default:
		return fmt.Errorf("unsupported Rin Control outbox event %q", event.EventType)
	}
	return nil
}
