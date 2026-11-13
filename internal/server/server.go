// Copyright (C) 2026 The go-matter-hub Authors. All rights reserved.
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

// Package server is the HTTP server of the hub: the JSON API under /api/v1
// and the web UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/cybergarage/go-matter-hub/internal/auth"
	"github.com/cybergarage/go-matter-hub/internal/hub"
	"github.com/cybergarage/go-matter-hub/internal/version"
)

const maxBodyBytes = 1 << 16

// Config configures a Server.
type Config struct {
	// Auth authenticates the requests of the protected API.
	Auth *auth.Authenticator
	// AllowedHosts are the host names, besides localhost and IP address
	// literals, which requests may use to reach the hub.
	AllowedHosts []string
	// Hub is the Matter hub the API reports on; nil serves the API
	// without one.
	Hub HubState
	// UI is the built web UI (index.html at its root). When it has no
	// index.html, the server answers with a notice instead.
	UI fs.FS
}

// HubState is what the API reads from the hub.
type HubState interface {
	// Fabric returns the fabric of the hub; ok is false before it started.
	Fabric() (fabric hub.Fabric, ok bool)
	// Nodes returns the state of every commissioned node.
	Nodes() []hub.Node
	// Node returns the state of a node.
	Node(id uint64) (hub.Node, bool)
	// Listen registers a function called for every change of a node's
	// state, and returns a function which removes it.
	Listen(fn func(hub.Change)) (remove func())
	// Invoke invokes a command on a node.
	Invoke(nodeID uint64, c hub.Command) error
}

// Server serves the API and the web UI.
type Server struct {
	auth *auth.Authenticator
	hub  HubState

	// streams is done when the event streams have to end.
	streams      context.Context
	closeStreams context.CancelFunc
	heartbeat    time.Duration
	ui           fs.FS
	handler      http.Handler
}

// New returns a Server.
func New(cfg Config) *Server {
	s := &Server{auth: cfg.Auth, hub: cfg.Hub, ui: cfg.UI, heartbeat: streamHeartbeat}
	s.streams, s.closeStreams = newStreams()

	mux := http.NewServeMux()
	// Public: what a page needs before the user has signed in.
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/session", s.handleSessionStatus)
	mux.HandleFunc("POST /api/v1/session", s.handleLogin)
	mux.HandleFunc("DELETE /api/v1/session", s.handleLogout)
	// Protected.
	mux.Handle("GET /api/v1/hub", s.protected(s.handleHub))
	mux.Handle("GET /api/v1/nodes", s.protected(s.handleNodes))
	mux.Handle("GET /api/v1/nodes/{id}", s.protected(s.handleNode))
	mux.Handle("GET /api/v1/events", s.protected(s.handleEvents))
	mux.Handle("POST /api/v1/nodes/{id}/commands", s.protected(s.handleCommand))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such API")
	})
	mux.HandleFunc("/", s.handleUI)

	s.handler = securityHeaders(newHostGuard(cfg.AllowedHosts).wrap(mux))
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) protected(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.Authenticated(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="go-matter-hub"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "sign in with the access token")
			return
		}
		next(w, r)
	})
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// readJSON decodes a JSON request body. A body has to be declared as JSON,
// which a cross-site HTML form cannot do.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	// Numbers stay exact, as json.Number, rather than float64.
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return false
	}
	return true
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: version.Version})
}

type sessionResponse struct {
	AuthRequired  bool `json:"authRequired"`
	Authenticated bool `json:"authenticated"`
}

func (s *Server) sessionStatus(r *http.Request) sessionResponse {
	return sessionResponse{AuthRequired: s.auth.Required(), Authenticated: s.auth.Authenticated(r)}
}

func (s *Server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sessionStatus(r))
}

type loginRequest struct {
	Token string `json:"token"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !s.auth.Required() {
		writeJSON(w, http.StatusOK, sessionResponse{AuthRequired: false, Authenticated: true})
		return
	}
	switch err := s.auth.Login(w, r, req.Token); {
	case err == nil:
		writeJSON(w, http.StatusOK, sessionResponse{AuthRequired: true, Authenticated: true})
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts; try again in a minute")
	case errors.Is(err, auth.ErrInvalidToken):
		writeError(w, http.StatusUnauthorized, "invalid_token", "the access token is not correct")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "could not start a session")
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(w, r)
	writeJSON(w, http.StatusOK, sessionResponse{AuthRequired: s.auth.Required(), Authenticated: !s.auth.Required()})
}

type hubResponse struct {
	Version      string `json:"version"`
	AuthRequired bool   `json:"authRequired"`
	// FabricID is the fabric of the hub as 16 hexadecimal digits; a JSON
	// number cannot hold a 64-bit ID exactly.
	FabricID string        `json:"fabricId,omitempty"`
	Nodes    *nodesSummary `json:"nodes,omitempty"`
}

type nodesSummary struct {
	Total  int `json:"total"`
	Online int `json:"online"`
}

func (s *Server) handleHub(w http.ResponseWriter, _ *http.Request) {
	res := hubResponse{Version: version.Version, AuthRequired: s.auth.Required()}
	if s.hub != nil {
		if fabric, ok := s.hub.Fabric(); ok {
			res.FabricID = fmt.Sprintf("%016X", fabric.FabricID)
		}
		summary := nodesSummary{}
		for _, n := range s.hub.Nodes() {
			summary.Total++
			if n.Status == hub.StatusOnline {
				summary.Online++
			}
		}
		res.Nodes = &summary
	}
	writeJSON(w, http.StatusOK, res)
}

const uiMissing = `<!doctype html>
<meta charset="utf-8">
<title>go-matter-hub</title>
<p>The web UI is not part of this build. Run <code>make web</code>, then build the hub again.</p>
`

// handleUI serves the files of the web UI, and index.html for every other
// path, so that the routes of the single-page application can be reloaded.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if s.ui == nil {
		s.serveUIMissing(w)
		return
	}
	if _, err := fs.Stat(s.ui, "index.html"); err != nil {
		s.serveUIMissing(w)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" {
		if info, err := fs.Stat(s.ui, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				// Vite names these files after their content.
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.ui, name)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.ui, "index.html")
}

func (s *Server) serveUIMissing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, uiMissing)
}
