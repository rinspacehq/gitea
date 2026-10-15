// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package rinspacecatalog

import (
	"strings"
	"testing"
)

func TestPostgresStatements(t *testing.T) {
	statements := Statements(`custom"schema`)
	if len(statements) != 8 {
		t.Fatalf("expected two functions and six indexes, got %d", len(statements))
	}
	for _, statement := range statements {
		if !strings.Contains(statement, `"custom""schema".`) || strings.Contains(statement, "CREATE EXTENSION") {
			t.Fatalf("unqualified or extension-dependent statement: %s", statement)
		}
	}
	if strings.Contains(strings.Join(statements, " "), "DROP ") {
		t.Fatal("catalog migration must be additive")
	}
	condition := Contains("public", `presentation.title`)
	if strings.Count(condition, "?") != 3 || !strings.Contains(condition, "strpos(") || strings.Contains(condition, "LIKE") {
		t.Fatalf("literal contains must retain an exact parameterized check: %s", condition)
	}
}
