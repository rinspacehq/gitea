// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
)

func GetRinspaceUserFollow(ctx *context.APIContext) {
	controlPlaneUser := strings.TrimSpace(os.Getenv("RIN_CONTROL_GITEA_USERNAME"))
	if controlPlaneUser == "" || ctx.Doer == nil || ctx.Doer.Name != controlPlaneUser {
		ctx.APIError(http.StatusForbidden, "Rin Control Plane service identity is not configured")
		return
	}
	actorID, actorErr := strconv.ParseInt(ctx.FormString("actor_user_id"), 10, 64)
	targetID, targetErr := strconv.ParseInt(ctx.FormString("target_user_id"), 10, 64)
	if actorErr != nil || targetErr != nil || actorID <= 0 || targetID <= 0 || actorID == targetID {
		ctx.APIError(http.StatusBadRequest, "user follow identity is invalid")
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"actor_user_id": actorID, "target_user_id": targetID, "active": user_model.IsFollowing(ctx, actorID, targetID)})
}

func UpdateRinspaceUserFollow(ctx *context.APIContext) {
	controlPlaneUser := strings.TrimSpace(os.Getenv("RIN_CONTROL_GITEA_USERNAME"))
	if controlPlaneUser == "" || ctx.Doer == nil || ctx.Doer.Name != controlPlaneUser {
		ctx.APIError(http.StatusForbidden, "Rin Control Plane service identity is not configured")
		return
	}
	form := web.GetForm(ctx).(*api.UpdateRinspaceUserFollowOption)
	actor, err := user_model.GetUserByID(ctx, form.ActorUserID)
	if err != nil || actor.IsOrganization() || !actor.IsActive || actor.ProhibitLogin {
		ctx.APIError(http.StatusUnprocessableEntity, "follow actor is not an active individual identity")
		return
	}
	target, err := user_model.GetUserByID(ctx, form.TargetUserID)
	if err != nil || target.IsOrganization() || !target.IsActive || target.ProhibitLogin {
		ctx.APIError(http.StatusUnprocessableEntity, "follow target is not an active individual identity")
		return
	}
	if form.Active {
		err = user_model.FollowUser(ctx, actor, target)
	} else {
		err = user_model.UnfollowUser(ctx, actor.ID, target.ID)
	}
	if err != nil {
		if errors.Is(err, user_model.ErrBlockedUser) {
			ctx.APIError(http.StatusForbidden, err.Error())
		} else {
			ctx.APIErrorInternal(err)
		}
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"actor_user_id": actor.ID, "target_user_id": target.ID, "active": form.Active})
}
