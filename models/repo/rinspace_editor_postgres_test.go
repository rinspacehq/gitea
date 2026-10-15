// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build rinspace_catalog_postgres

package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/migrations/v1_27"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/rinspacecatalog"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/structs"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

// This opt-in suite fails without an explicitly named disposable database;
// it never discovers a database or credentials from application configuration.
func TestRinspaceEditorProjectPostgres(t *testing.T) {
	dsn := os.Getenv("RINSPACE_CATALOG_TEST_DSN")
	require.NotEmpty(t, dsn, "explicit isolated PostgreSQL DSN is required")
	engine, err := xorm.NewEngine("postgres", dsn)
	require.NoError(t, err)
	engine.SetMaxOpenConns(1)
	engine.SetMaxIdleConns(1)
	engine.SetMapper(names.GonicMapper{})
	var database struct{ Name string }
	_, err = engine.SQL("SELECT current_database() AS name").Get(&database)
	require.NoError(t, err)
	require.Equal(t, "editor_catalog_model_test", database.Name, "refuse application databases")
	_, err = engine.Exec("CREATE SCHEMA rinspace_catalog_model_test")
	require.NoError(t, err, "refuse to overwrite an existing test schema")
	_, err = engine.Exec("SET search_path TO rinspace_catalog_model_test")
	require.NoError(t, err)
	engine.SetSchema("rinspace_catalog_model_test")
	_, err = engine.Exec("SET statement_timeout TO '15s'")
	require.NoError(t, err)
	_, err = engine.Exec("SET max_parallel_maintenance_workers TO 0")
	require.NoError(t, err)
	require.NoError(t, engine.Sync(new(Repository), new(user_model.User), new(RepoUnit), new(Collaboration), new(RinspaceRepositoryPresentation), new(RinspaceRepositoryOwner)))
	for _, statement := range []string{
		`CREATE TABLE access (id bigserial PRIMARY KEY, repo_id bigint NOT NULL, user_id bigint NOT NULL, mode integer NOT NULL)`,
		`CREATE TABLE team_repo (team_id bigint, repo_id bigint)`,
		`CREATE TABLE team_user (team_id bigint, uid bigint)`,
		`CREATE TABLE team (id bigint, authorize integer, org_id bigint)`,
		`CREATE TABLE team_unit (team_id bigint, type integer, access_mode integer)`,
		`CREATE TABLE org_user (org_id bigint, uid bigint, is_public boolean)`,
	} {
		_, err = engine.Exec(statement)
		require.NoError(t, err)
	}
	actor := &user_model.User{ID: 2, Name: "reader", LowerName: "reader", Email: "reader@example.test", IsActive: true}
	_, err = engine.Insert(actor, &user_model.User{ID: 3, Name: "writer", LowerName: "writer", Email: "writer@example.test", IsActive: true},
		&user_model.User{ID: 99, Name: "tags", LowerName: "tags", Email: "tags@example.test", IsActive: true, Type: user_model.UserTypeOrganization})
	require.NoError(t, err)
	previousEngine, previousType := db.GetXORMEngineForTesting(), setting.Database.Type
	db.SetDefaultEngine(t.Context(), engine)
	setting.Database.Type = "postgres"
	t.Cleanup(func() {
		db.SetDefaultEngine(context.Background(), previousEngine)
		setting.Database.Type = previousType
		require.NoError(t, engine.Close())
	})
	for _, scale := range []int{10000, 100000} {
		t.Run(strconv.Itoa(scale), func(t *testing.T) {
			fixtureStart := time.Now()
			_, err := engine.Exec("SET statement_timeout TO '180s'")
			require.NoError(t, err)
			for _, table := range []string{"repository", "repo_unit", "collaboration", "access", "rinspace_repository_presentation", "rinspace_repository_owner", "team", "team_repo", "team_user", "team_unit", "org_user"} {
				_, err := engine.Exec("TRUNCATE " + rinspacecatalog.Identifier(table))
				require.NoError(t, err)
			}
			for _, statement := range []string{
				fmt.Sprintf(`INSERT INTO repository (id,owner_id,owner_name,lower_name,name,default_branch,is_private,is_archived)
SELECT n,99,'tags',n::text,n::text,'main',n%%97=0,n%%89=0 FROM generate_series(1,%d) n`, scale),
				fmt.Sprintf(`INSERT INTO repository (id,owner_id,owner_name,lower_name,name,default_branch,is_private,is_archived)
SELECT %d+n,CASE WHEN n%%2=0 THEN 2 ELSE 3 END,CASE WHEN n%%2=0 THEN 'reader' ELSE 'writer' END,'project-'||n,'project-'||n,'main',false,false FROM generate_series(1,200) n`, scale),
				`INSERT INTO access(repo_id,user_id,mode) SELECT id,2,2 FROM repository`,
				`INSERT INTO collaboration(repo_id,user_id,mode) SELECT id,2,2 FROM repository`,
				fmt.Sprintf(`INSERT INTO repo_unit(repo_id,type) SELECT id,%d FROM repository`, unit.TypeCode),
				`INSERT INTO rinspace_repository_presentation(repo_id,title,content_type,version)
SELECT id,CASE WHEN id=25 THEN 'General quantum 数学例子' ELSE '数学理论 标签 '||id END,'tag-wiki',1 FROM repository WHERE owner_name='tags'`,
				`DELETE FROM rinspace_repository_presentation WHERE repo_id=42`,
				`DELETE FROM access WHERE repo_id=25`, `DELETE FROM collaboration WHERE repo_id=25`,
				fmt.Sprintf(`UPDATE repository SET is_private=true WHERE id=%d`, scale+199),
				fmt.Sprintf(`DELETE FROM access WHERE repo_id=%d`, scale+199),
				fmt.Sprintf(`DELETE FROM collaboration WHERE repo_id=%d`, scale+199),
				fmt.Sprintf(`INSERT INTO rinspace_repository_presentation(repo_id,title,content_type,version) VALUES(%d,'Hidden ownership alone','book',1)`, scale+199),
				fmt.Sprintf(`INSERT INTO rinspace_repository_owner(repo_id,user_id) VALUES(%d,2)`, scale+199),
				fmt.Sprintf(`DELETE FROM repo_unit WHERE repo_id=%d`, scale+198),
			} {
				_, err := engine.Exec(statement)
				require.NoError(t, err)
			}
			// Initial index construction needs a separate bounded migration budget,
			// not the interactive query timeout.
			_, err = engine.Exec("SET statement_timeout TO '180s'")
			require.NoError(t, err)
			migrationStart := time.Now()
			require.NoError(t, v1_27.AddRinspaceEditorCatalogIndexes(engine))
			t.Logf("scale=%d migrationMs=%d", scale, time.Since(migrationStart).Milliseconds())
			require.NoError(t, v1_27.AddRinspaceEditorCatalogIndexes(engine), "migration must be retryable")
			var indexSize struct{ Bytes int64 }
			_, err = engine.SQL(`SELECT sum(pg_relation_size(indexrelid)) AS bytes FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='rinspace_catalog_model_test' AND c.relname LIKE 'rinspace_editor_%'`).Get(&indexSize)
			require.NoError(t, err)
			t.Logf("scale=%d catalogIndexBytes=%d", scale, indexSize.Bytes)
			for _, table := range []string{"repository", "repo_unit", "access", "collaboration", "rinspace_repository_presentation"} {
				_, err := engine.Exec("VACUUM ANALYZE " + rinspacecatalog.Identifier(table))
				require.NoError(t, err)
			}
			t.Logf("scale=%d fixtureAndMaintenanceMs=%d", scale, time.Since(fixtureStart).Milliseconds())
			_, err = engine.Exec("SET statement_timeout TO '15s'")
			require.NoError(t, err)
			seen := map[int64]bool{}
			before := int64(0)
			for range 20 {
				page, err := FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: actor, BeforeID: before, Limit: 24})
				require.NoError(t, err)
				for _, repository := range page.Repositories {
					require.Greater(t, repository.ID, int64(scale))
					require.NotEqual(t, int64(scale+199), repository.ID, "ownership projection must not grant read access")
					require.NotEqual(t, int64(scale+198), repository.ID, "disabled Code unit must not be listed")
					require.False(t, seen[repository.ID], "duplicate across cursor pages")
					seen[repository.ID] = true
				}
				if !page.HasMore {
					break
				}
				_, err = fmt.Sscan(page.NextCursor, &before)
				require.NoError(t, err)
			}
			require.Len(t, seen, 198)
			for _, query := range []string{"General", "tags/25", "tags/42"} {
				page, err := FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: actor, Query: query, Limit: 24})
				require.NoError(t, err)
				require.Len(t, page.Repositories, 1, query)
			}
			for _, item := range []struct {
				query  string
				before int64
			}{
				{"", 0}, {"quantum", 0}, {"标签", 0}, {"数", 0}, {"稀词", 0}, {"罕", 0}, {"%", 0}, {"project-1", 0}, {"1", 0}, {"标签", int64(scale / 10)},
			} {
				query := item.query
				opts := RinspaceEditorProjectOptions{Actor: actor, Query: query, Limit: 24, BeforeID: item.before}
				statement, args, err := rinspaceEditorProjectSQL("rinspace_catalog_model_test", opts)
				require.NoError(t, err)
				for iteration := range 7 {
					result, err := engine.SQL("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement, args...).Query()
					require.NoError(t, err)
					if reportDir := os.Getenv("RINSPACE_CATALOG_REPORT_DIR"); reportDir != "" {
						label := strings.NewReplacer("%", "literal-percent", " ", "-").Replace(query)
						require.NoError(t, os.WriteFile(filepath.Join(reportDir, fmt.Sprintf("%d-%s-before%d-%d.json", scale, label, item.before, iteration)), result[0]["QUERY PLAN"], 0o600))
					}
					var plan []struct {
						ExecutionTime float64 `json:"Execution Time"`
						Plan          catalogScanPlan
					}
					require.NoError(t, json.Unmarshal(result[0]["QUERY PLAN"], &plan))
					require.Less(t, plan[0].ExecutionTime, 5000.0, "query exceeds the API's five-second budget")
					if query == "" {
						assertCatalogDefaultScans(t, plan[0].Plan)
					}
					t.Logf("scale=%d query=%q iteration=%d executionMs=%.3f", scale, query, iteration, plan[0].ExecutionTime)
				}
			}
			applied, err := UpsertRinspaceRepositoryPresentation(t.Context(), &RinspaceRepositoryPresentation{RepoID: 25, Title: "更新后的标签标题", ContentType: RinspaceContentTagWiki, Version: 2}, nil)
			require.NoError(t, err)
			require.True(t, applied)
			updated, err := FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: actor, Query: "更新后", Limit: 24})
			require.NoError(t, err)
			require.Len(t, updated.Repositories, 1, "title index must update with the presentation")
			old, err := FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: actor, Query: "quantum", Limit: 24})
			require.NoError(t, err)
			require.Empty(t, old.Repositories)
			for _, query := range []string{"tags/89", "tags/97"} {
				page, err := FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: actor, Query: query, Limit: 24})
				require.NoError(t, err)
				require.Empty(t, page.Repositories, "archived/private shared tags are not searchable")
			}
			org := &user_model.User{ID: 4, Name: "private-org", LowerName: "private-org", Email: "private-org@example.test", IsActive: true, Type: user_model.UserTypeOrganization, Visibility: structs.VisibleTypePrivate}
			_, err = engine.Insert(org)
			require.NoError(t, err)
			for _, statement := range []string{
				fmt.Sprintf(`INSERT INTO repository(id,owner_id,owner_name,lower_name,name,default_branch,is_private,is_archived) VALUES
(%d,4,'private-org','team-code','team-code','main',true,false),
(%d,4,'private-org','team-issues','team-issues','main',true,false),
(%d,4,'private-org','org-public','org-public','main',false,false)`, scale+301, scale+302, scale+303),
				fmt.Sprintf(`INSERT INTO repo_unit(repo_id,type) SELECT id,%d FROM repository WHERE owner_id=4`, unit.TypeCode),
				`INSERT INTO org_user(org_id,uid,is_public) VALUES(4,2,false)`,
				`INSERT INTO team(id,authorize,org_id) VALUES(10,1,4),(11,1,4)`,
				`INSERT INTO team_user(team_id,uid) VALUES(10,2),(11,2)`,
				fmt.Sprintf(`INSERT INTO team_repo(team_id,repo_id) VALUES(10,%d),(11,%d)`, scale+301, scale+302),
				fmt.Sprintf(`INSERT INTO team_unit(team_id,type,access_mode) VALUES(10,%d,1),(11,%d,1)`, unit.TypeCode, unit.TypeIssues),
			} {
				_, err = engine.Exec(statement)
				require.NoError(t, err)
			}
			for _, item := range []struct {
				query      string
				restricted bool
				publicOnly bool
				want       int
			}{
				{"team-code", false, false, 1},
				{"team-issues", false, false, 0},
				{"org-public", false, false, 1},
				{"org-public", true, false, 0},
				{"team-code", true, false, 1},
				{"team-code", false, true, 0},
				{"org-public", false, true, 0},
			} {
				copyActor := *actor
				copyActor.IsRestricted = item.restricted
				page, err := FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: &copyActor, Query: item.query, PublicOnly: item.publicOnly, Limit: 24})
				require.NoError(t, err)
				require.Len(t, page.Repositories, item.want, "%+v", item)
			}
			_, err = engine.ID(org.ID).Delete(new(user_model.User))
			require.NoError(t, err)
			_, err = engine.Exec(`DROP INDEX rinspace_catalog_model_test.rinspace_editor_name_tokens`)
			require.NoError(t, err)
			_, err = FindRinspaceEditorProjects(t.Context(), RinspaceEditorProjectOptions{Actor: actor, Limit: 24})
			require.ErrorIs(t, err, ErrRinspaceEditorCatalogUnavailable, "missing index must not start a fallback scan")
			_, err = engine.Exec("SET statement_timeout TO '180s'")
			require.NoError(t, err)
			require.NoError(t, v1_27.AddRinspaceEditorCatalogIndexes(engine), "retry must repair a missing index")
		})
	}
}

type catalogScanPlan struct {
	Relation string            `json:"Relation Name"`
	Rows     float64           `json:"Actual Rows"`
	Removed  float64           `json:"Rows Removed by Filter"`
	Loops    float64           `json:"Actual Loops"`
	Plans    []catalogScanPlan `json:"Plans"`
}

func assertCatalogDefaultScans(t *testing.T, plan catalogScanPlan) {
	t.Helper()
	switch plan.Relation {
	case "repository", "collaboration", "access":
		require.Less(t, (plan.Rows+plan.Removed)*plan.Loops, 1000.0, "default catalog must not scan all tag rows: %+v", plan)
	}
	for _, child := range plan.Plans {
		assertCatalogDefaultScans(t, child)
	}
}
