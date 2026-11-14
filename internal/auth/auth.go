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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// SessionCookie is the name of the browser session cookie.
	SessionCookie = "homewire_session"
	// SessionLifetime is how long a browser session lasts.
	SessionLifetime = 30 * 24 * time.Hour

	// maxRevoked bounds the sessions signed out before they expire.
	maxRevoked        = 1024
	maxLoginFailures  = 10
	loginFailureFrame = time.Minute

	// sessionVersion is the first byte of a session, the format of the
	// rest: its expiry, 8 bytes of Unix seconds, then 16 random bytes,
	// then the HMAC-SHA256 of all of that.
	sessionVersion    byte = 1
	sessionIDLength        = 16
	sessionDataLength      = 1 + 8 + sessionIDLength
	sessionLength          = sessionDataLength + sha256.Size
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
// header, or by the session cookie that Login sets for a browser.
//
// A session is signed with a key derived from the access token rather than
// kept by the hub, so it outlasts a restart of the hub, and replacing the
// token ends every session. A session signed out is refused until the hub
// restarts; the browser has dropped its cookie by then.
type Authenticator struct {
	required bool
	digest   [sha256.Size]byte
	key      []byte

	mu       sync.Mutex
	revoked  map[string]time.Time // session ID → expiry
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
		key:      sessionKey(token),
		revoked:  map[string]time.Time{},
		now:      time.Now,
	}
}

// sessionKey derives the key which signs the sessions from the access
// token, apart from the token itself.
func sessionKey(token string) []byte {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("homewire session key v1"))
	return mac.Sum(nil)
}

// Required reports whether requests have to be authenticated.
func (a *Authenticator) Required() bool { return a.required }

func (a *Authenticator) tokenMatches(token string) bool {
	d := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(d[:], a.digest[:]) == 1
}

func (a *Authenticator) sign(data []byte) []byte {
	mac := hmac.New(sha256.New, a.key)
	mac.Write(data)
	return mac.Sum(nil)
}

// newSession returns a session which expires at expiry, and its ID.
func (a *Authenticator) newSession(expiry time.Time) (value, id string, err error) {
	data := make([]byte, sessionDataLength)
	data[0] = sessionVersion
	binary.BigEndian.PutUint64(data[1:9], uint64(expiry.Unix()))
	if _, err := rand.Read(data[9:]); err != nil {
		return "", "", err
	}
	raw := append(data, a.sign(data)...)
	return base64.RawURLEncoding.EncodeToString(raw), base64.RawURLEncoding.EncodeToString(data[9:]), nil
}

// parseSession returns the ID and expiry of a session the hub signed; ok
// is false for anything else.
func (a *Authenticator) parseSession(value string) (id string, expiry time.Time, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != sessionLength || raw[0] != sessionVersion {
		return "", time.Time{}, false
	}
	data, sig := raw[:sessionDataLength], raw[sessionDataLength:]
	if !hmac.Equal(sig, a.sign(data)) {
		return "", time.Time{}, false
	}
	expiry = time.Unix(int64(binary.BigEndian.Uint64(data[1:9])), 0)
	return base64.RawURLEncoding.EncodeToString(data[9:]), expiry, true
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
	id, expiry, ok := a.parseSession(c.Value)
	if !ok || !a.now().Before(expiry) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, revoked := a.revoked[id]
	return !revoked
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
	return a.startSession(w, r)
}

// startSession sets the cookie of a new session on w.
func (a *Authenticator) startSession(w http.ResponseWriter, r *http.Request) error {
	// Whole seconds: the session carries its expiry in Unix seconds.
	expiry := a.now().Add(SessionLifetime).Truncate(time.Second)
	value, _, err := a.newSession(expiry)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
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
		if id, expiry, ok := a.parseSession(c.Value); ok {
			a.revoke(id, expiry)
		}
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

// revoke refuses a session until it expires, or until the hub restarts.
func (a *Authenticator) revoke(id string, expiry time.Time) {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, e := range a.revoked {
		if !now.Before(e) {
			delete(a.revoked, k)
		}
	}
	// Bound the table; forget the session which expires first.
	for len(a.revoked) >= maxRevoked {
		first, firstExpiry := "", time.Time{}
		for k, e := range a.revoked {
			if first == "" || e.Before(firstExpiry) {
				first, firstExpiry = k, e
			}
		}
		delete(a.revoked, first)
	}
	a.revoked[id] = expiry
}
