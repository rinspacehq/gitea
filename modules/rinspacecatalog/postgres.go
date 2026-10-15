// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rinspacecatalog

import (
	"fmt"
	"strings"
)

const SchemaVersion = "rinspace-gitea-editor-catalog/v1"

var IndexNames = []string{
	"rinspace_editor_ordinary_id",
	"rinspace_editor_public_tags_id",
	"rinspace_editor_name_tokens",
	"rinspace_editor_title_tokens",
	"rinspace_editor_type_repo",
	"rinspace_editor_access_user_repo",
}

func Identifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func Function(schema, name string) string {
	return Identifier(schema) + "." + Identifier("rinspace_editor_"+name+"_v1")
}

// Statements are additive and retryable after a failed migration. Published
// immutable function versions must never change without rebuilding their indexes.
func Statements(schema string) []string {
	grams := Function(schema, "grams")
	tokens := Function(schema, "tokens")
	repository := Identifier(schema) + `."repository"`
	presentation := Identifier(schema) + `."rinspace_repository_presentation"`
	access := Identifier(schema) + `."access"`
	return []string{
		fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s(value text, width integer) RETURNS text[]
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE COST 1 SET search_path = pg_catalog AS $body$
SELECT ARRAY(SELECT DISTINCT substr(lower(value), pos, width)
FROM generate_series(1, char_length(lower(value))-width+1) pos)
$body$`, grams),
		fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s(value text) RETURNS text[]
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE COST 100 SET search_path = pg_catalog AS $body$
SELECT %s(value,1) || %s(value,2) || %s(value,3)
$body$`, tokens, grams, grams, grams),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (id DESC) WHERE owner_name <> 'tags'`, Identifier(IndexNames[0]), repository),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (id DESC) WHERE owner_name = 'tags' AND NOT is_private AND NOT is_archived`, Identifier(IndexNames[1]), repository),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING gin (%s(lower_name))`, Identifier(IndexNames[2]), repository, tokens),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s USING gin (%s(title))`, Identifier(IndexNames[3]), presentation, tokens),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (content_type, repo_id)`, Identifier(IndexNames[4]), presentation),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (user_id, repo_id) INCLUDE (mode)`, Identifier(IndexNames[5]), access),
	}
}

// Contains uses the index for candidate generation and verifies literal order;
// repeated or reordered grams must never produce a false search result.
func Contains(schema, column string) string {
	return fmt.Sprintf(`%s(%s) @> %s(?, LEAST(3, char_length(?))) AND strpos(lower(%s), lower(?)) > 0`,
		Function(schema, "tokens"), column, Function(schema, "grams"), column)
}
