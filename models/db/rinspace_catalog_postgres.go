// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"maps"
	"strings"

	"xorm.io/xorm"
	"xorm.io/xorm/core"
	"xorm.io/xorm/dialects"
	"xorm.io/xorm/schemas"
)

type rinspaceCatalogPostgresDialect struct {
	dialects.Dialect
}

//revive:disable-next-line:context-as-argument // XORM's Dialect interface fixes this parameter order.
func (d *rinspaceCatalogPostgresDialect) GetIndexes(queryer core.Queryer, ctx context.Context, table string) (map[string]*schemas.Index, error) {
	indexes, err := d.Dialect.GetIndexes(queryer, ctx, table)
	if err != nil {
		return nil, err
	}
	indexes = maps.Clone(indexes)
	for name, index := range indexes {
		if rinspaceCatalogIndexOwned(table, name, index.Cols) {
			// These exact layouts are migration-owned. XORM otherwise rejects
			// expressions or drops unmodeled indexes; readiness reads pg_catalog.
			delete(indexes, name)
		}
	}
	return indexes, nil
}

func rinspaceCatalogIndexOwned(table, name string, columns []string) bool {
	if len(columns) == 1 {
		column := strings.ReplaceAll(columns[0], `"`, "")
		switch {
		case table == "repository" && (name == "rinspace_editor_ordinary_id" || name == "rinspace_editor_public_tags_id"):
			return column == "id"
		case table == "repository" && name == "rinspace_editor_name_tokens",
			table == "rinspace_repository_presentation" && name == "rinspace_editor_title_tokens":
			return column == "rinspace_editor_tokens_v1" || strings.HasSuffix(column, ".rinspace_editor_tokens_v1")
		}
	}
	if len(columns) == 2 {
		switch {
		case table == "rinspace_repository_presentation" && name == "rinspace_editor_type_repo":
			return columns[0] == "content_type" && columns[1] == "repo_id"
		case table == "access" && name == "rinspace_editor_access_user_repo":
			return columns[0] == "user_id" && columns[1] == "repo_id"
		}
	}
	return false
}

func newRinspacePostgresEngine(driver, connection string) (*xorm.Engine, error) {
	dialect, err := dialects.OpenDialect(driver, connection)
	if err != nil {
		return nil, err
	}
	database, err := core.Open(driver, connection)
	if err != nil {
		return nil, err
	}
	engine, err := xorm.NewEngineWithDialectAndDB(driver, connection, &rinspaceCatalogPostgresDialect{Dialect: dialect}, database)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	return engine, nil
}
