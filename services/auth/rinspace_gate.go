// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	rinauth_model "gitea.dev/models/rinauth"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

var (
	errRinspaceIdentityUnavailable = errors.New("Rinspace Identity is unavailable")
	errRinspaceCredentialRejected  = errors.New("Rinspace credential is no longer active")
)

type rinspaceIdentityPrincipal struct {
	UID            string `json:"uid"`
	SID            string `json:"sid"`
	SessionVersion int64  `json:"sessionVersion"`
}

type rinspaceIntrospectionResult struct {
	Active    bool                       `json:"active"`
	Principal *rinspaceIdentityPrincipal `json:"principal"`
}

type rinspaceAccountStatus struct {
	UID             string `json:"uid"`
	Status          string `json:"status"`
	CredentialEpoch int64  `json:"credentialEpoch"`
}

type rinspaceIdentityClient struct {
	baseURL  *url.URL
	service  string
	audience string
	keyID    string
	secret   []byte
	http     *http.Client
	now      func() time.Time
	random   io.Reader
}

type RinspaceCredentialGate struct {
	client    *rinspaceIdentityClient
	configErr error
	bindUser  func(context.Context, int64, string) error
}

var (
	defaultRinspaceGateOnce sync.Once
	defaultRinspaceGate     CredentialGate
)

func DefaultRinspaceCredentialGate() CredentialGate {
	defaultRinspaceGateOnce.Do(func() {
		if os.Getenv("RINSPACE_IDENTITY_STRICT") != "true" {
			return
		}
		client, err := newRinspaceIdentityClientFromEnv()
		defaultRinspaceGate = &RinspaceCredentialGate{client: client, configErr: err}
	})
	return defaultRinspaceGate
}

func newRinspaceIdentityClientFromEnv() (*rinspaceIdentityClient, error) {
	return newRinspaceIdentityClientFromValues(
		os.Getenv("RINSPACE_IDENTITY_INTERNAL_URL"), os.Getenv("RINSPACE_IDENTITY_SERVICE_ID"),
		os.Getenv("RINSPACE_IDENTITY_AUDIENCE"), os.Getenv("RINSPACE_IDENTITY_SERVICE_KEY_ID"), os.Getenv("RINSPACE_IDENTITY_SERVICE_SECRET"),
	)
}

func newRinspaceIdentityClientFromValues(rawURL, serviceValue, audienceValue, keyIDValue, secretValue string) (*rinspaceIdentityClient, error) {
	base, err := url.Parse(strings.TrimSpace(rawURL))
	secret := []byte(secretValue)
	service := strings.TrimSpace(serviceValue)
	audience := strings.TrimSpace(audienceValue)
	keyID := strings.TrimSpace(keyIDValue)
	if err != nil || !base.IsAbs() || base.User != nil || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") || len(secret) < 32 || service == "" || audience == "" || keyID == "" || len(keyID) > 64 {
		return nil, errRinspaceIdentityUnavailable
	}
	return &rinspaceIdentityClient{baseURL: base, service: service, audience: audience, keyID: keyID, secret: secret, http: &http.Client{Timeout: 3 * time.Second}, now: time.Now, random: rand.Reader}, nil
}

func (gate *RinspaceCredentialGate) Verify(ctx context.Context, user *user_model.User, credential Credential) error {
	if gate == nil || gate.configErr != nil || gate.client == nil {
		return errRinspaceIdentityUnavailable
	}
	if user == nil || !user.IsActive || user.ProhibitLogin {
		return errRinspaceCredentialRejected
	}
	if user.ID == user_model.ActionsUserID {
		return nil
	}
	if credential.Method == ReverseProxyMethodName {
		if credential.UID == "" || credential.SID == "" || credential.BindingID == "" || !strings.HasPrefix(credential.RuntimeHandle, "rin_rh_") || credential.Version < 1 {
			return errRinspaceCredentialRejected
		}
		result, err := gate.client.introspect(ctx, credential.RuntimeHandle)
		if err != nil {
			return err
		}
		if !validRinspacePrincipal(result, credential) {
			if err := gate.client.activate(ctx, credential, fmt.Sprintf("gitea:%d:%s", user.ID, credential.ID)); err != nil {
				return errRinspaceCredentialRejected
			}
			result, err = gate.client.introspect(ctx, credential.RuntimeHandle)
			if err != nil {
				return err
			}
		}
		bindUser := gate.bindUser
		if bindUser == nil {
			bindUser = rinauth_model.UpsertUserIdentity
		}
		if !validRinspacePrincipal(result, credential) || bindUser(ctx, user.ID, credential.UID) != nil {
			return errRinspaceCredentialRejected
		}
		return nil
	}
	if credential.Method == "session" || credential.ID == "" {
		return errRinspaceCredentialRejected
	}
	identity, err := rinauth_model.GetUserIdentity(ctx, user.ID)
	if err != nil {
		return errRinspaceCredentialRejected
	}
	binding, err := rinauth_model.GetCredentialBinding(ctx, credential.ID)
	if err != nil || binding.UserID != user.ID || binding.RinspaceUID != identity.RinspaceUID || binding.State != "active" {
		return errRinspaceCredentialRejected
	}
	account, err := gate.client.accountStatus(ctx, identity.RinspaceUID)
	if err != nil {
		return err
	}
	if account.Status != "active" || account.UID != identity.RinspaceUID || account.CredentialEpoch != binding.IssuedEpoch {
		return errRinspaceCredentialRejected
	}
	if err := rinauth_model.TouchCredentialBinding(ctx, credential.ID); err != nil {
		return errRinspaceIdentityUnavailable
	}
	return nil
}

