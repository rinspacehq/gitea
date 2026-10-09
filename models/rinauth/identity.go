// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rinauth

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
	"gitea.dev/modules/timeutil"
)

var (
	ErrBindingNotFound           = errors.New("Rinspace identity binding not found")
	ErrRevocationReceiptConflict = errors.New("Rinspace revocation receipt conflict")
	revocationEventIDPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type UserIdentity struct {
	UserID      int64              `xorm:"pk"`
	RinspaceUID string             `xorm:"VARCHAR(200) UNIQUE NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func (*UserIdentity) TableName() string { return "rinspace_user_identity" }

type CredentialBinding struct {
	CredentialRef string             `xorm:"pk VARCHAR(255)"`
	UserID        int64              `xorm:"INDEX NOT NULL"`
	RinspaceUID   string             `xorm:"INDEX VARCHAR(200) NOT NULL"`
	IssuedEpoch   int64              `xorm:"NOT NULL"`
	Kind          string             `xorm:"VARCHAR(24) NOT NULL DEFAULT 'unknown'"`
	State         string             `xorm:"INDEX VARCHAR(16) NOT NULL DEFAULT 'active'"`
	LastUsedUnix  timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
	RevokedUnix   timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
	CreatedUnix   timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix   timeutil.TimeStamp `xorm:"updated"`
}

func (*CredentialBinding) TableName() string { return "rinspace_credential_binding" }

type RevocationReceipt struct {
	EventID       string             `xorm:"pk VARCHAR(64)"`
	AggregateType string             `xorm:"INDEX VARCHAR(16) NOT NULL"`
	AggregateID   string             `xorm:"INDEX VARCHAR(200) NOT NULL"`
	EventType     string             `xorm:"VARCHAR(64) NOT NULL"`
	Version       int64              `xorm:"NOT NULL"`
	Payload       string             `xorm:"TEXT NOT NULL"`
	AppliedUnix   timeutil.TimeStamp `xorm:"NOT NULL"`
	CreatedUnix   timeutil.TimeStamp `xorm:"created"`
}

func (*RevocationReceipt) TableName() string { return "rinspace_revocation_receipt" }

func init() {
	db.RegisterModel(new(UserIdentity))
	db.RegisterModel(new(CredentialBinding))
	db.RegisterModel(new(RevocationReceipt))
}

func UpsertUserIdentity(ctx context.Context, userID int64, uid string) error {
	uid = strings.TrimSpace(uid)
	if userID <= 0 || uid == "" {
		return errors.New("invalid Rinspace user binding")
	}
	return db.WithTx(ctx, func(ctx context.Context) error {
		current := new(UserIdentity)
		has, err := db.GetEngine(ctx).ID(userID).Get(current)
		if err != nil {
			return err
		}
		if has {
			if current.RinspaceUID != uid {
				return errors.New("Rinspace user binding conflict")
			}
			return nil
		}
		_, err = db.GetEngine(ctx).Insert(&UserIdentity{UserID: userID, RinspaceUID: uid})
		return err
	})
}

func GetUserIdentity(ctx context.Context, userID int64) (*UserIdentity, error) {
	result := new(UserIdentity)
	has, err := db.GetEngine(ctx).ID(userID).Get(result)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrBindingNotFound
	}
	return result, nil
}

func GetUserIdentityByUID(ctx context.Context, uid string) (*UserIdentity, error) {
	result := new(UserIdentity)
	has, err := db.GetEngine(ctx).Where("rinspace_uid = ?", strings.TrimSpace(uid)).Get(result)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrBindingNotFound
	}
	return result, nil
}

func GetCredentialBinding(ctx context.Context, ref string) (*CredentialBinding, error) {
	result := new(CredentialBinding)
	has, err := db.GetEngine(ctx).ID(strings.TrimSpace(ref)).Get(result)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrBindingNotFound
	}
	return result, nil
}

func UpsertCredentialBinding(ctx context.Context, binding *CredentialBinding) error {
	if binding == nil || strings.TrimSpace(binding.CredentialRef) == "" || binding.UserID <= 0 || strings.TrimSpace(binding.RinspaceUID) == "" || binding.IssuedEpoch < 1 {
		return errors.New("invalid Rinspace credential binding")
	}
	current := new(CredentialBinding)
	has, err := db.GetEngine(ctx).ID(binding.CredentialRef).Get(current)
	if err != nil {
		return err
	}
	if has {
		if current.UserID != binding.UserID || current.RinspaceUID != binding.RinspaceUID || current.IssuedEpoch != binding.IssuedEpoch || current.State == "revoked" {
			return errors.New("Rinspace credential binding conflict")
		}
		if current.Kind == "unknown" && (binding.Kind == "ssh" || binding.Kind == "pat" || binding.Kind == "oauth") {
			_, err = db.GetEngine(ctx).ID(binding.CredentialRef).Where("kind = ?", "unknown").Cols("kind").Update(&CredentialBinding{Kind: binding.Kind})
			return err
		}
		return nil
	}
	_, err = db.GetEngine(ctx).Insert(binding)
	return err
}

func ListCredentialBindings(ctx context.Context, uid string) ([]*CredentialBinding, error) {
	result := make([]*CredentialBinding, 0)
	err := db.GetEngine(ctx).Where("rinspace_uid = ?", strings.TrimSpace(uid)).Asc("credential_ref").Limit(100).Find(&result)
	return result, err
}

func TouchCredentialBinding(ctx context.Context, ref string) error {
	now := timeutil.TimeStampNow()
	_, err := db.GetEngine(ctx).ID(strings.TrimSpace(ref)).Where("last_used_unix < ?", now-300).
		Cols("last_used_unix").Update(&CredentialBinding{LastUsedUnix: now})
	return err
}

func RecordRevocationReceipt(ctx context.Context, receipt *RevocationReceipt) error {
	if receipt == nil {
		return errors.New("invalid Rinspace revocation receipt")
	}
	receipt.EventID = strings.ToLower(strings.TrimSpace(receipt.EventID))
	receipt.AggregateType = strings.TrimSpace(receipt.AggregateType)
	receipt.AggregateID = strings.TrimSpace(receipt.AggregateID)
	receipt.EventType = strings.TrimSpace(receipt.EventType)
	var compactPayload bytes.Buffer
	if !revocationEventIDPattern.MatchString(receipt.EventID) || len(receipt.AggregateID) == 0 || len(receipt.AggregateID) > 200 || receipt.Version < 1 ||
		!validRevocationEventType(receipt.AggregateType, receipt.EventType) || json.Compact(&compactPayload, []byte(receipt.Payload)) != nil {
		return errors.New("invalid Rinspace revocation receipt")
	}
	receipt.Payload = compactPayload.String()
	if receipt.AppliedUnix <= 0 {
		receipt.AppliedUnix = timeutil.TimeStampNow()
	}
	_, err := db.GetEngine(ctx).Insert(receipt)
	if err == nil {
		return nil
	}
	current := new(RevocationReceipt)
	has, getErr := db.GetEngine(ctx).ID(receipt.EventID).Get(current)
	if getErr != nil {
		return getErr
	}
	if has && current.AggregateType == receipt.AggregateType && current.AggregateID == receipt.AggregateID &&
		current.EventType == receipt.EventType && current.Version == receipt.Version && current.Payload == receipt.Payload {
		return nil
	}
	return ErrRevocationReceiptConflict
}

func validRevocationEventType(aggregateType, eventType string) bool {
	return (aggregateType == "session" && eventType == "session.revoked") ||
		(aggregateType == "account" && eventType == "account.authorization_changed")
}

func RevokeCredentialBinding(ctx context.Context, uid, ref string) (bool, error) {
	result, err := db.GetEngine(ctx).Where("credential_ref = ? AND rinspace_uid = ? AND state = ?", strings.TrimSpace(ref), strings.TrimSpace(uid), "active").
		Cols("state", "revoked_unix").Update(&CredentialBinding{State: "revoked", RevokedUnix: timeutil.TimeStampNow()})
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func RevokeAllCredentialBindings(ctx context.Context, uid string) (int64, error) {
	result, err := db.GetEngine(ctx).Where("rinspace_uid = ? AND state = ?", strings.TrimSpace(uid), "active").
		Cols("state", "revoked_unix").Update(&CredentialBinding{State: "revoked", RevokedUnix: timeutil.TimeStampNow()})
	if err != nil {
		return 0, err
	}
	return result, nil
}
