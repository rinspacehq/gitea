// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/auth/httpauth"
)

var ErrExplicitCredentialRejected = errors.New("explicit authorization credential was rejected")

const CredentialIDDataKey = "RinspaceCredentialID"

type Credential struct {
	Method        string
	ID            string
	UID           string
	SID           string
	BindingID     string
	RuntimeHandle string
	Version       int64
}

// CredentialGate performs a final authorization check after a Gitea credential identifies a user.
// Rinspace uses it to verify the parent session or personal credential epoch before granting access.
type CredentialGate interface {
	Verify(ctx context.Context, user *user_model.User, credential Credential) error
}

// Ensure the struct implements the interface.
var (
	_ Method = &Group{}
)

// Group implements the Auth interface with serval Auth.
type Group struct {
	methods []Method
	gate    CredentialGate
}

// SetCredentialGate sets the final authorization check for all methods in this group.
func (b *Group) SetCredentialGate(gate CredentialGate) {
	b.gate = gate
}

// NewGroup creates a new auth group
func NewGroup(methods ...Method) *Group {
	return &Group{
		methods: methods,
		gate:    DefaultRinspaceCredentialGate(),
	}
}

// Add adds a new method to group
func (b *Group) Add(method Method) {
	b.methods = append(b.methods, method)
}

// Name returns group's methods name
func (b *Group) Name() string {
	names := make([]string, 0, len(b.methods))
	for _, m := range b.methods {
		names = append(names, m.Name())
	}
	return strings.Join(names, ",")
}

func (b *Group) Verify(req *http.Request, w http.ResponseWriter, store DataStore, sess SessionStore) (*user_model.User, error) {
	// Try to sign in with each of the enabled plugins
	var retErr error
	authorization := strings.TrimSpace(req.Header.Get("Authorization"))
	explicitCredential := authorization != ""
	_, explicitCredentialParsed := httpauth.ParseAuthorizationHeader(authorization)
	explicitMethodAttempted := false
	for _, m := range b.methods {
		if explicitCredential && isAmbientMethod(m.Name()) && explicitMethodAttempted {
			if retErr != nil {
				return nil, retErr
			}
			return nil, ErrExplicitCredentialRejected
		}
		user, err := m.Verify(req, w, store, sess)
		if explicitCredential && !isAmbientMethod(m.Name()) && (!explicitCredentialParsed || user != nil || err != nil) {
			// A valid Authorization scheme may belong to a protocol-specific
			// handler instead of this method (for example, an LFS bearer token
			// reaching Basic auth or OAuth client Basic auth reaching OAuth2).
			// Count the method only when it actually accepted or rejected the
			// credential. Malformed headers still block ambient fallback.
			explicitMethodAttempted = true
		}
		if err != nil {
			if retErr == nil {
				retErr = err
			}
			// Try other methods if this one failed.
			// Some methods may share the same protocol to detect if they are matched.
			// For example, OAuth2 and conan.Auth both read token from "Authorization: Bearer <token>" header,
			// If OAuth2 returns error, we should give conan.Auth a chance to try.
			continue
		}

		// If any method returns a user, we can stop trying.
		// Return the user and ignore any error returned by previous methods.
		if user != nil {
			if b.gate != nil {
				if err := b.gate.Verify(req.Context(), user, credentialFromRequest(req, m.Name(), store, sess)); err != nil {
					// Reverse-proxy authentication may have just created or refreshed a
					// local Gitea session before the Rinspace parent check runs.  Do not
					// leave that ambient state behind when the final gate rejects it.
					if sess != nil && isAmbientMethod(m.Name()) {
						_ = sess.Flush()
						_ = sess.Destroy(w, req)
					}
					return nil, err
				}
			}
			if store.GetData()["AuthedMethod"] == nil {
				store.GetData()["AuthedMethod"] = m.Name()
			}
			return user, nil
		}
	}

	// If no method returns a user, return the error returned by the first method.
	return nil, retErr
}

func isAmbientMethod(name string) bool {
	return name == "session" || name == ReverseProxyMethodName || name == "sspi"
}

func credentialFromRequest(req *http.Request, method string, store DataStore, sess SessionStore) Credential {
	credential := Credential{Method: method}
	if value, ok := store.GetData()["LoginMethod"].(string); ok && value != "" {
		credential.Method = value
	}
	if value, ok := store.GetData()[CredentialIDDataKey].(string); ok {
		credential.ID = value
	}
	if credential.ID == "" && sess != nil && isAmbientMethod(method) {
		credential.ID = sess.ID()
	}
	if method == ReverseProxyMethodName {
		credential.UID = strings.TrimSpace(req.Header.Get("X-Rin-UID"))
		credential.SID = strings.TrimSpace(req.Header.Get("X-Rin-Parent-SID"))
		credential.BindingID = strings.TrimSpace(req.Header.Get("X-Rin-Binding-ID"))
		credential.RuntimeHandle = strings.TrimSpace(req.Header.Get("X-Rin-Runtime-Handle"))
		credential.Version, _ = strconv.ParseInt(strings.TrimSpace(req.Header.Get("X-Rin-Parent-Version")), 10, 64)
	}
	return credential
}
