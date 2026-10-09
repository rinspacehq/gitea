// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

func AddRinspaceRepositoryPresentation(x db.EngineMigration) error {
	type RinspaceRepositoryPresentation struct {
		RepoID      int64              `xorm:"pk"`
		Title       string             `xorm:"VARCHAR(512) NOT NULL DEFAULT ''"`
		ContentType string             `xorm:"VARCHAR(16) INDEX NOT NULL"`
		Version     int64              `xorm:"NOT NULL DEFAULT 1"`
		UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
	}
	type RinspaceRepositoryOwner struct {
		RepoID int64 `xorm:"pk"`
		UserID int64 `xorm:"pk INDEX"`
	}
	return x.Sync(new(RinspaceRepositoryPresentation), new(RinspaceRepositoryOwner))
}
