// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rincontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

const (
	DeliveryChannelPublication = "publication"
	DeliveryStateEnabled       = "enabled"
	DeliveryStatePaused        = "paused"
	MaxDeliveryPause           = 15 * time.Minute
)

var (
	ErrDeliveryControlConflict = errors.New("Rin Control delivery maintenance change conflicts with persisted state")
	changeIDPattern            = regexp.MustCompile(`^RCP-[A-Z0-9][A-Z0-9._-]{5,119}$`)
	allowedDeliveryReasons     = map[string]struct{}{
		"credential_rotation":  {},
		"incident_containment": {},
		"protocol_isolation":   {},
	}
)

type DeliveryControl struct {
	Channel         string             `xorm:"pk VARCHAR(32)"`
	State           string             `xorm:"VARCHAR(16) NOT NULL DEFAULT 'enabled'"`
	ChangeID        string             `xorm:"VARCHAR(128) NOT NULL DEFAULT ''"`
	ReasonCode      string             `xorm:"VARCHAR(32) NOT NULL DEFAULT ''"`
	PausedUntilUnix timeutil.TimeStamp `xorm:"INDEX NOT NULL DEFAULT 0"`
	Version         int64              `xorm:"NOT NULL DEFAULT 1"`
	LastAction      string             `xorm:"VARCHAR(16) NOT NULL DEFAULT ''"`
	LastRequestHash string             `xorm:"CHAR(64) NOT NULL DEFAULT ''"`
	CreatedUnix     timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix     timeutil.TimeStamp `xorm:"updated"`
}

func (*DeliveryControl) TableName() string { return "rin_control_delivery_controls" }

func init() {
	db.RegisterModel(new(DeliveryControl))
}

type PauseDeliveryRequest struct {
	ChangeID   string
	ReasonCode string
	Until      time.Time
}

func GetPublicationDeliveryControl(ctx context.Context, now time.Time) (*DeliveryControl, error) {
	var result *DeliveryControl
	err := db.WithTx(ctx, func(ctx context.Context) error {
		control, err := effectivePublicationDeliveryControl(ctx, now.UTC())
		if err != nil {
			return err
		}
		result = control
		return nil
	})
	return result, err
}

func PausePublicationDelivery(ctx context.Context, request PauseDeliveryRequest, now time.Time) (*DeliveryControl, error) {
	now = now.UTC()
	requestHash, err := validatePauseDeliveryRequest(request, now)
	if err != nil {
		return nil, err
	}
	var result *DeliveryControl
	err = db.WithTx(ctx, func(ctx context.Context) error {
		control, err := effectivePublicationDeliveryControl(ctx, now)
		if err != nil {
			return err
		}
		if control.State == DeliveryStatePaused {
			if control.ChangeID == request.ChangeID && control.LastAction == "pause" && strings.TrimSpace(control.LastRequestHash) == requestHash {
				result = control
				return nil
			}
			return ErrDeliveryControlConflict
		}
		if control.ChangeID == request.ChangeID {
			return ErrDeliveryControlConflict
		}
		updated := &DeliveryControl{
			State: DeliveryStatePaused, ChangeID: request.ChangeID, ReasonCode: request.ReasonCode,
			PausedUntilUnix: timeutil.TimeStamp(request.Until.UTC().Unix()), Version: control.Version + 1,
			LastAction: "pause", LastRequestHash: requestHash,
		}
		count, err := db.GetEngine(ctx).ID(DeliveryChannelPublication).
			Where("state = ? AND version = ?", DeliveryStateEnabled, control.Version).
			Cols("state", "change_id", "reason_code", "paused_until_unix", "version", "last_action", "last_request_hash").
			Update(updated)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrDeliveryControlConflict
		}
		updated.Channel = DeliveryChannelPublication
		result = updated
		return nil
	})
	return result, err
}

