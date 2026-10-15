// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"strings"
	"testing"

	user_model "gitea.dev/models/user"
)

func TestRinspaceEditorProjectSQLIsBounded(t *testing.T) {
	actor := &user_model.User{ID: 2, Name: "reader", IsActive: true}
	for _, query := range []string{"", "数学", "tags/25", "%' OR true --"} {
		opts := RinspaceEditorProjectOptions{Actor: actor, Query: query, BeforeID: 500, Limit: 24}
		statement, args, err := rinspaceEditorProjectSQL("public", opts)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(statement, "OFFSET") || strings.Contains(statement, "COUNT(") || !strings.HasSuffix(statement, "LIMIT 25") {
			t.Fatalf("unbounded pagination: %s", statement)
		}
		if strings.Contains(statement, query) && strings.Contains(query, "'") {
			t.Fatal("query text was interpolated into SQL")
		}
		if len(args) == 0 || !strings.Contains(statement, "repository.owner_name") || !strings.Contains(statement, "editor_tag.content_type='tag-wiki'") {
			t.Fatal("missing default namespace/type guard")
		}
		if query == "数学" && (!strings.Contains(statement, "editor_title.repo_id<?") || !strings.Contains(statement, "strpos(")) {
			t.Fatalf("search must constrain both sides of the cursor and verify literal substring order: %s", statement)
		}
	}
}

func TestRinspaceEditorProjectOptions(t *testing.T) {
	actor := &user_model.User{ID: 2, IsActive: true}
	for _, opts := range []RinspaceEditorProjectOptions{
		{Limit: 24}, {Actor: actor, Limit: 0}, {Actor: actor, Limit: 51},
		{Actor: actor, Limit: 24, BeforeID: -1}, {Actor: actor, Limit: 24, Query: "bad\nquery"},
		{Actor: actor, Limit: 24, Query: strings.Repeat("a", 201)}, {Actor: actor, Limit: 24, Query: "\xff"},
	} {
		if opts.Validate() == nil {
			t.Fatalf("invalid options accepted: %+v", opts)
		}
	}
}
