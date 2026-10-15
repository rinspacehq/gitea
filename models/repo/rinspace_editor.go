// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitea.dev/models/db"
	"gitea.dev/models/perm"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/rinspacecatalog"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/structs"

	"xorm.io/builder"
)

var ErrRinspaceEditorCatalogUnavailable = errors.New("indexed Rinspace editor catalog is unavailable")

type RinspaceEditorProjectOptions struct {
	Actor      *user_model.User
	Query      string
	BeforeID   int64
	Limit      int
	PublicOnly bool
}

type RinspaceEditorProjectPage struct {
	Repositories RepositoryList
	HasMore      bool
	NextCursor   string
}

func (opts RinspaceEditorProjectOptions) Validate() error {
	if opts.Actor == nil || opts.Actor.ID <= 0 || opts.Actor.IsOrganization() || !opts.Actor.IsActive || opts.Actor.ProhibitLogin ||
		opts.Limit < 1 || opts.Limit > 50 || opts.BeforeID < 0 || len(opts.Query) > 200 ||
		!utf8.ValidString(opts.Query) || strings.ContainsAny(opts.Query, "\x00\r\n") {
		return errors.New("invalid editor catalog options")
	}
	return nil
}

// Readiness is checked without DDL; unsupported databases and incomplete
// migrations fail closed instead of falling back to full catalog scans.
func rinspaceEditorSchema(ctx context.Context) (string, error) {
	if !setting.Database.Type.IsPostgreSQL() {
		return "", ErrRinspaceEditorCatalogUnavailable
	}
	var schema struct{ Name string }
	if _, err := db.GetEngine(ctx).SQL("SELECT current_schema() AS name").Get(&schema); err != nil || schema.Name == "" {
		return "", ErrRinspaceEditorCatalogUnavailable
	}
	args := []any{schema.Name}
	for _, name := range rinspacecatalog.IndexNames {
		args = append(args, name)
	}
	var ready struct{ Total int64 }
	query := `SELECT count(*) AS total FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class c ON c.oid=i.indexrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE i.indisvalid AND i.indisready AND n.nspname=? AND c.relname IN (` + strings.TrimSuffix(strings.Repeat("?,", len(rinspacecatalog.IndexNames)), ",") + `)`
	if _, err := db.GetEngine(ctx).SQL(query, args...).Get(&ready); err != nil || ready.Total != int64(len(rinspacecatalog.IndexNames)) {
		return "", ErrRinspaceEditorCatalogUnavailable
	}
	return schema.Name, nil
}

type rinspaceEditorSource struct {
	join string
	args []any
	cond builder.Cond
}

func rinspaceEditorSources(opts RinspaceEditorProjectOptions) []rinspaceEditorSource {
	id := opts.Actor.ID
	return []rinspaceEditorSource{
		{cond: builder.Eq{"repository.owner_id": id}},
		{join: ` JOIN "access" editor_access ON editor_access.repo_id=repository.id AND editor_access.user_id=? AND editor_access.mode>0`, args: []any{id}, cond: builder.NewCond()},
		{join: ` JOIN team_repo editor_team_repo ON editor_team_repo.repo_id=repository.id JOIN team_user editor_team_user ON editor_team_user.team_id=editor_team_repo.team_id AND editor_team_user.uid=?`, args: []any{id}, cond: builder.NewCond()},
		{cond: userOrgPublicRepoCondPrivate(id)},
		{
			join: ` JOIN rinspace_repository_owner editor_owner ON editor_owner.repo_id=repository.id AND editor_owner.user_id=?`, args: []any{id},
			cond: builder.In("repository.id", builder.Select("repo_id").From("rinspace_repository_presentation").Where(builder.In("content_type", RinspaceContentArticle, RinspaceContentBook, RinspaceContentPDF))),
		},
	}
}

