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

package server

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// SignInPath is the path of a sign-in link: SignInPath?code=<code>.
const SignInPath = "/signin"

type signInCodeResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Path is the path of the sign-in link, with the code.
	Path string `json:"path"`
}

// handleNewSignInCode gives a caller authenticated with the token a
// one-time sign-in code, for a link which signs a browser in. The
// "homewire open" command asks for it with the token from the data
// directory, so that nobody has to type or copy the token.
func (s *Server) handleNewSignInCode(w http.ResponseWriter, _ *http.Request) {
	code, expiry, err := s.auth.NewSignInCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not issue a sign-in code")
		return
	}
	writeJSON(w, http.StatusCreated, signInCodeResponse{
		Code:      code,
		ExpiresAt: expiry,
		Path:      SignInPath + "?" + url.Values{"code": {code}}.Encode(),
	})
}

// handleSignInLink signs a browser in with a sign-in code, and sends it on
// to the web UI, which also takes a browser whose code is not valid (any
// more) to the sign-in page. The code leaves the address bar with the
// redirect.
func (s *Server) handleSignInLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.auth.Required() {
		ok, err := s.auth.RedeemSignInCode(w, r, r.URL.Query().Get("code"))
		if err != nil {
			slog.Warn("could not start a session from a sign-in link", "error", err)
		} else if !ok {
			slog.Info("a sign-in link which is not valid was used", "remote", r.RemoteAddr)
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
