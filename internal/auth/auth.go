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
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// SessionCookie is the name of the browser session cookie.
	SessionCookie = "go_matter_hub_session"
	// SessionLifetime is how long a browser session lasts.
	SessionLifetime = 30 * 24 * time.Hour

	maxSessions       = 64
	maxLoginFailures  = 10
	loginFailureFrame = time.Minute
)

var (
	// ErrInvalidToken is returned by Login for a wrong access token.
	ErrInvalidToken = errors.New("invalid access token")
	// ErrTooManyAttempts is returned by Login while too many wrong tokens
	// have been tried recently.
	ErrTooManyAttempts = errors.New("too many failed sign-in attempts")
)

// Authenticator decides whether a request may use the protected API.
//
// A request is authenticated by the access token in an Authorization: Bearer
// header, or by the session cookie that Login sets for a browser. Sessions
// live in memory: a restart of the hub signs every browser out.
type Authenticator struct {
	required bool
	digest   [sha256.Size]byte

	mu       sync.Mutex
	sessions map[string]time.Time // session ID digest → expiry
	failures []time.Time
	now      func() time.Time
}

// NewAuthenticator returns an Authenticator for the access token. With
// required false, every request is treated as authenticated; that is meant
// for a hub which listens on the loopback interface only.
func NewAuthenticator(token string, required bool) *Authenticator {
	return &Authenticator{
		required: required,
		digest:   sha256.Sum256([]byte(token)),
		sessions: map[string]time.Time{},
		now:      time.Now,
	}
}

// Required reports whether requests have to be authenticated.
func (a *Authenticator) Required() bool { return a.required }

func (a *Authenticator) tokenMatches(token string) bool {
	d := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(d[:], a.digest[:]) == 1
}

func sessionKey(id string) string {
	d := sha256.Sum256([]byte(id))
	return base64.RawURLEncoding.EncodeToString(d[:])
}

// Authenticated reports whether the request carries a valid bearer token or
// session cookie.
func (a *Authenticator) Authenticated(r *http.Request) bool {
	if !a.required {
		return true
	}
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, token, ok := strings.Cut(h, " ")
		return ok && strings.EqualFold(scheme, "Bearer") && a.tokenMatches(strings.TrimSpace(token))
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	key := sessionKey(c.Value)
	a.mu.Lock()
	defer a.mu.Unlock()
	expiry, ok := a.sessions[key]
	if !ok {
		return false
	}
	if !a.now().Before(expiry) {
		delete(a.sessions, key)
		return false
	}
	return true
}

// Login checks the access token and, when it is right, starts a browser
// session by setting the session cookie on w.
func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request, token string) error {
	now := a.now()
	a.mu.Lock()
	recent := a.failures[:0]
	for _, t := range a.failures {
		if now.Sub(t) < loginFailureFrame {
			recent = append(recent, t)
		}
	}
	a.failures = recent
	if len(a.failures) >= maxLoginFailures {
		a.mu.Unlock()
		return ErrTooManyAttempts
	}
	a.mu.Unlock()

	if !a.tokenMatches(token) {
		a.mu.Lock()
		a.failures = append(a.failures, now)
		a.mu.Unlock()
		return ErrInvalidToken
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	expiry := now.Add(SessionLifetime)

	a.mu.Lock()
	for k, e := range a.sessions {
		if !now.Before(e) {
			delete(a.sessions, k)
		}
	}
	// Bound the table; drop the session which expires first.
	for len(a.sessions) >= maxSessions {
		oldest, oldestExpiry := "", time.Time{}
		for k, e := range a.sessions {
			if oldest == "" || e.Before(oldestExpiry) {
				oldest, oldestExpiry = k, e
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sessionKey(id)] = expiry
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    id,
		Path:     "/",
		Expires:  expiry,
		MaxAge:   int(SessionLifetime / time.Second),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// Logout ends the browser session of the request and clears its cookie.
func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		a.mu.Lock()
		delete(a.sessions, sessionKey(c.Value))
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}