func validRinspacePrincipal(result rinspaceIntrospectionResult, credential Credential) bool {
	return result.Active && result.Principal != nil && result.Principal.UID == credential.UID && result.Principal.SID == credential.SID && result.Principal.SessionVersion >= credential.Version
}

func RevokeRinspaceWebParent(ctx context.Context, req *http.Request) error {
	gate, ok := DefaultRinspaceCredentialGate().(*RinspaceCredentialGate)
	if !ok || gate.client == nil || gate.configErr != nil {
		return errRinspaceIdentityUnavailable
	}
	credential := Credential{
		UID:       strings.TrimSpace(req.Header.Get("X-Rin-UID")),
		SID:       strings.TrimSpace(req.Header.Get("X-Rin-Parent-SID")),
		BindingID: strings.TrimSpace(req.Header.Get("X-Rin-Binding-ID")),
	}
	if credential.UID == "" || credential.SID == "" || credential.BindingID == "" {
		return errRinspaceCredentialRejected
	}
	return gate.client.revoke(ctx, credential)
}

func AuthorizeRinspacePersonalCredential(ctx context.Context, user *user_model.User, credentialRef string) error {
	gate := DefaultRinspaceCredentialGate()
	if gate == nil {
		return nil
	}
	return gate.Verify(ctx, user, Credential{Method: "personal", ID: credentialRef})
}

func RegisterRinspacePersonalCredential(ctx context.Context, userID int64, credentialRef string) error {
	gate, ok := DefaultRinspaceCredentialGate().(*RinspaceCredentialGate)
	if !ok {
		return nil
	}
	if gate.client == nil || gate.configErr != nil {
		return errRinspaceIdentityUnavailable
	}
	identity, err := rinauth_model.GetUserIdentity(ctx, userID)
	if err != nil {
		return errRinspaceCredentialRejected
	}
	account, err := gate.client.accountStatus(ctx, identity.RinspaceUID)
	if err != nil || account.Status != "active" || account.CredentialEpoch < 1 {
		return errRinspaceCredentialRejected
	}
	kind, _, _ := strings.Cut(credentialRef, ":")
	if kind != "ssh" && kind != "pat" && kind != "oauth" {
		return errRinspaceCredentialRejected
	}
	return rinauth_model.UpsertCredentialBinding(ctx, &rinauth_model.CredentialBinding{
		CredentialRef: credentialRef, UserID: userID, RinspaceUID: identity.RinspaceUID, IssuedEpoch: account.CredentialEpoch, Kind: kind, State: "active",
	})
}

func (client *rinspaceIdentityClient) introspect(ctx context.Context, handle string) (rinspaceIntrospectionResult, error) {
	var result rinspaceIntrospectionResult
	err := client.do(ctx, "/internal/v1/identity/introspect", map[string]any{"runtimeHandle": handle, "audience": client.audience}, http.StatusOK, &result)
	return result, err
}

func (client *rinspaceIdentityClient) accountStatus(ctx context.Context, uid string) (rinspaceAccountStatus, error) {
	var result rinspaceAccountStatus
	path := "/internal/v1/identity/accounts/" + url.PathEscape(uid) + "/status"
	err := client.do(ctx, path, map[string]any{"uid": uid, "audience": client.audience}, http.StatusOK, &result)
	return result, err
}

func (client *rinspaceIdentityClient) activate(ctx context.Context, credential Credential, runtimeRef string) error {
	return client.do(ctx, "/internal/v1/identity/runtime-bindings", map[string]any{
		"operation": "activate", "uid": credential.UID, "runtime": "gitea_web", "bindingId": credential.BindingID,
		"runtimeRef": runtimeRef, "issuedVersion": credential.Version, "audience": client.audience,
	}, http.StatusNoContent, nil)
}

func (client *rinspaceIdentityClient) revoke(ctx context.Context, credential Credential) error {
	return client.do(ctx, "/internal/v1/identity/runtime-bindings", map[string]any{
		"operation": "revoke", "uid": credential.UID, "sid": credential.SID, "runtime": "gitea_web",
		"bindingId": credential.BindingID, "audience": client.audience,
	}, http.StatusNoContent, nil)
}

func (client *rinspaceIdentityClient) do(ctx context.Context, path string, body any, expected int, output any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return errRinspaceIdentityUnavailable
	}
	target := client.baseURL.ResolveReference(&url.URL{Path: strings.TrimRight(client.baseURL.Path, "/") + path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return errRinspaceIdentityUnavailable
	}
	timestamp := strconv.FormatInt(client.now().UTC().Unix(), 10)
	nonceBytes := make([]byte, 18)
	if _, err := io.ReadFull(client.random, nonceBytes); err != nil {
		return errRinspaceIdentityUnavailable
	}
	nonce := hex.EncodeToString(nonceBytes)
	bodyHash := sha256.Sum256(encoded)
	canonical := strings.Join([]string{http.MethodPost, target.RequestURI(), timestamp, nonce, hex.EncodeToString(bodyHash[:]), client.keyID}, "\n")
	mac := hmac.New(sha256.New, client.secret)
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Rin-Service", client.service)
	request.Header.Set("X-Rin-Key-ID", client.keyID)
	request.Header.Set("X-Rin-Timestamp", timestamp)
	request.Header.Set("X-Rin-Nonce", nonce)
	request.Header.Set("X-Rin-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := client.http.Do(request)
	if err != nil {
		return errRinspaceIdentityUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != expected || response.Header.Get("X-Rin-Identity-Version") != "2026-09-12" {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return errRinspaceCredentialRejected
	}
	if output != nil && json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output) != nil {
		return errRinspaceIdentityUnavailable
	}
	return nil
}
