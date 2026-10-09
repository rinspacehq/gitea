// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

const (
	RinspaceContentArticle = "article"
	RinspaceContentBook    = "book"
	RinspaceContentPDF     = "pdf"
	RinspaceContentTagWiki = "tag-wiki"
)

type RinspaceRepositoryPresentation struct {
	RepoID      int64              `xorm:"pk"`
	Title       string             `xorm:"VARCHAR(512) NOT NULL DEFAULT ''"`
	ContentType string             `xorm:"VARCHAR(16) INDEX NOT NULL"`
	Version     int64              `xorm:"NOT NULL DEFAULT 1"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

type RinspaceRepositoryOwner struct {
	RepoID int64 `xorm:"pk"`
	UserID int64 `xorm:"pk INDEX"`
}

func init() {
	db.RegisterModel(new(RinspaceRepositoryPresentation))
	db.RegisterModel(new(RinspaceRepositoryOwner))
}

func (presentation *RinspaceRepositoryPresentation) Validate() error {
	if presentation.RepoID <= 0 || presentation.Version <= 0 {
		return errors.New("repository ID and projection version must be positive")
	}
	if len(strings.TrimSpace(presentation.Title)) > 512 {
		return errors.New("Rinspace repository title is too long")
	}
	switch presentation.ContentType {
	case RinspaceContentArticle, RinspaceContentBook, RinspaceContentPDF, RinspaceContentTagWiki:
		return nil
	default:
		return fmt.Errorf("unsupported Rinspace content type %q", presentation.ContentType)
	}
}

func UpsertRinspaceRepositoryPresentation(ctx context.Context, presentation *RinspaceRepositoryPresentation, ownerIDs []int64) (bool, error) {
	if err := presentation.Validate(); err != nil {
		return false, err
	}
	returnValue := false
	err := db.WithTx(ctx, func(ctx context.Context) error {
		current := new(RinspaceRepositoryPresentation)
		has, err := db.GetEngine(ctx).ID(presentation.RepoID).Get(current)
		if err != nil {
			return err
		}
		if has && current.Version >= presentation.Version {
			return nil
		}
		if has {
			if _, err := db.GetEngine(ctx).ID(presentation.RepoID).AllCols().Update(presentation); err != nil {
				return err
			}
		} else if _, err := db.GetEngine(ctx).Insert(presentation); err != nil {
			return err
		}
		if _, err := db.DeleteByBean(ctx, &RinspaceRepositoryOwner{RepoID: presentation.RepoID}); err != nil {
			return err
		}
		seen := make(map[int64]struct{}, len(ownerIDs))
		for _, userID := range ownerIDs {
			if userID <= 0 {
				return errors.New("content owner user ID must be positive")
			}
			if _, ok := seen[userID]; ok {
				continue
			}
			seen[userID] = struct{}{}
			if _, err := db.GetEngine(ctx).Insert(&RinspaceRepositoryOwner{RepoID: presentation.RepoID, UserID: userID}); err != nil {
				return err
			}
		}
		returnValue = true
		return nil
	})
	return returnValue, err
}

func loadRinspaceRepositoryPresentations(ctx context.Context, repos RepositoryList) error {
	if len(repos) == 0 {
		return nil
	}
	byID := make(map[int64]*Repository, len(repos))
	ids := make([]int64, 0, len(repos))
	for _, repo := range repos {
		byID[repo.ID] = repo
		ids = append(ids, repo.ID)
	}
	presentations := make([]*RinspaceRepositoryPresentation, 0, len(repos))
	if err := db.GetEngine(ctx).In("repo_id", ids).Find(&presentations); err != nil {
		return err
	}
	for _, presentation := range presentations {
		if repo := byID[presentation.RepoID]; repo != nil {
			repo.RinspaceTitle = presentation.Title
			repo.RinspaceContentType = presentation.ContentType
			repo.RinspaceProjectionVersion = presentation.Version
		}
	}
	owners := make([]*RinspaceRepositoryOwner, 0)
	if err := db.GetEngine(ctx).In("repo_id", ids).Find(&owners); err != nil {
		return err
	}
	for _, owner := range owners {
		if repo := byID[owner.RepoID]; repo != nil {
			repo.RinspaceContentOwnerIDs = append(repo.RinspaceContentOwnerIDs, owner.UserID)
		}
	}
	return nil
}

func (repo *Repository) LoadRinspacePresentation(ctx context.Context) error {
	return loadRinspaceRepositoryPresentations(ctx, RepositoryList{repo})
}
