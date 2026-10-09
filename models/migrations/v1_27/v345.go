// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/models/migrations/base"

	"xorm.io/xorm/schemas"
)

// ExpandRinControlOutboxForPublicationEvents widens the deterministic push
// identity while retaining all existing social-event rows.
func ExpandRinControlOutboxForPublicationEvents(x db.EngineMigration) error {
	if x.Dialect().URI().DBType == schemas.SQLITE {
		return nil
	}
	return base.ModifyColumn(x, "rin_control_outbox", &schemas.Column{
		Name: "event_id",
		SQLType: schemas.SQLType{
			Name: "VARCHAR",
		},
		Length:         240,
		Nullable:       false,
		DefaultIsEmpty: true,
	})
}
