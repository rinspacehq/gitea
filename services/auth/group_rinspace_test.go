// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/reqctx"
	"gitea.dev/modules/session"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errParentRevoked = errors.New("rinspace parent authorization revoked")

type rinspaceTestMethod struct {
	name   string
	user   *user_model.User
	err    error
	called int
}

func (m *rinspaceTestMethod) Name() string { return m.name }

func (m *rinspaceTestMethod) Verify(*http.Request, http.ResponseWriter, DataStore, SessionStore) (*user_model.User, error) {
	m.called++
	return m.user, m.err
}

type rinspaceTestGate struct {
	err        error
	credential Credential
	calls      int
}

func (g *rinspaceTestGate) Verify(_ context.Context, _ *user_model.User, credential Credential) error {
	g.calls++
	g.credential = credential
	return g.err
}

func TestRinspaceInvalidAuthorizationDoesNotFallBackToAmbientLogin(t *testing.T) {
	tokenErr := errors.New("invalid bearer token")
	token := &rinspaceTestMethod{name: "oauth2", err: tokenErr}
	session := &rinspaceTestMethod{name: "session", user: &user_model.User{ID: 7}}
	group := NewGroup(token, session)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
	request.Header.Set("Authorization", "Bearer invalid")

	user, err := group.Verify(request, httptest.NewRecorder(), reqctx.ContextData{}, nil)
	require.ErrorIs(t, err, tokenErr)
	assert.Nil(t, user)
	assert.Equal(t, 0, session.called)
}

func TestRinspaceMalformedAuthorizationDoesNotFallBackToReverseProxy(t *testing.T) {
	token := &rinspaceTestMethod{name: "oauth2"}
	reverseProxy := &rinspaceTestMethod{name: ReverseProxyMethodName, user: &user_model.User{ID: 7}}
	group := NewGroup(token, reverseProxy)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
	request.Header.Set("Authorization", "not-a-supported-scheme")

	user, err := group.Verify(request, httptest.NewRecorder(), reqctx.ContextData{}, nil)
	require.ErrorIs(t, err, ErrExplicitCredentialRejected)
	assert.Nil(t, user)
	assert.Equal(t, 0, reverseProxy.called)
}

func TestRinspaceAuthorizationHeaderDoesNotDisableAmbientOnlyGroup(t *testing.T) {
	session := &rinspaceTestMethod{name: "session", user: &user_model.User{ID: 7}}
	group := NewGroup(session)
	request := httptest.NewRequest(http.MethodGet, "/actions/jobs/42/logs", nil)
	request.Header.Set("Authorization", "Basic ignored-by-this-router")

	user, err := group.Verify(request, httptest.NewRecorder(), reqctx.ContextData{}, nil)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.EqualValues(t, 7, user.ID)
	assert.Equal(t, 1, session.called)
}

func TestRinspaceProtocolCredentialIgnoredByIncompatibleMethodAllowsAmbientLogin(t *testing.T) {
	oauth := &rinspaceTestMethod{name: "oauth2"}
	session := &rinspaceTestMethod{name: "session", user: &user_model.User{ID: 7}}
	group := NewGroup(oauth, session)
	request := httptest.NewRequest(http.MethodPost, "/login/oauth/access_token", nil)
	request.SetBasicAuth("oauth-client", "oauth-secret")

	user, err := group.Verify(request, httptest.NewRecorder(), reqctx.ContextData{}, nil)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.EqualValues(t, 7, user.ID)
	assert.Equal(t, 1, oauth.called)
	assert.Equal(t, 1, session.called)
}

func TestRinspaceCredentialGateCoversSessionAndTokenMethods(t *testing.T) {
	for _, methodName := range []string{"session", "oauth2", ReverseProxyMethodName} {
		t.Run(methodName, func(t *testing.T) {
			method := &rinspaceTestMethod{name: methodName, user: &user_model.User{ID: 7}}
			gate := &rinspaceTestGate{err: errParentRevoked}
			group := NewGroup(method)
			group.SetCredentialGate(gate)

			store := reqctx.ContextData{}
			var sessionStore SessionStore
			if isAmbientMethod(methodName) {
				sessionStore = session.NewMockMemStore("gitea-session-42")
				require.NoError(t, sessionStore.Set("uid", int64(7)))
			} else {
				store[CredentialIDDataKey] = "token-42"
			}
			user, err := group.Verify(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder(), store, sessionStore)
			require.ErrorIs(t, err, errParentRevoked)
			assert.Nil(t, user)
			assert.Equal(t, 1, gate.calls)
			assert.Equal(t, methodName, gate.credential.Method)
			if isAmbientMethod(methodName) {
				assert.Equal(t, "gitea-session-42", gate.credential.ID)
				assert.Nil(t, sessionStore.Get("uid"), "rejected parent left an ambient local session behind")
			} else {
				assert.Equal(t, "token-42", gate.credential.ID)
			}
		})
	}
}

func TestRinspaceCredentialGateRepresentsSSHKeyByID(t *testing.T) {
	gate := &rinspaceTestGate{err: errParentRevoked}
	err := gate.Verify(t.Context(), &user_model.User{ID: 7}, Credential{Method: "ssh", ID: "key-42"})
	require.ErrorIs(t, err, errParentRevoked)
	assert.Equal(t, Credential{Method: "ssh", ID: "key-42"}, gate.credential)
}

func TestRinspaceCredentialGateAllowsCurrentParent(t *testing.T) {
	method := &rinspaceTestMethod{name: "oauth2", user: &user_model.User{ID: 7}}
	gate := &rinspaceTestGate{}
	group := NewGroup(method)
	group.SetCredentialGate(gate)

	user, err := group.Verify(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder(), reqctx.ContextData{}, nil)
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.EqualValues(t, 7, user.ID)
	assert.Equal(t, 1, gate.calls)
}
