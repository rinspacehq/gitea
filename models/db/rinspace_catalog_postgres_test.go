// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm/core"
	"xorm.io/xorm/dialects"
	"xorm.io/xorm/schemas"
)

type catalogIndexDialectStub struct {
	dialects.Dialect
	indexes map[string]*schemas.Index
	err     error
}

func (d *catalogIndexDialectStub) GetIndexes(core.Queryer, context.Context, string) (map[string]*schemas.Index, error) {
	return d.indexes, d.err
}

func TestRinspaceCatalogPostgresIndexMetadata(t *testing.T) {
	for _, item := range []struct {
		table, name, column string
		ignore              bool
	}{
		{"repository", "rinspace_editor_name_tokens", "rinspace_editor_tokens_v1", true},
		{"rinspace_repository_presentation", "rinspace_editor_title_tokens", `"custom"."rinspace_editor_tokens_v1"`, true},
		{"repository", "unrelated_expression", "rinspace_editor_tokens_v1", false},
		{"other_table", "rinspace_editor_name_tokens", "rinspace_editor_tokens_v1", false},
		{"repository", "rinspace_editor_title_tokens", "rinspace_editor_tokens_v1", false},
		{"repository", "rinspace_editor_name_tokens", "lower_name", false},
		{"repository", "rinspace_editor_name_tokens", "another_tokens_v1", false},
	} {
		t.Run(item.table+"/"+item.name+"/"+item.column, func(t *testing.T) {
			original := map[string]*schemas.Index{
				item.name:  {Name: item.name, Cols: []string{item.column}},
				"ordinary": {Name: "ordinary", Cols: []string{"id"}},
			}
			dialect := &rinspaceCatalogPostgresDialect{Dialect: &catalogIndexDialectStub{indexes: original}}
			indexes, err := dialect.GetIndexes(nil, t.Context(), item.table)
			require.NoError(t, err)
			require.Contains(t, indexes, "ordinary")
			require.Len(t, original, 2, "do not mutate upstream metadata")
			_, exists := indexes[item.name]
			require.Equal(t, !item.ignore, exists)
		})
	}
	t.Run("upstream-error", func(t *testing.T) {
		expected := errors.New("metadata unavailable")
		dialect := &rinspaceCatalogPostgresDialect{Dialect: &catalogIndexDialectStub{err: expected}}
		_, err := dialect.GetIndexes(nil, t.Context(), "repository")
		require.ErrorIs(t, err, expected)
	})
	t.Run("migration-owned-btree-layouts", func(t *testing.T) {
		for _, item := range []struct {
			table, name string
			columns     []string
			owned       bool
		}{
			{"repository", "rinspace_editor_ordinary_id", []string{"id"}, true},
			{"repository", "rinspace_editor_public_tags_id", []string{"id"}, true},
			{"rinspace_repository_presentation", "rinspace_editor_type_repo", []string{"content_type", "repo_id"}, true},
			{"access", "rinspace_editor_access_user_repo", []string{"user_id", "repo_id"}, true},
			{"repository", "rinspace_editor_ordinary_id", []string{"owner_id"}, false},
			{"access", "rinspace_editor_access_user_repo", []string{"repo_id", "user_id"}, false},
			{"access", "unrelated_user_repo", []string{"user_id", "repo_id"}, false},
			{"repository", "rinspace_editor_name_tokens", []string{"rinspace_editor_tokens_v1", "id"}, false},
		} {
			dialect := &rinspaceCatalogPostgresDialect{Dialect: &catalogIndexDialectStub{indexes: map[string]*schemas.Index{
				item.name: {Name: item.name, Cols: item.columns},
			}}}
			indexes, err := dialect.GetIndexes(nil, t.Context(), item.table)
			require.NoError(t, err)
			_, exists := indexes[item.name]
			require.Equal(t, !item.owned, exists, "%+v", item)
		}
	})
}
