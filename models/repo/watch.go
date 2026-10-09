// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"

	"gitea.dev/models/db"
	rincontrol_model "gitea.dev/models/rincontrol"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// WatchMode specifies what kind of watch the user has on a repository
type WatchMode int8

const (
	// WatchModeNone don't watch
	WatchModeNone WatchMode = iota // 0
	// WatchModeNormal watch repository (from other sources)
	WatchModeNormal // 1
	// WatchModeDont explicit don't auto-watch
	WatchModeDont // 2
	// WatchModeAuto watch repository (from AutoWatchOnChanges)
	WatchModeAuto // 3
)

// Watch is connection request for receiving repository notification.
type Watch struct {
	ID          int64              `xorm:"pk autoincr"`
	UserID      int64              `xorm:"UNIQUE(watch)"`
	RepoID      int64              `xorm:"UNIQUE(watch)"`
	Mode        WatchMode          `xorm:"SMALLINT NOT NULL DEFAULT 1"`
	CreatedUnix timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"INDEX updated"`
}

func init() {
	db.RegisterModel(new(Watch))
}

// GetWatch gets what kind of subscription a user has on a given repository; returns dummy record if none found
func GetWatch(ctx context.Context, userID, repoID int64) (*Watch, error) {
	watch, has, err := db.Get[Watch](ctx, builder.Eq{"user_id": userID, "repo_id": repoID})
	if err != nil {
		return watch, err
	}
	if watch == nil {
		watch = &Watch{UserID: userID, RepoID: repoID}
	}
	if !has {
		watch.Mode = WatchModeNone
	}
	return watch, nil
}

// IsWatchMode Decodes watchability of WatchMode
func IsWatchMode(mode WatchMode) bool {
	return mode != WatchModeNone && mode != WatchModeDont
}

// IsWatching checks if user has watched given repository.
func IsWatching(ctx context.Context, userID, repoID int64) bool {
	watch, err := GetWatch(ctx, userID, repoID)
	return err == nil && IsWatchMode(watch.Mode)
}

func watchRepoMode(ctx context.Context, watch *Watch, mode WatchMode) (err error) {
	return db.WithTx(ctx, func(ctx context.Context) error {
		current, err := GetWatch(ctx, watch.UserID, watch.RepoID)
		if err != nil {
			return err
		}
		if current.Mode == mode {
			return nil
		}
		if mode == WatchModeAuto && (current.Mode == WatchModeDont || IsWatchMode(current.Mode)) {
			// Don't auto watch if already watching or deliberately not watching
			return nil
		}

		hadrec := current.Mode != WatchModeNone
		needsrec := mode != WatchModeNone
		wasWatching := IsWatchMode(current.Mode)
		isWatching := IsWatchMode(mode)
		repodiff := 0
		if isWatching && !wasWatching {
			repodiff = 1
		} else if !isWatching && wasWatching {
			repodiff = -1
		}
		current.Mode = mode
		if !hadrec && needsrec {
			if err = db.Insert(ctx, current); err != nil {
				return err
			}
		} else if needsrec {
			if _, err := db.GetEngine(ctx).ID(current.ID).AllCols().Update(current); err != nil {
				return err
			}
		} else if _, err = db.DeleteByID[Watch](ctx, current.ID); err != nil {
			return err
		}
		if repodiff != 0 {
			if _, err = db.GetEngine(ctx).Exec("UPDATE `repository` SET num_watches = num_watches + ? WHERE id = ?", repodiff, current.RepoID); err != nil {
				return err
			}
			if err = rincontrol_model.EnqueueSocialEvent(ctx, rincontrol_model.SocialEvent{EventType: "repository.watch.changed", ActorUserID: current.UserID, RepositoryID: current.RepoID, Active: isWatching}); err != nil {
				return err
			}
		}
		return nil
	})
}

// WatchRepo watch or unwatch repository.
func WatchRepo(ctx context.Context, doer *user_model.User, repo *Repository, doWatch bool) error {
	watch, err := GetWatch(ctx, doer.ID, repo.ID)
	if err != nil {
		return err
	}
	if !doWatch && watch.Mode == WatchModeAuto {
		return watchRepoMode(ctx, watch, WatchModeDont)
	} else if !doWatch {
		return watchRepoMode(ctx, watch, WatchModeNone)
	}

	if user_model.IsUserBlockedBy(ctx, doer, repo.OwnerID) {
		return user_model.ErrBlockedUser
	}

	return watchRepoMode(ctx, watch, WatchModeNormal)
}

// GetWatchers returns all watchers of given repository.
func GetWatchers(ctx context.Context, repoID int64) ([]*Watch, error) {
	watches := make([]*Watch, 0, 10)
	return watches, db.GetEngine(ctx).Where("`watch`.repo_id=?", repoID).
		And("`watch`.mode<>?", WatchModeDont).
		And("`user`.is_active=?", true).
		And("`user`.prohibit_login=?", false).
		Join("INNER", "`user`", "`user`.id = `watch`.user_id").
		Find(&watches)
}

// GetRepoWatchersIDs returns IDs of watchers for a given repo ID
// but avoids joining with `user` for performance reasons
// User permissions must be verified elsewhere if required
func GetRepoWatchersIDs(ctx context.Context, repoID int64) ([]int64, error) {
	ids := make([]int64, 0, 64)
	return ids, db.GetEngine(ctx).Table("watch").
		Where("watch.repo_id=?", repoID).
		And("watch.mode<>?", WatchModeDont).
		Select("user_id").
		Find(&ids)
}

// GetRepoWatchers returns range of users watching given repository.
func GetRepoWatchers(ctx context.Context, repoID int64, opts db.ListOptions) ([]*user_model.User, error) {
	sess := db.GetEngine(ctx).Where("watch.repo_id=?", repoID).
		Join("LEFT", "watch", "`user`.id=`watch`.user_id").
		And("`watch`.mode<>?", WatchModeDont)
	if opts.Page > 0 {
		db.SetSessionPagination(sess, &opts)
		users := make([]*user_model.User, 0, opts.PageSize)

		return users, sess.Find(&users)
	}

	users := make([]*user_model.User, 0, 8)
	return users, sess.Find(&users)
}

// WatchIfAuto subscribes to repo if AutoWatchOnChanges is set
func WatchIfAuto(ctx context.Context, userID, repoID int64, isWrite bool) error {
	if !isWrite || !setting.Service.AutoWatchOnChanges {
		return nil
	}
	watch, err := GetWatch(ctx, userID, repoID)
	if err != nil {
		return err
	}
	if watch.Mode != WatchModeNone {
		return nil
	}
	return watchRepoMode(ctx, watch, WatchModeAuto)
}

// ClearRepoWatches clears all watches for a repository and from the user that watched it.
// Visibility changes must preserve these rows; this is for destructive cleanup.
func ClearRepoWatches(ctx context.Context, repoID int64) error {
	if _, err := db.Exec(ctx, "UPDATE `repository` SET num_watches = 0 WHERE id = ?", repoID); err != nil {
		return err
	}

	return db.DeleteBeans(ctx, Watch{RepoID: repoID})
}
