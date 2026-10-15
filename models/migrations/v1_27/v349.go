// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"fmt"

	"gitea.dev/models/db"
	"gitea.dev/modules/rinspacecatalog"

	"xorm.io/xorm/schemas"
)

func AddRinspaceEditorCatalogIndexes(x db.EngineMigration) error {
	if x.Dialect().URI().DBType != schemas.POSTGRES {
		return nil
	}
	var current struct{ Name string }
	if _, err := x.SQL("SELECT current_schema() AS name").Get(&current); err != nil {
		return err
	}
	if current.Name == "" {
		return fmt.Errorf("Rinspace catalog requires an explicit PostgreSQL schema")
	}
	for _, statement := range rinspacecatalog.Statements(current.Name) {
		if _, err := x.Exec(statement); err != nil {
			return fmt.Errorf("Rinspace catalog index migration: %w", err)
		}
	}
	return nil
}