func ResumePublicationDelivery(ctx context.Context, changeID string, now time.Time) (*DeliveryControl, error) {
	now = now.UTC()
	if err := validateChangeID(changeID); err != nil {
		return nil, err
	}
	requestHash := deliveryRequestHash("resume", changeID)
	var result *DeliveryControl
	err := db.WithTx(ctx, func(ctx context.Context) error {
		control, err := effectivePublicationDeliveryControl(ctx, now)
		if err != nil {
			return err
		}
		if control.State == DeliveryStateEnabled {
			if control.ChangeID == changeID && (control.LastAction == "resume" || control.LastAction == "auto_resume") {
				result = control
				return nil
			}
			return ErrDeliveryControlConflict
		}
		if control.ChangeID != changeID {
			return ErrDeliveryControlConflict
		}
		updated := &DeliveryControl{
			State: DeliveryStateEnabled, ChangeID: control.ChangeID, ReasonCode: control.ReasonCode,
			PausedUntilUnix: 0, Version: control.Version + 1, LastAction: "resume", LastRequestHash: requestHash,
		}
		count, err := db.GetEngine(ctx).ID(DeliveryChannelPublication).
			Where("state = ? AND change_id = ? AND version = ?", DeliveryStatePaused, changeID, control.Version).
			Cols("state", "paused_until_unix", "version", "last_action", "last_request_hash").
			Update(updated)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrDeliveryControlConflict
		}
		updated.Channel = DeliveryChannelPublication
		result = updated
		return nil
	})
	return result, err
}

func effectivePublicationDeliveryControl(ctx context.Context, now time.Time) (*DeliveryControl, error) {
	control, err := ensurePublicationDeliveryControl(ctx)
	if err != nil {
		return nil, err
	}
	if control.State != DeliveryStatePaused || int64(control.PausedUntilUnix) > now.Unix() {
		return control, nil
	}
	requestHash := deliveryRequestHash("auto_resume", control.ChangeID, strconv.FormatInt(now.Unix(), 10))
	updated := &DeliveryControl{
		State: DeliveryStateEnabled, ChangeID: control.ChangeID, ReasonCode: control.ReasonCode,
		PausedUntilUnix: 0, Version: control.Version + 1, LastAction: "auto_resume", LastRequestHash: requestHash,
	}
	count, err := db.GetEngine(ctx).ID(DeliveryChannelPublication).
		Where("state = ? AND version = ? AND paused_until_unix <= ?", DeliveryStatePaused, control.Version, timeutil.TimeStamp(now.Unix())).
		Cols("state", "paused_until_unix", "version", "last_action", "last_request_hash").
		Update(updated)
	if err != nil {
		return nil, err
	}
	if count == 1 {
		updated.Channel = DeliveryChannelPublication
		return updated, nil
	}
	reloaded := new(DeliveryControl)
	has, err := db.GetEngine(ctx).ID(DeliveryChannelPublication).Get(reloaded)
	if err != nil {
		return nil, fmt.Errorf("reload publication delivery control after lease race: %w", err)
	}
	if !has {
		return nil, errors.New("publication delivery control disappeared after lease race")
	}
	return reloaded, nil
}

func ensurePublicationDeliveryControl(ctx context.Context) (*DeliveryControl, error) {
	control := new(DeliveryControl)
	has, err := db.GetEngine(ctx).ID(DeliveryChannelPublication).Get(control)
	if err != nil || has {
		return control, err
	}
	control = &DeliveryControl{Channel: DeliveryChannelPublication, State: DeliveryStateEnabled, Version: 1}
	if err := db.Insert(ctx, control); err != nil {
		reloaded := new(DeliveryControl)
		has, getErr := db.GetEngine(ctx).ID(DeliveryChannelPublication).Get(reloaded)
		if getErr != nil {
			return nil, getErr
		}
		if !has {
			return nil, err
		}
		return reloaded, nil
	}
	return control, nil
}

func validatePauseDeliveryRequest(request PauseDeliveryRequest, now time.Time) (string, error) {
	if err := validateChangeID(request.ChangeID); err != nil {
		return "", err
	}
	if _, ok := allowedDeliveryReasons[request.ReasonCode]; !ok {
		return "", errors.New("Rin Control delivery maintenance reason is invalid")
	}
	until := request.Until.UTC()
	if until.Unix() <= now.Unix() || until.Sub(now) > MaxDeliveryPause {
		return "", errors.New("Rin Control delivery maintenance deadline must be in the future and at most 15 minutes")
	}
	return deliveryRequestHash("pause", request.ChangeID, request.ReasonCode, strconv.FormatInt(until.Unix(), 10)), nil
}

func validateChangeID(changeID string) error {
	if !changeIDPattern.MatchString(changeID) {
		return errors.New("Rin Control delivery maintenance change ID is invalid")
	}
	return nil
}

func deliveryRequestHash(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(sum[:])
}
