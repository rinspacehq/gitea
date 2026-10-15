// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build rinspace_catalog_postgres

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

type catalogAPIPerformanceSample struct {
	Scale        int     `json:"scale"`
	Case         string  `json:"case"`
	State        string  `json:"state"`
	Iteration    int     `json:"iteration"`
	ColdGate     int     `json:"coldGate,omitempty"`
	Milliseconds float64 `json:"milliseconds"`
	Rows         int     `json:"rows"`
	ResponseSize int     `json:"responseBytes"`
}

// The external launcher restarts only its disposable PostgreSQL and verifies
// eviction of catalog data files, never the host's global page cache.
func catalogAPIAwaitCold(t *testing.T, directory string, sequence int, label string) {
	t.Helper()
	engine := db.GetXORMEngineForTesting()
	engine.SetMaxIdleConns(0)
	t.Cleanup(func() { engine.SetMaxIdleConns(setting.Database.MaxIdleConns) })
	stem := filepath.Join(directory, fmt.Sprintf("%03d", sequence))
	require.NoFileExists(t, stem+".request")
	require.NoFileExists(t, stem+".ready")
	require.NoError(t, os.WriteFile(stem+".request", []byte(label), 0o600))
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := os.ReadFile(stem + ".ready")
		if err == nil {
			require.Equal(t, "ok\n", string(ready), "cold launcher must verify its restart and file residency")
			engine.SetMaxIdleConns(setting.Database.MaxIdleConns)
			return
		}
		require.True(t, os.IsNotExist(err), "cold gate read failed: %v", err)
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("disposable PostgreSQL cold gate timed out")
		case <-t.Context().Done():
			t.Fatal("performance test canceled while waiting for cold gate")
		}
	}
}

