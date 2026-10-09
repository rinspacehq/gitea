// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	auth_model "gitea.dev/models/auth"
	rinauth_model "gitea.dev/models/rinauth"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/private"
	"gitea.dev/modules/util"
	asymkey_service "gitea.dev/services/asymkey"
	"gitea.dev/services/context"
)

type rinspaceRevocationEvent struct {
	EventID       string     `json:"eventId"`
	AggregateType string     `json:"aggregateType"`
	AggregateID   string     `json:"aggregateId"`
	EventType     string     `json:"eventType"`
	Version       int64      `json:"version"`
	Payload       json.Value `json:"payload"`
}

func ReceiveRinspaceRevocation(ctx *context.PrivateContext) {
	ctx.Req.Body = http.MaxBytesReader(ctx.Resp, ctx.Req.Body, 16<<10)
	decoder := json.NewDecoderDisallowUnknownFields(ctx.Req.Body)
	var event rinspaceRevocationEvent
	if decoder.Decode(&event) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		ctx.JSON(http.StatusBadRequest, private.Response{UserMsg: "invalid revocation event"})
		return
	}
	err := rinauth_model.RecordRevocationReceipt(ctx, &rinauth_model.RevocationReceipt{
		EventID: event.EventID, AggregateType: event.AggregateType, AggregateID: event.AggregateID,
		EventType: event.EventType, Version: event.Version, Payload: string(event.Payload),
	})
	if errors.Is(err, rinauth_model.ErrRevocationReceiptConflict) {
		ctx.JSON(http.StatusConflict, private.Response{UserMsg: "revocation event conflicts with its receipt"})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusBadRequest, private.Response{UserMsg: "invalid revocation event"})
		return
	}
	ctx.Status(http.StatusNoContent)
}

type rinspaceCredentialMetadata struct {
	Ref        string     `json:"ref"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	State      string     `json:"state"`
}

func ListRinspaceCredentials(ctx *context.PrivateContext) {
	uid := strings.TrimSpace(ctx.PathParam("uid"))
	bindings, err := rinauth_model.ListCredentialBindings(ctx, uid)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	items := make([]rinspaceCredentialMetadata, 0, len(bindings))
	for _, binding := range bindings {
		item := rinspaceCredentialMetadata{
			Ref: binding.CredentialRef, Kind: binding.Kind, Label: credentialLabel(binding.Kind, binding.CredentialRef),
			Scopes: credentialScopes(binding.Kind), CreatedAt: binding.CreatedUnix.AsTime().UTC(), State: binding.State,
		}
		if binding.LastUsedUnix > 0 {
			lastUsed := binding.LastUsedUnix.AsTime().UTC()
			item.LastUsedAt = &lastUsed
		}
		items = append(items, item)
	}
	ctx.JSON(http.StatusOK, map[string]any{"credentials": items})
}

func RevokeRinspaceCredential(ctx *context.PrivateContext) {
	uid := strings.TrimSpace(ctx.PathParam("uid"))
	ref := strings.TrimSpace(ctx.PathParam("ref"))
	identity, err := rinauth_model.GetUserIdentityByUID(ctx, uid)
	if err != nil {
		ctx.JSON(http.StatusNotFound, private.Response{UserMsg: "credential owner was not found"})
		return
	}
	binding, err := rinauth_model.GetCredentialBinding(ctx, ref)
	if err != nil || binding.UserID != identity.UserID || binding.RinspaceUID != uid {
		ctx.JSON(http.StatusNotFound, private.Response{UserMsg: "credential was not found"})
		return
	}
	if _, err := rinauth_model.RevokeCredentialBinding(ctx, uid, ref); err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	if err := deleteNativeCredential(ctx, identity.UserID, ref); err != nil {
		ctx.JSON(http.StatusServiceUnavailable, private.Response{Err: err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, map[string]string{"operationId": "gitea:" + ref, "state": "complete"})
}

func RevokeAllRinspaceCredentials(ctx *context.PrivateContext) {
	uid := strings.TrimSpace(ctx.PathParam("uid"))
	identity, err := rinauth_model.GetUserIdentityByUID(ctx, uid)
	if err != nil {
		ctx.JSON(http.StatusNotFound, private.Response{UserMsg: "credential owner was not found"})
		return
	}
	bindings, err := rinauth_model.ListCredentialBindings(ctx, uid)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	if _, err := rinauth_model.RevokeAllCredentialBindings(ctx, uid); err != nil {
		ctx.JSON(http.StatusInternalServerError, private.Response{Err: err.Error()})
		return
	}
	for _, binding := range bindings {
		if err := deleteNativeCredential(ctx, identity.UserID, binding.CredentialRef); err != nil {
			ctx.JSON(http.StatusServiceUnavailable, private.Response{Err: err.Error()})
			return
		}
	}
	ctx.JSON(http.StatusOK, map[string]string{"operationId": "gitea:all:" + uid, "state": "complete"})
}

func deleteNativeCredential(ctx *context.PrivateContext, userID int64, ref string) error {
	kind, rawID, ok := strings.Cut(ref, ":")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if !ok || err != nil || id <= 0 {
		return errors.New("invalid credential reference")
	}
	switch kind {
	case "pat":
		err = auth_model.DeleteAccessTokenByID(ctx, id, userID)
	case "oauth":
		err = auth_model.RevokeOAuth2Grant(ctx, id, userID)
	case "ssh":
		user, getErr := user_model.GetUserByID(ctx, userID)
		if getErr != nil {
			return getErr
		}
		err = asymkey_service.DeletePublicKey(ctx, user, id)
	default:
		return errors.New("unsupported credential kind")
	}
	if errors.Is(err, util.ErrNotExist) {
		return nil
	}
	return err
}

func credentialLabel(kind, ref string) string {
	_, id, _ := strings.Cut(ref, ":")
	switch kind {
	case "ssh":
		return "SSH key " + id
	case "pat":
		return "Personal access token " + id
	case "oauth":
		return "OAuth grant " + id
	default:
		return "Gitea credential"
	}
}

func credentialScopes(kind string) []string {
	switch kind {
	case "ssh":
		return []string{"git"}
	case "pat":
		return []string{"api", "git", "packages"}
	case "oauth":
		return []string{"oauth"}
	default:
		return []string{}
	}
}
