// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/modules/rinspacecatalog"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	service_context "gitea.dev/services/context"
)

type editorCatalogRepository struct {
	ID                        int64           `json:"id"`
	Name                      string          `json:"name"`
	FullName                  string          `json:"full_name"`
	Description               string          `json:"description"`
	Private                   bool            `json:"private"`
	Archived                  bool            `json:"archived"`
	DefaultBranch             string          `json:"default_branch"`
	ObjectFormat              string          `json:"object_format_name"`
	UpdatedAt                 time.Time       `json:"updated_at"`
	RinspaceTitle             string          `json:"rinspace_title"`
	RinspaceContentType       string          `json:"rinspace_content_type"`
	RinspaceProjectionVersion int64           `json:"rinspace_projection_version"`
	RinspaceContentOwnerIDs   []int64         `json:"rinspace_content_owner_ids"`
	Owner                     editorActor     `json:"owner"`
	Permissions               *api.Permission `json:"permissions"`
}

type editorActor struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// ListRinspaceEditorProjects returns only catalog metadata. Unlike ToRepo it
// does not load Git objects, parent repositories or unrelated issue counters.
func ListRinspaceEditorProjects(ctx *service_context.APIContext) {
	if !setting.API.RinspaceEditorCatalogEnabled {
		ctx.APIError(http.StatusNotFound, "editor catalog is disabled")
		return
	}
	limit := 24
	if raw := ctx.FormString("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			ctx.APIError(http.StatusBadRequest, "invalid editor page size")
			return
		}
		limit = value
	}
	var before int64
	if raw := ctx.FormString("before_id"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 || strconv.FormatInt(value, 10) != raw {
			ctx.APIError(http.StatusBadRequest, "invalid editor cursor")
			return
		}
		before = value
	}
	opts := repo_model.RinspaceEditorProjectOptions{
		Actor: ctx.Doer, Query: strings.TrimSpace(ctx.FormString("q")), Limit: limit, BeforeID: before, PublicOnly: ctx.PublicOnly,
	}
	if err := opts.Validate(); err != nil {
		ctx.APIError(http.StatusBadRequest, "invalid editor query")
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	page, err := repo_model.FindRinspaceEditorProjects(requestCtx, opts)
	if err != nil {
		ctx.APIError(http.StatusServiceUnavailable, "indexed editor catalog is unavailable")
		return
	}
	projects := make([]editorCatalogRepository, 0, len(page.Repositories))
	for _, repository := range page.Repositories {
		permission, err := access_model.GetDoerRepoPermission(requestCtx, repository, ctx.Doer)
		if err != nil || permission.UnitAccessMode(unit.TypeCode) < perm.AccessModeRead {
			// A concurrent ACL change must not leak data or silently skip a page.
			ctx.APIError(http.StatusServiceUnavailable, "editor repository access changed; refresh the catalog")
			return
		}
		projects = append(projects, editorCatalogRepository{
			ID: repository.ID, Name: repository.Name, FullName: repository.FullName(), Description: repository.Description,
			Private: repository.IsPrivate, Archived: repository.IsArchived, DefaultBranch: repository.DefaultBranch,
			ObjectFormat: repository.ObjectFormatName, UpdatedAt: repository.UpdatedUnix.AsTime(),
			RinspaceTitle: repository.RinspaceTitle, RinspaceContentType: repository.RinspaceContentType,
			RinspaceProjectionVersion: repository.RinspaceProjectionVersion, RinspaceContentOwnerIDs: repository.RinspaceContentOwnerIDs,
			Owner:       editorActor{ID: repository.OwnerID, Login: repository.OwnerName},
			Permissions: &api.Permission{Pull: true, Push: permission.UnitAccessMode(unit.TypeCode) >= perm.AccessModeWrite, Admin: permission.IsAdmin()},
		})
	}
	ctx.Resp.Header().Set("Cache-Control", "private, no-store")
	ctx.JSON(http.StatusOK, struct {
		SchemaVersion string                    `json:"schemaVersion"`
		Account       editorActor               `json:"account"`
		Projects      []editorCatalogRepository `json:"projects"`
		Limit         int                       `json:"limit"`
		Query         string                    `json:"query"`
		HasMore       bool                      `json:"hasMore"`
		NextCursor    string                    `json:"nextCursor,omitempty"`
	}{rinspacecatalog.SchemaVersion, editorActor{ID: ctx.Doer.ID, Login: ctx.Doer.Name}, projects, limit, opts.Query, page.HasMore, page.NextCursor})
}
