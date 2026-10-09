// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package structs

type UpdateRinspaceUserFollowOption struct {
	ActorUserID  int64 `json:"actor_user_id" binding:"Required"`
	TargetUserID int64 `json:"target_user_id" binding:"Required"`
	Active       bool  `json:"active"`
}

type RepairRinspaceRepositoryPermissionsOption struct {
	RepositoryID int64   `json:"repository_id" binding:"Required"`
	OwnerUserIDs []int64 `json:"owner_user_ids" binding:"Required"`
}