func TestAPIRinspaceEditorCatalogPerformance(t *testing.T) {
	if os.Getenv("RINSPACE_CATALOG_PERFORMANCE_TEST") != "1" {
		t.Skip("explicit disposable-database performance launcher required")
	}
	require.True(t, setting.Database.Type.IsPostgreSQL())
	engine := db.GetXORMEngineForTesting()
	var database struct{ Name string }
	_, err := engine.SQL("SELECT current_database() AS name").Get(&database)
	require.NoError(t, err)
	require.Equal(t, "editor_catalog_api_test", database.Name)
	gateDir, reportDir := os.Getenv("RINSPACE_CATALOG_COLD_GATE_DIR"), os.Getenv("RINSPACE_CATALOG_REPORT_DIR")
	require.True(t, filepath.IsAbs(gateDir) && filepath.IsAbs(reportDir), "explicit absolute fixture directories required")
	file, err := os.OpenFile(filepath.Join(reportDir, "requests.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	encoder := json.NewEncoder(file)
	previous := setting.API.RinspaceEditorCatalogEnabled
	setting.API.RinspaceEditorCatalogEnabled = true
	t.Cleanup(func() {
		setting.API.RinspaceEditorCatalogEnabled = previous
		engine.SetMaxOpenConns(setting.Database.MaxOpenConns)
		engine.SetMaxIdleConns(setting.Database.MaxIdleConns)
	})
	sequence := 0
	for _, scale := range []int{10000, 100000} {
		t.Run(strconv.Itoa(scale), func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()
			actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			_, err := engine.Insert(&user_model.User{ID: 600001, Name: "tags", LowerName: "tags", Email: "tags@catalog-perf.example.test", Type: user_model.UserTypeOrganization, IsActive: true})
			require.NoError(t, err)
			engine.SetMaxOpenConns(1)
			engine.SetMaxIdleConns(1)
			_, err = engine.Exec("SET statement_timeout TO '180s'")
			require.NoError(t, err)
			start := time.Now()
			for _, statement := range []string{
				`INSERT INTO repository(id,owner_id,owner_name,lower_name,name,default_branch,is_private,is_archived,is_empty)
SELECT 1000000+n,2,'user2','benchproject-'||n,'benchproject-'||n,'main',true,false,false FROM generate_series(1,200) n`,
				fmt.Sprintf(`INSERT INTO repository(id,owner_id,owner_name,lower_name,name,default_branch,is_private,is_archived,is_empty)
SELECT 1000200+n,600001,'tags',n::text,n::text,'main',n%%97=0,n%%89=0,false FROM generate_series(1,%d) n`, scale),
				`INSERT INTO repo_unit(repo_id,type,config) SELECT id,1,'{}' FROM repository WHERE id>1000000`,
				`INSERT INTO rinspace_repository_presentation(repo_id,title,content_type,version)
SELECT id,CASE WHEN name='25' THEN 'General 数学 quantum' ELSE '数学理论 标签 '||name END,'tag-wiki',1 FROM repository WHERE owner_id=600001 AND name<>'42'`,
				`INSERT INTO access(repo_id,user_id,mode) SELECT id,2,2 FROM repository WHERE owner_id=600001 AND name<>'25'`,
				`INSERT INTO collaboration(repo_id,user_id,mode) SELECT id,2,2 FROM repository WHERE owner_id=600001 AND name<>'25'`,
			} {
				_, err := engine.Exec(statement)
				require.NoError(t, err)
			}
			for _, table := range []string{"repository", "repo_unit", "access", "collaboration", "rinspace_repository_presentation"} {
				// VACUUM changes no fixture rows; its SQL is unsupported by the tracking hook.
				_, err := engine.DB().DB.ExecContext(t.Context(), "VACUUM ANALYZE "+table)
				require.NoError(t, err)
			}
			t.Logf("scale=%d fixtureAndMaintenanceMs=%d", scale, time.Since(start).Milliseconds())
			_, err = engine.Exec("SET statement_timeout TO '5s'")
			require.NoError(t, err)
			engine.SetMaxOpenConns(setting.Database.MaxOpenConns)
			engine.SetMaxIdleConns(setting.Database.MaxIdleConns)
			token := getTokenForLoggedInUser(t, loginUser(t, actor.Name), auth_model.AccessTokenScopeReadUser, auth_model.AccessTokenScopeReadRepository)
			for _, item := range []struct {
				name, query string
				before      int
				rows        int
			}{
				{"default", "", 0, 24},
				{"ordinary", "benchproject", 0, 24},
				{"coordinate", "tags/25", 0, 1},
				{"rare-title", "General", 0, 1},
				{"short-chinese", "数", 0, 24},
				{"absent", "稀词", 0, 0},
				{"numeric-name", "1", 0, 24},
				{"literal-percent", "%", 0, 0},
				{"deep", "标签", 1000200 + scale/10, 24},
			} {
				values := url.Values{"q": {item.query}, "limit": {"24"}}
				if item.before > 0 {
					values.Set("before_id", strconv.Itoa(item.before))
				}
				for _, state := range []string{"catalog-pages-cold", "warm"} {
					iterations := 7
					if state == "catalog-pages-cold" {
						iterations = 3
					}
					for iteration := range iterations {
						gate := 0
						if state == "catalog-pages-cold" {
							sequence++
							gate = sequence
							catalogAPIAwaitCold(t, gateDir, gate, fmt.Sprintf("scale=%d case=%s iteration=%d", scale, item.name, iteration))
						}
						start := time.Now()
						response := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/user/rinspace-editor/projects?"+values.Encode()).AddTokenAuth(token), http.StatusOK)
						elapsed := time.Since(start)
						page := DecodeJSON(t, response, &editorCatalogAPIPage{})
						require.Equal(t, actor.ID, page.Account.ID)
						require.Len(t, page.Projects, item.rows)
						require.Less(t, elapsed, 5*time.Second, "complete in-process route exceeds query budget")
						for _, project := range page.Projects {
							if item.name == "default" {
								require.False(t, strings.HasPrefix(project.FullName, "tags/"))
							}
							if item.before > 0 {
								require.Less(t, project.ID, int64(item.before))
							}
							if item.name == "coordinate" || item.name == "rare-title" {
								require.Equal(t, "tags/25", project.FullName)
								require.True(t, project.Permissions.Pull)
								require.False(t, project.Permissions.Push)
							}
						}
						if item.rows == 0 {
							require.False(t, page.HasMore)
						}
						require.NoError(t, encoder.Encode(catalogAPIPerformanceSample{
							Scale: scale, Case: item.name, State: state, Iteration: iteration, ColdGate: gate,
							Milliseconds: float64(elapsed) / float64(time.Millisecond), Rows: len(page.Projects), ResponseSize: response.Body.Len(),
						}))
					}
				}
			}
		})
	}
	require.Equal(t, 54, sequence)
}
