// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"time"

	repo_model "gitea.dev/models/repo"
	rincontrol_model "gitea.dev/models/rincontrol"
	repo_module "gitea.dev/modules/repository"
)

// EnqueueRinspacePublicationPushEvents records successful managed branch
// updates before the asynchronous push notification pipeline begins.
func EnqueueRinspacePublicationPushEvents(ctx context.Context, repo *repo_model.Repository, updates []*repo_module.PushUpdateOptions) error {
	if repo == nil || repo.ID <= 0 {
		return errors.New("publication push repository is invalid")
	}
	if err := repo.LoadRinspacePresentation(ctx); err != nil {
		return err
	}
	if repo.RinspaceProjectionVersion <= 0 || !isSupportedRinspacePublicationType(repo.RinspaceContentType) {
		return nil
	}
	occurredAt := time.Now().UTC()
	for _, update := range updates {
		if update == nil || !update.RefFullName.IsBranch() || update.IsDelRef() || update.OldCommitID == update.NewCommitID {
			continue
		}
		if err := rincontrol_model.EnqueueRepositoryPushEvent(ctx, rincontrol_model.RepositoryPushEvent{
			RepositoryID: repo.ID,
			Ref:          update.RefFullName.String(),
			Before:       update.OldCommitID,
			After:        update.NewCommitID,
			OccurredAt:   occurredAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func isSupportedRinspacePublicationType(contentType string) bool {
	switch contentType {
	case repo_model.RinspaceContentArticle, repo_model.RinspaceContentBook, repo_model.RinspaceContentPDF, repo_model.RinspaceContentTagWiki:
		return true
	default:
		return false
	}
}
