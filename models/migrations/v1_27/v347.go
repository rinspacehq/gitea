// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

type rinControlDeliveryControl struct {
	Channel         string             `xorm:"pk VARCHAR(32)"`
	State           string             `xorm:"VARCHAR(16) NOT NULL DEFAULT 'enabled'"`
	ChangeID        string             `xorm:"VARCHAR(128) NOT NULL DEFAULT ''"`
	ReasonCode      string             `xorm:"VARCHAR(32) NOT NULL DEFAULT ''"`
	PausedUntilUnix timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
	Version         int64              `xorm:"NOT NULL DEFAULT 1"`
	LastAction      string             `xorm:"VARCHAR(16) NOT NULL DEFAULT ''"`
	LastRequestHash string             `xorm:"CHAR(64) NOT NULL DEFAULT ''"`
	CreatedUnix     timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix     timeutil.TimeStamp `xorm:"updated"`
}

func (*rinControlDeliveryControl) TableName() string { return "rin_control_delivery_controls" }

// AddRinControlDeliveryControls creates the bounded dispatcher-maintenance
// lease without changing or deleting any existing outbox rows.
func AddRinControlDeliveryControls(x db.EngineMigration) error {
	if err := x.Sync(new(rinControlDeliveryControl)); err != nil {
		return err
	}
	control := &rinControlDeliveryControl{Channel: "publication", State: "enabled", Version: 1}
	has, err := x.ID(control.Channel).Get(new(rinControlDeliveryControl))
	if err != nil || has {
		return err
	}
	_, err = x.Insert(control)
	return err
}
