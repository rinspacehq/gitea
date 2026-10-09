// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

type rinspaceUserIdentity struct {
	UserID      int64              `xorm:"pk"`
	RinspaceUID string             `xorm:"VARCHAR(200) UNIQUE NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func (*rinspaceUserIdentity) TableName() string { return "rinspace_user_identity" }

type rinspaceCredentialBinding struct {
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

func (*rinspaceCredentialBinding) TableName() string { return "rinspace_credential_binding" }

type rinspaceRevocationReceipt struct {
	EventID       string             `xorm:"pk VARCHAR(64)"`
	AggregateType string             `xorm:"INDEX VARCHAR(16) NOT NULL"`
	AggregateID   string             `xorm:"INDEX VARCHAR(200) NOT NULL"`
	EventType     string             `xorm:"VARCHAR(64) NOT NULL"`
	Version       int64              `xorm:"NOT NULL"`
	Payload       string             `xorm:"TEXT NOT NULL"`
	AppliedUnix   timeutil.TimeStamp `xorm:"NOT NULL"`
	CreatedUnix   timeutil.TimeStamp `xorm:"created"`
}

func (*rinspaceRevocationReceipt) TableName() string { return "rinspace_revocation_receipt" }

func AddRinspaceIdentityBindings(x db.EngineMigration) error {
	return x.Sync(new(rinspaceUserIdentity), new(rinspaceCredentialBinding), new(rinspaceRevocationReceipt))
}
