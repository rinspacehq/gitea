// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build rinspace_catalog_postgres

package integration

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/models/migrations/v1_27"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/rinspacecatalog"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

type editorCatalogAPIPage struct {
	SchemaVersion string `json:"schemaVersion"`
	Account       struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"account"`
	Projects []struct {
		ID                    int64           `json:"id"`
		FullName              string          `json:"full_name"`
		Title                 string          `json:"rinspace_title"`
		ContentType           string          `json:"rinspace_content_type"`
		Permissions           *api.Permission `json:"permissions"`
		RinspaceContentOwners []int64         `json:"rinspace_content_owner_ids"`
	} `json:"projects"`
	Query      string `json:"query"`
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"hasMore"`
	NextCursor string `json:"nextCursor"`
}

// Exercise the installed router and token middleware, not a mocked APIContext.
// The integration launcher must provide a disposable PostgreSQL test database.
func TestAPIRinspaceEditorCatalog(t *testing.T) {
	require.True(t, setting.Database.Type.IsPostgreSQL(), "this opt-in suite requires PostgreSQL")
	defer tests.PrepareTestEnv(t)()
	previous := setting.API.RinspaceEditorCatalogEnabled
	setting.API.RinspaceEditorCatalogEnabled = true
	t.Cleanup(func() { setting.API.RinspaceEditorCatalogEnabled = previous })
	t.Run("schema-remains-compatible-with-orm", func(t *testing.T) {
		for range 2 {
			require.NoError(t, db.SyncAllTables())
			_, err := db.GetXORMEngineForTesting().DBMetas()
			require.NoError(t, err)
		}
		var indexes struct{ Total int64 }
		_, err := db.GetEngine(t.Context()).SQL(`SELECT count(*) AS total FROM pg_index i
JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname=current_schema() AND c.relname LIKE 'rinspace_editor_%' AND i.indisvalid AND i.indisready`).Get(&indexes)
		require.NoError(t, err)
		require.EqualValues(t, 6, indexes.Total, "ORM compatibility must preserve the real indexes")
	})

	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	tags := &user_model.User{ID: 600001, Name: "tags", LowerName: "tags", Email: "tags@catalog.example.test", Type: user_model.UserTypeOrganization, IsActive: true}
	_, err := db.GetEngine(t.Context()).Insert(tags)
	require.NoError(t, err)
	insertRepo := func(id int64, owner *user_model.User, name string, private, archived, code bool) *repo_model.Repository {
		t.Helper()
		repository := &repo_model.Repository{
			ID: id, OwnerID: owner.ID, OwnerName: owner.Name,
			Name: name, LowerName: strings.ToLower(name), DefaultBranch: "main", ObjectFormatName: "sha1", IsPrivate: private, IsArchived: archived,
		}
		_, err := db.GetEngine(t.Context()).Insert(repository)
		require.NoError(t, err)
		if code {
			_, err = db.GetEngine(t.Context()).Insert(&repo_model.RepoUnit{RepoID: id, Type: unit.TypeCode, Config: new(repo_model.UnitConfig)})
			require.NoError(t, err)
		}
		return repository
	}
	collaborate := func(repository *repo_model.Repository, mode perm.AccessMode) {
		t.Helper()
		_, err := db.GetEngine(t.Context()).Insert(&repo_model.Collaboration{RepoID: repository.ID, UserID: actor.ID, Mode: mode},
			&access_model.Access{RepoID: repository.ID, UserID: actor.ID, Mode: mode})
		require.NoError(t, err)
	}
	present := func(repository *repo_model.Repository, title, contentType string, owners []int64) {
		t.Helper()
		applied, err := repo_model.UpsertRinspaceRepositoryPresentation(t.Context(), &repo_model.RinspaceRepositoryPresentation{
			RepoID: repository.ID, Title: title, ContentType: contentType, Version: 1,
		}, owners)
		require.NoError(t, err)
		require.True(t, applied)
	}
	owned := insertRepo(700101, actor, "catalog-owned", true, false, true)
	read := insertRepo(700102, owner, "catalog-read", true, false, true)
	write := insertRepo(700103, owner, "catalog-write", true, false, true)
	collaborate(read, perm.AccessModeRead)
	collaborate(write, perm.AccessModeWrite)
	insertRepo(700104, owner, "catalog-hidden", true, false, true)
	projected := insertRepo(700105, owner, "catalog-projected", true, false, true)
	present(projected, "Hidden ownership alone", repo_model.RinspaceContentBook, []int64{actor.ID})
	insertRepo(700106, actor, "catalog-no-code", false, false, false)
	otherTag := insertRepo(700107, actor, "catalog-not-canonical", false, false, true)
	present(otherTag, "Noncanonical tag", repo_model.RinspaceContentTagWiki, nil)
	publicOwned := insertRepo(700108, actor, "catalog-public", false, false, true)
	general := insertRepo(700125, tags, "25", false, false, true)
	present(general, "General 数学 quantum", repo_model.RinspaceContentTagWiki, nil)
	missingProjection := insertRepo(700142, tags, "42", false, false, true)
	insertRepo(700189, tags, "89", false, true, true)
	insertRepo(700197, tags, "97", true, false, true)
	org := &user_model.User{
		ID: 600002, Name: "catalog-org", LowerName: "catalog-org", Email: "org@catalog.example.test",
		Type: user_model.UserTypeOrganization, IsActive: true, Visibility: api.VisibleTypePrivate,
	}
	_, err = db.GetEngine(t.Context()).Insert(org)
	require.NoError(t, err)
	teamCode := insertRepo(700201, org, "teamcode", true, false, true)
	teamIssues := insertRepo(700202, org, "teamissues", true, false, true)
	orgPublic := insertRepo(700203, org, "orgpublic", false, false, true)
	_, err = db.GetEngine(t.Context()).Insert(&organization.OrgUser{OrgID: org.ID, UID: actor.ID},
		&organization.Team{ID: 601001, OrgID: org.ID, Name: "Code", LowerName: "code", AccessMode: perm.AccessModeRead},
		&organization.Team{ID: 601002, OrgID: org.ID, Name: "Issues", LowerName: "issues", AccessMode: perm.AccessModeRead},
		&organization.TeamUser{OrgID: org.ID, TeamID: 601001, UID: actor.ID},
		&organization.TeamUser{OrgID: org.ID, TeamID: 601002, UID: actor.ID},
		&organization.TeamRepo{OrgID: org.ID, TeamID: 601001, RepoID: teamCode.ID},
		&organization.TeamRepo{OrgID: org.ID, TeamID: 601002, RepoID: teamIssues.ID},
		&organization.TeamUnit{OrgID: org.ID, TeamID: 601001, Type: unit.TypeCode, AccessMode: perm.AccessModeRead},
		&organization.TeamUnit{OrgID: org.ID, TeamID: 601002, Type: unit.TypeIssues, AccessMode: perm.AccessModeRead},
		&access_model.Access{RepoID: teamCode.ID, UserID: actor.ID, Mode: perm.AccessModeRead},
		&access_model.Access{RepoID: teamIssues.ID, UserID: actor.ID, Mode: perm.AccessModeRead})
	require.NoError(t, err)

	session := loginUser(t, actor.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository)
	publicToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository, auth_model.AccessTokenScopePublicOnly)
	userOnly := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadUser)
	repositoryOnly := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
	const endpoint = "/api/v1/user/rinspace-editor/projects"
	requestPage := func(t *testing.T, query, token string, expected int) *editorCatalogAPIPage {
		t.Helper()
		response := MakeRequest(t, NewRequest(t, http.MethodGet, endpoint+query).AddTokenAuth(token), expected)
		if expected != http.StatusOK {
			return nil
		}
		require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
		page := DecodeJSON(t, response, &editorCatalogAPIPage{})
		require.Equal(t, rinspacecatalog.SchemaVersion, page.SchemaVersion)
		require.Equal(t, actor.ID, page.Account.ID)
		require.Equal(t, actor.Name, page.Account.Login)
		return page
	}

	t.Run("authentication-and-scopes", func(t *testing.T) {
		MakeRequest(t, NewRequest(t, http.MethodGet, endpoint), http.StatusUnauthorized)
		requestPage(t, "", "not-a-valid-token", http.StatusUnauthorized)
		requestPage(t, "", userOnly, http.StatusForbidden)
		requestPage(t, "", repositoryOnly, http.StatusForbidden)
	})
	t.Run("default-hidden-tags-and-real-permissions", func(t *testing.T) {
		page := requestPage(t, "?q=catalog-&limit=50", token, http.StatusOK)
		require.Len(t, page.Projects, 4)
		permissions := map[int64]*api.Permission{}
		for _, project := range page.Projects {
			permissions[project.ID] = project.Permissions
		}
		require.Equal(t, &api.Permission{Pull: true, Push: true, Admin: true}, permissions[owned.ID])
		require.Equal(t, &api.Permission{Pull: true}, permissions[read.ID])
		require.Equal(t, &api.Permission{Pull: true, Push: true}, permissions[write.ID])
		require.Contains(t, permissions, publicOwned.ID)
		page = requestPage(t, "?limit=50", token, http.StatusOK)
		for _, project := range page.Projects {
			require.False(t, strings.HasPrefix(project.FullName, "tags/"))
			require.NotEqual(t, repo_model.RinspaceContentTagWiki, project.ContentType)
		}
	})
	t.Run("all-shared-tags-searchable-without-grant", func(t *testing.T) {
		for query, id := range map[string]int64{"General": general.ID, "数学": general.ID, "tags/25": general.ID, "tags/42": missingProjection.ID} {
			page := requestPage(t, "?q="+url.QueryEscape(query), token, http.StatusOK)
			require.Len(t, page.Projects, 1, query)
			require.Equal(t, id, page.Projects[0].ID)
			require.Equal(t, &api.Permission{Pull: true}, page.Projects[0].Permissions)
			if id == general.ID {
				require.Equal(t, "General 数学 quantum", page.Projects[0].Title)
				require.Equal(t, repo_model.RinspaceContentTagWiki, page.Projects[0].ContentType)
			}
		}
		for _, query := range []string{"tags/89", "tags/97", "catalog-hidden", "catalog-projected", "catalog-no-code", "Noncanonical", "%"} {
			require.Empty(t, requestPage(t, "?q="+url.QueryEscape(query), token, http.StatusOK).Projects, query)
		}
		unittest.AssertNotExistsBean(t, &repo_model.Collaboration{RepoID: general.ID, UserID: actor.ID})
		unittest.AssertNotExistsBean(t, &access_model.Access{RepoID: general.ID, UserID: actor.ID})
	})
	t.Run("public-only-token", func(t *testing.T) {
		page := requestPage(t, "?q=catalog-&limit=50", publicToken, http.StatusOK)
		require.Len(t, page.Projects, 1)
		require.Equal(t, publicOwned.ID, page.Projects[0].ID)
		require.Len(t, requestPage(t, "?q=General", publicToken, http.StatusOK).Projects, 1)
		require.Empty(t, requestPage(t, "?q=orgpublic", publicToken, http.StatusOK).Projects)
	})
	t.Run("team-code-unit-and-organization-visibility", func(t *testing.T) {
		page := requestPage(t, "?q=teamcode", token, http.StatusOK)
		require.Len(t, page.Projects, 1)
		require.Equal(t, teamCode.ID, page.Projects[0].ID)
		require.Equal(t, &api.Permission{Pull: true}, page.Projects[0].Permissions)
		require.Empty(t, requestPage(t, "?q=teamissues", token, http.StatusOK).Projects)
		page = requestPage(t, "?q=orgpublic", token, http.StatusOK)
		require.Len(t, page.Projects, 1)
		require.Equal(t, orgPublic.ID, page.Projects[0].ID)
	})
	t.Run("cursor-covers-every-readable-project", func(t *testing.T) {
		seen := map[int64]bool{}
		cursor := ""
		for pageNumber := 0; pageNumber < 5; pageNumber++ {
			query := "?q=catalog-&limit=1"
			if cursor != "" {
				query += "&before_id=" + cursor
			}
			page := requestPage(t, query, token, http.StatusOK)
			require.Len(t, page.Projects, 1)
			id := page.Projects[0].ID
			require.False(t, seen[id], "duplicate project across cursor pages")
			seen[id] = true
			if !page.HasMore {
				require.Empty(t, page.NextCursor)
				break
			}
			require.Equal(t, strconv.FormatInt(id, 10), page.NextCursor)
			cursor = page.NextCursor
		}
		require.Len(t, seen, 4)
	})
	t.Run("invalid-query-and-disabled-feature", func(t *testing.T) {
		for _, query := range []string{"limit=0", "limit=51", "before_id=025", "before_id=-1", "q=%FF", "q=line%0Abreak"} {
			requestPage(t, "?"+query, token, http.StatusBadRequest)
		}
		setting.API.RinspaceEditorCatalogEnabled = false
		t.Cleanup(func() { setting.API.RinspaceEditorCatalogEnabled = true })
		requestPage(t, "", token, http.StatusNotFound)
	})
	t.Run("permission-changes-are-rechecked", func(t *testing.T) {
		for _, bean := range []any{&repo_model.Collaboration{Mode: perm.AccessModeRead}, &access_model.Access{Mode: perm.AccessModeRead}} {
			_, err := db.GetEngine(t.Context()).Where("repo_id=? AND user_id=?", write.ID, actor.ID).Cols("mode").Update(bean)
			require.NoError(t, err)
		}
		page := requestPage(t, "?q=catalog-write", token, http.StatusOK)
		require.Len(t, page.Projects, 1)
		require.Equal(t, &api.Permission{Pull: true}, page.Projects[0].Permissions)
		_, err := db.GetEngine(t.Context()).Delete(&repo_model.Collaboration{RepoID: read.ID, UserID: actor.ID})
		require.NoError(t, err)
		_, err = db.GetEngine(t.Context()).Delete(&access_model.Access{RepoID: read.ID, UserID: actor.ID})
		require.NoError(t, err)
		require.Empty(t, requestPage(t, "?q=catalog-read", token, http.StatusOK).Projects)
	})
	t.Run("prohibited-account-is-rejected", func(t *testing.T) {
		_, err := db.GetEngine(t.Context()).ID(actor.ID).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := db.GetEngine(context.WithoutCancel(t.Context())).ID(actor.ID).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: actor.ProhibitLogin})
			require.NoError(t, err)
		})
		requestPage(t, "", token, http.StatusForbidden)
	})
	t.Run("missing-index-fails-closed-without-get-ddl", func(t *testing.T) {
		engine := db.GetXORMEngineForTesting()
		_, err := engine.Exec("DROP INDEX rinspace_editor_name_tokens")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, v1_27.AddRinspaceEditorCatalogIndexes(engine)) })
		requestPage(t, "?q=General", token, http.StatusServiceUnavailable)
		var count struct{ Total int64 }
		_, err = engine.SQL("SELECT count(*) AS total FROM pg_indexes WHERE schemaname=current_schema() AND indexname='rinspace_editor_name_tokens'").Get(&count)
		require.NoError(t, err)
		require.Zero(t, count.Total, "GET must not repair missing schema")
		require.NoError(t, v1_27.AddRinspaceEditorCatalogIndexes(engine))
		requestPage(t, "?q=General", token, http.StatusOK)
	})
}
