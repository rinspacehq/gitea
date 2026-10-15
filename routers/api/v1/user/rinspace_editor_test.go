// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestRinspaceEditorCatalogDisabled(t *testing.T) {
	previous := setting.API.RinspaceEditorCatalogEnabled
	setting.API.RinspaceEditorCatalogEnabled = false
	t.Cleanup(func() { setting.API.RinspaceEditorCatalogEnabled = previous })
	ctx, response := contexttest.MockAPIContext(t, "GET /api/v1/user/rinspace-editor/projects")
	ListRinspaceEditorProjects(ctx)
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestRinspaceEditorCatalogInvalidRequest(t *testing.T) {
	previous := setting.API.RinspaceEditorCatalogEnabled
	setting.API.RinspaceEditorCatalogEnabled = true
	t.Cleanup(func() { setting.API.RinspaceEditorCatalogEnabled = previous })
	for _, query := range []string{
		"limit=0", "limit=51", "limit=invalid", "before_id=0", "before_id=-1", "before_id=025",
		"before_id=9223372036854775808", "q=" + url.QueryEscape("line\nbreak"),
		"q=" + url.QueryEscape(strings.Repeat("数", 67)), "q=%FF",
	} {
		t.Run(query, func(t *testing.T) {
			ctx, response := contexttest.MockAPIContext(t, "GET /api/v1/user/rinspace-editor/projects?"+query)
			ctx.Doer = &user_model.User{ID: 2, Name: "reader", IsActive: true}
			ListRinspaceEditorProjects(ctx)
			require.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
	for _, actor := range []*user_model.User{nil, {ID: 2}, {ID: 2, IsActive: true, ProhibitLogin: true}, {ID: 2, IsActive: true, Type: user_model.UserTypeOrganization}} {
		ctx, response := contexttest.MockAPIContext(t, "GET /api/v1/user/rinspace-editor/projects")
		ctx.Doer = actor
		ListRinspaceEditorProjects(ctx)
		require.Equal(t, http.StatusBadRequest, response.Code)
	}
}

func TestRinspaceEditorCatalogUnsupportedDatabase(t *testing.T) {
	previousEnabled, previousType := setting.API.RinspaceEditorCatalogEnabled, setting.Database.Type
	setting.API.RinspaceEditorCatalogEnabled, setting.Database.Type = true, "sqlite3"
	t.Cleanup(func() {
		setting.API.RinspaceEditorCatalogEnabled, setting.Database.Type = previousEnabled, previousType
	})
	ctx, response := contexttest.MockAPIContext(t, "GET /api/v1/user/rinspace-editor/projects?limit=24")
	ctx.Doer = &user_model.User{ID: 2, Name: "reader", IsActive: true}
	ListRinspaceEditorProjects(ctx)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
}
