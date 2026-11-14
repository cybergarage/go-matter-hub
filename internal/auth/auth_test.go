// Copyright (C) 2026 The homewire Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func request(cookies []*http.Cookie, bearer string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	return r
}

func TestNotRequiredAuthenticatesEveryRequest(t *testing.T) {
	a := NewAuthenticator("", false)
	if !a.Authenticated(request(nil, "")) {
		t.Fatal("request refused although the token is not required")
	}
}

func TestBearerToken(t *testing.T) {
	a := NewAuthenticator("secret", true)
	if a.Authenticated(request(nil, "")) {
		t.Fatal("request without credentials accepted")
	}
	if a.Authenticated(request(nil, "wrong")) {
		t.Fatal("wrong bearer token accepted")
	}
	if !a.Authenticated(request(nil, "secret")) {
		t.Fatal("right bearer token refused")
	}
	// An empty token never matches an empty credential.
	empty := NewAuthenticator("secret", true)
	r := request(nil, "")
	r.Header.Set("Authorization", "Bearer ")
	if empty.Authenticated(r) {
		t.Fatal("empty bearer token accepted")
	}
}

func TestSessionLifecycle(t *testing.T) {
	a := NewAuthenticator("secret", true)
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	w := httptest.NewRecorder()
	if err := a.Login(w, request(nil, ""), "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Login(wrong) = %v", err)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie set for a wrong token")
	}

	w = httptest.NewRecorder()
	if err := a.Login(w, request(nil, ""), "secret"); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookie {
		t.Fatalf("cookies = %v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie attributes: %+v", c)
	}
	if c.Value == "secret" {
		t.Fatal("the cookie carries the access token")
	}
	if !a.Authenticated(request(cookies, "")) {
		t.Fatal("session refused")
	}
	if a.Authenticated(request([]*http.Cookie{{Name: SessionCookie, Value: "forged"}}, "")) {
		t.Fatal("forged session accepted")
	}

	now = now.Add(SessionLifetime)
	if a.Authenticated(request(cookies, "")) {
		t.Fatal("expired session accepted")
	}

	now = now.Add(time.Hour)
	w = httptest.NewRecorder()
	if err := a.Login(w, request(nil, ""), "secret"); err != nil {
		t.Fatal(err)
	}
	cookies = w.Result().Cookies()
	w = httptest.NewRecorder()
	a.Logout(w, request(cookies, ""))
	if a.Authenticated(request(cookies, "")) {
		t.Fatal("session accepted after logout")
	}
	if out := w.Result().Cookies(); len(out) != 1 || out[0].MaxAge >= 0 {
		t.Fatal("logout does not clear the cookie")
	}
}

func TestLoginIsLimitedAfterFailures(t *testing.T) {
	a := NewAuthenticator("secret", true)
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for i := 0; i < maxLoginFailures; i++ {
		if err := a.Login(httptest.NewRecorder(), request(nil, ""), "wrong"); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := a.Login(httptest.NewRecorder(), request(nil, ""), "secret"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("Login after failures = %v", err)
	}
	// A bearer token is not a sign-in attempt and keeps working.
	if !a.Authenticated(request(nil, "secret")) {
		t.Fatal("bearer token refused while sign-in is limited")
	}
	now = now.Add(loginFailureFrame)
	if err := a.Login(httptest.NewRecorder(), request(nil, ""), "secret"); err != nil {
		t.Fatalf("Login after the limit elapsed = %v", err)
	}
}

// A session outlasts a restart of the hub: a new Authenticator for the same
// token accepts it. One for another token, as after the token was
// replaced, does not.
func TestSessionOutlastsARestart(t *testing.T) {
	a := NewAuthenticator("secret", true)
	w := httptest.NewRecorder()
	if err := a.Login(w, request(nil, ""), "secret"); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()

	if !NewAuthenticator("secret", true).Authenticated(request(cookies, "")) {
		t.Fatal("the session is refused after a restart")
	}
	if NewAuthenticator("replaced", true).Authenticated(request(cookies, "")) {
		t.Fatal("the session is accepted after the token was replaced")
	}
}

// A session is refused when any byte of it is changed, as when its expiry
// is pushed back.
func TestSessionIsTamperProof(t *testing.T) {
	a := NewAuthenticator("secret", true)
	value, _, err := a.newSession(time.Now().Add(time.Hour).Truncate(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(value)
	for i := range raw {
		forged := append([]byte(nil), raw...)
		forged[i] ^= 0x01
		c := []*http.Cookie{{Name: SessionCookie, Value: base64.RawURLEncoding.EncodeToString(forged)}}
		if a.Authenticated(request(c, "")) {
			t.Fatalf("a session changed at byte %d is accepted", i)
		}
	}
	for _, v := range []string{"", "x", value[:len(value)-2], value + "AA"} {
		if a.Authenticated(request([]*http.Cookie{{Name: SessionCookie, Value: v}}, "")) {
			t.Fatalf("session %q accepted", v)
		}
	}
}

func TestRevokedSessionsAreBounded(t *testing.T) {
	a := NewAuthenticator("secret", true)
	now := time.Now()
	for i := range maxRevoked + 5 {
		a.revoke(fmt.Sprint(i), now.Add(time.Hour+time.Duration(i)*time.Second))
	}
	if len(a.revoked) != maxRevoked {
		t.Fatalf("revoked = %d", len(a.revoked))
	}
	// Expired ones are forgotten: two hours later, all of them are.
	a.now = func() time.Time { return now.Add(2 * time.Hour) }
	a.revoke("last", now.Add(3*time.Hour))
	if len(a.revoked) != 1 {
		t.Fatalf("revoked after they expired = %d, want 1", len(a.revoked))
	}
}

func TestLoadOrCreateToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub", "token")
	token, created, err := LoadOrCreateToken(path)
	if err != nil || !created || len(token) < 40 {
		t.Fatalf("create: %q %v %v", token, created, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode: %v %v", info.Mode(), err)
	}
	again, created, err := LoadOrCreateToken(path)
	if err != nil || created || again != token {
		t.Fatalf("reload: %q %v %v", again, created, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateToken(path); !errors.Is(err, ErrTokenFilePermissions) {
		t.Fatalf("readable token file accepted: %v", err)
	}
	other, _ := NewToken()
	if other == token {
		t.Fatal("tokens repeat")
	}
}
