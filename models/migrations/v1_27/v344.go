// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

func AddRinControlSocialOutbox(x db.EngineMigration) error {
	type RinControlOutbox struct {
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
	return x.Sync(new(RinControlOutbox))
}
