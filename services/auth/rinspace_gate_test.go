// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRinspaceWebBindingActivatesBeforeAuthorization(t *testing.T) {
	active := false
	operations := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		require.NotEmpty(t, request.Header.Get("X-Rin-Signature"))
		require.Equal(t, "current", request.Header.Get("X-Rin-Key-ID"))
		response.Header().Set("X-Rin-Identity-Version", "2026-09-12")
		var body map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		if strings.HasSuffix(request.URL.Path, "/introspect") {
			operations = append(operations, "introspect")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"active":    active,
				"principal": map[string]any{"uid": "uid-7", "sid": "81111111-1111-4111-8111-111111111111", "sessionVersion": 7},
			})
			return
		}
		operations = append(operations, body["operation"].(string))
		active = true
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	base, err := newTestRinspaceClient(server.URL, server.Client())
	require.NoError(t, err)
	gate := &RinspaceCredentialGate{client: base, bindUser: func(context.Context, int64, string) error { return nil }}
	credential := Credential{
		Method: ReverseProxyMethodName, ID: "gitea-session", UID: "uid-7", SID: "81111111-1111-4111-8111-111111111111",
		BindingID: "binding-7", RuntimeHandle: "rin_rh_" + strings.Repeat("h", 32), Version: 7,
	}
	require.NoError(t, gate.Verify(t.Context(), &user_model.User{ID: 42, IsActive: true}, credential))
	assert.Equal(t, []string{"introspect", "activate", "introspect"}, operations)
}

func TestRinspaceLocalSessionCannotBypassParent(t *testing.T) {
	gate := &RinspaceCredentialGate{client: &rinspaceIdentityClient{}}
	err := gate.Verify(t.Context(), &user_model.User{ID: 42, IsActive: true}, Credential{Method: "session", ID: "old-local-session"})
	require.ErrorIs(t, err, errRinspaceCredentialRejected)
}

func TestRinspaceNativeLogoutRevokesExactParent(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Rin-Identity-Version", "2026-09-12")
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := newTestRinspaceClient(server.URL, server.Client())
	require.NoError(t, err)
	require.NoError(t, client.revoke(t.Context(), Credential{UID: "uid-7", SID: "sid-a", BindingID: "binding-a"}))
	assert.Equal(t, map[string]any{
		"operation": "revoke", "uid": "uid-7", "sid": "sid-a", "runtime": "gitea_web",
		"bindingId": "binding-a", "audience": "gitea",
	}, body)
}

func newTestRinspaceClient(rawURL string, httpClient *http.Client) (*rinspaceIdentityClient, error) {
	base, err := newRinspaceIdentityClientFromValues(rawURL, "gitea", "gitea", "current", strings.Repeat("s", 32))
	if err != nil {
		return nil, err
	}
	base.http = httpClient
	base.now = func() time.Time { return time.Unix(1_789_200_000, 0).UTC() }
	base.random = strings.NewReader(strings.Repeat("n", 64))
	return base, nil
}