func rinspaceEditorProjectSQL(schema string, opts RinspaceEditorProjectOptions) (string, []any, error) {
	if err := opts.Validate(); err != nil {
		return "", nil, err
	}
	base := builder.And(
		builder.Neq{"repository.default_branch": ""},
		builder.In("repository.id", builder.Select("repo_id").From("repo_unit").Where(builder.Eq{"type": unit.TypeCode})),
	)
	if !opts.Actor.IsAdmin {
		// Scalar subqueries keep ACL lookups correlated to candidate IDs. The
		// generic IN conditions may hash every tag collaboration before LIMIT.
		collaboration := builder.Expr(`COALESCE((SELECT editor_collaboration.mode FROM collaboration editor_collaboration
WHERE editor_collaboration.repo_id=repository.id AND editor_collaboration.user_id=?),0)>0`, opts.Actor.ID)
		team := builder.Expr(`(SELECT 1 FROM team_repo editor_acl_repo
JOIN team_user editor_acl_user ON editor_acl_user.team_id=editor_acl_repo.team_id
JOIN team editor_acl_team ON editor_acl_team.id=editor_acl_repo.team_id
LEFT JOIN team_unit editor_acl_unit ON editor_acl_unit.team_id=editor_acl_repo.team_id AND editor_acl_unit.type=?
WHERE editor_acl_repo.repo_id=repository.id AND editor_acl_user.uid=?
AND (editor_acl_team.authorize>=? OR editor_acl_unit.access_mode>0) LIMIT 1)=1`, unit.TypeCode, opts.Actor.ID, perm.AccessModeAdmin)
		readable := builder.Or(builder.Eq{"repository.owner_id": opts.Actor.ID}, collaboration, team)
		if !opts.Actor.IsRestricted {
			readable = userAllPublicRepoCond(readable, []structs.VisibleType{structs.VisibleTypePrivate}).Or(userOrgPublicRepoCond(opts.Actor.ID))
		} else if !setting.Service.RequireSignInViewStrict {
			readable = userAllPublicRepoCond(readable, []structs.VisibleType{structs.VisibleTypePrivate, structs.VisibleTypeLimited})
		}
		base = base.And(readable)
		if opts.Actor.IsRestricted {
			base = base.And(builder.Or(
				builder.Eq{"repository.owner_id": opts.Actor.ID},
				collaboration,
				team,
				builder.In("repository.id", builder.Select("repo_id").From("repo_unit").Where(builder.And(
					builder.Eq{"type": unit.TypeCode},
					builder.Or(builder.Gt{"everyone_access_mode": 0}, builder.Gt{"anonymous_access_mode": 0}),
				))),
			))
		}
	}
	if opts.PublicOnly {
		base = base.And(PublicRepoUnderPublicOwnerCond())
	}
	if opts.BeforeID > 0 {
		base = base.And(builder.Lt{"repository.id": opts.BeforeID})
	}
	ordinary := builder.And(builder.Neq{"repository.owner_name": "tags"}, builder.Expr(`NOT EXISTS (
SELECT 1 FROM rinspace_repository_presentation editor_tag WHERE editor_tag.repo_id=repository.id AND editor_tag.content_type='tag-wiki')`))
	sources := rinspaceEditorSources(opts)
	branches := make([]string, 0, 12)
	args := make([]any, 0, 80)
	appendBranch := func(source rinspaceEditorSource, tags, title bool) error {
		cond := base.And(source.cond)
		if !tags {
			cond = cond.And(ordinary)
		}
		join := source.join
		if opts.Query != "" {
			if strings.Count(opts.Query, "/") == 1 {
				coordinate := strings.SplitN(strings.ToLower(opts.Query), "/", 2)
				cond = cond.And(builder.Eq{"repository.lower_name": coordinate[1]},
					builder.In("repository.owner_id", builder.Select("id").From("`user`").Where(builder.Eq{"lower_name": coordinate[0]})))
			} else {
				column := "repository.lower_name"
				if title {
					join += " JOIN rinspace_repository_presentation editor_title ON editor_title.repo_id=repository.id"
					column = "editor_title.title"
					if opts.BeforeID > 0 {
						cond = cond.And(builder.Lt{"editor_title.repo_id": opts.BeforeID})
					}
				}
				cond = cond.And(builder.Expr(rinspacecatalog.Contains(schema, column), opts.Query, opts.Query, opts.Query))
			}
		}
		where, values, err := builder.ToSQL(cond)
		if err != nil {
			return err
		}
		branches = append(branches, fmt.Sprintf(`(SELECT DISTINCT repository.id FROM repository%s WHERE %s ORDER BY repository.id DESC LIMIT %d)`, join, where, opts.Limit+1))
		args = append(args, source.args...)
		args = append(args, values...)
		return nil
	}
	for _, source := range sources {
		if err := appendBranch(source, false, false); err != nil {
			return "", nil, err
		}
		if opts.Query != "" && !strings.Contains(opts.Query, "/") {
			if err := appendBranch(source, false, true); err != nil {
				return "", nil, err
			}
		}
	}
	if opts.Query != "" {
		tags := rinspaceEditorSource{cond: builder.Eq{"repository.owner_name": "tags", "repository.is_private": false, "repository.is_archived": false}}
		for _, title := range []bool{false, true} {
			if err := appendBranch(tags, true, title); err != nil {
				return "", nil, err
			}
			if strings.Contains(opts.Query, "/") {
				break
			}
		}
	}
	return fmt.Sprintf("SELECT id FROM (%s) editor_candidates ORDER BY id DESC LIMIT %d", strings.Join(branches, " UNION "), opts.Limit+1), args, nil
}

func FindRinspaceEditorProjects(ctx context.Context, opts RinspaceEditorProjectOptions) (RinspaceEditorProjectPage, error) {
	if err := opts.Validate(); err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	schema, err := rinspaceEditorSchema(ctx)
	if err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	statement, args, err := rinspaceEditorProjectSQL(schema, opts)
	if err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	var rows []struct{ ID int64 }
	if err := db.GetEngine(ctx).SQL(statement, args...).Find(&rows); err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	page := RinspaceEditorProjectPage{Repositories: make(RepositoryList, 0, opts.Limit), HasMore: len(rows) > opts.Limit}
	if page.HasMore {
		rows = rows[:opts.Limit]
		page.NextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	if len(ids) == 0 {
		return page, nil
	}
	if err := db.GetEngine(ctx).In("id", ids).Desc("id").Find(&page.Repositories); err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	if err := page.Repositories.LoadOwners(ctx); err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	if err := page.Repositories.LoadUnits(ctx); err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	if err := loadRinspaceRepositoryPresentations(ctx, page.Repositories); err != nil {
		return RinspaceEditorProjectPage{}, err
	}
	return page, nil
}
