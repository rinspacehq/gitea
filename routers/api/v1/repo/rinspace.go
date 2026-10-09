// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"net/http"
	"strconv"

	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	repo_service "gitea.dev/services/repository"
)

func GetRinspaceSocial(ctx *context.APIContext) {
	actorID, err := strconv.ParseInt(ctx.FormString("actor_user_id"), 10, 64)
	if err != nil || actorID <= 0 {
		ctx.APIError(http.StatusBadRequest, "actor_user_id is invalid")
		return
	}
	var active bool
	switch ctx.FormString("relation") {
	case "star":
		active = repo_model.IsStaring(ctx, actorID, ctx.Repo.Repository.ID)
	case "watch":
		active = repo_model.IsWatching(ctx, actorID, ctx.Repo.Repository.ID)
	default:
		ctx.APIError(http.StatusBadRequest, "unsupported social relation")
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"repository_id": ctx.Repo.Repository.ID, "actor_user_id": actorID, "relation": ctx.FormString("relation"), "active": active})
}

func GetRinspaceAudit(ctx *context.APIContext) {
	collaborators, _, err := repo_model.GetCollaborators(ctx, &repo_model.FindCollaborationOptions{RepoID: ctx.Repo.Repository.ID})
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	permissions := make(map[string]string, len(collaborators))
	for _, collaborator := range collaborators {
		permissions[strconv.FormatInt(collaborator.ID, 10)] = collaborator.Collaboration.Mode.ToString()
	}
	ctx.JSON(http.StatusOK, map[string]any{"repository_id": ctx.Repo.Repository.ID, "collaborator_permissions": permissions})
}

func RepairRinspacePermissions(ctx *context.APIContext) {
	form := web.GetForm(ctx).(*api.RepairRinspaceRepositoryPermissionsOption)
	if form.RepositoryID != ctx.Repo.Repository.ID {
		ctx.APIError(http.StatusConflict, "repository ID does not match the canonical repository")
		return
	}
	for _, userID := range form.OwnerUserIDs {
		user, err := user_model.GetUserByID(ctx, userID)
		if err != nil || user.IsOrganization() || !user.IsActive || user.ProhibitLogin {
			ctx.APIError(http.StatusUnprocessableEntity, "content owner is not an active individual identity")
			return
		}
		if err := repo_service.AddOrUpdateCollaborator(ctx, ctx.Repo.Repository, user, perm.AccessModeWrite); err != nil {
			ctx.APIErrorInternal(err)
			return
		}
	}
	ctx.JSON(http.StatusOK, map[string]any{"repository_id": form.RepositoryID, "owner_user_ids": form.OwnerUserIDs})
}

func UpdateRinspaceSocial(ctx *context.APIContext) {
	form := web.GetForm(ctx).(*api.UpdateRinspaceRepositorySocialOption)
	if form.RepositoryID != ctx.Repo.Repository.ID {
		ctx.APIError(http.StatusConflict, "repository ID does not match the canonical repository")
		return
	}
	actor, err := user_model.GetUserByID(ctx, form.ActorUserID)
	if err != nil || actor.IsOrganization() || !actor.IsActive || actor.ProhibitLogin {
		ctx.APIError(http.StatusUnprocessableEntity, "social actor is not an active individual Gitea identity")
		return
	}
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, ctx.Repo.Repository, actor)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	if !permission.HasAnyUnitAccessOrPublicAccess() {
		ctx.APIError(http.StatusForbidden, "social actor cannot read this repository")
		return
	}
	switch form.Relation {
	case "star":
		err = repo_model.StarRepo(ctx, actor, ctx.Repo.Repository, form.Active)
	case "watch":
		err = repo_model.WatchRepo(ctx, actor, ctx.Repo.Repository, form.Active)
	default:
		ctx.APIError(http.StatusBadRequest, "unsupported social relation")
		return
	}
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{
		"repository_id": form.RepositoryID, "actor_user_id": form.ActorUserID, "relation": form.Relation, "active": form.Active,
	})
}

func UpdateRinspacePresentation(ctx *context.APIContext) {
	form := web.GetForm(ctx).(*api.UpdateRinspaceRepositoryPresentationOption)
	if form.RepositoryID != ctx.Repo.Repository.ID {
		ctx.APIError(http.StatusConflict, "repository ID does not match the canonical repository")
		return
	}
	for _, userID := range form.ContentOwners {
		user, err := user_model.GetUserByID(ctx, userID)
		if err != nil || user.IsOrganization() {
			ctx.APIError(http.StatusUnprocessableEntity, "content owner is not an individual Gitea identity")
			return
		}
	}
	applied, err := repo_model.UpsertRinspaceRepositoryPresentation(ctx, &repo_model.RinspaceRepositoryPresentation{
		RepoID: form.RepositoryID, Title: form.Title, ContentType: form.ContentType, Version: form.Version,
	}, form.ContentOwners)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"applied": applied, "repository_id": form.RepositoryID, "version": form.Version})
}
