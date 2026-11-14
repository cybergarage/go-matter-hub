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
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cybergarage/homewire/internal/auth"
)

func newSignInCode(t *testing.T, s *Server) signInCodeResponse {
	t.Helper()
	res := do(s, call{method: "POST", path: "/api/v1/session/codes", headers: bearer})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/v1/session/codes: status %d", res.StatusCode)
	}
	var code signInCodeResponse
	if err := json.NewDecoder(res.Body).Decode(&code); err != nil {
		t.Fatal(err)
	}
	return code
}

// A sign-in link, from a code given for the token, signs a browser in
// once, and sends it on to the web UI without the code.
func TestSignInLink(t *testing.T) {
	s, _ := nodesServer(true)
	if res := do(s, call{method: "POST", path: "/api/v1/session/codes"}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a code without the token: status %d", res.StatusCode)
	}

	code := newSignInCode(t, s)
	if !strings.HasPrefix(code.Path, SignInPath+"?code=") || !strings.Contains(code.Path, code.Code) {
		t.Fatalf("path = %q", code.Path)
	}
	res := do(s, call{method: "GET", path: code.Path})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("sign-in link: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("the sign-in link set no session")
	}
	if res := do(s, call{method: "GET", path: "/api/v1/hub", cookies: []*http.Cookie{session}}); res.StatusCode != http.StatusOK {
		t.Fatalf("the session from the link: status %d", res.StatusCode)
	}

	// Used up: the browser is sent to the UI, signed out.
	res = do(s, call{method: "GET", path: code.Path})
	if res.StatusCode != http.StatusSeeOther || len(res.Cookies()) != 0 {
		t.Fatalf("the link used again: status %d, cookies %v", res.StatusCode, res.Cookies())
	}
	if res := do(s, call{method: "GET", path: SignInPath + "?code=made-up"}); res.StatusCode != http.StatusSeeOther || len(res.Cookies()) != 0 {
		t.Fatalf("a made-up code: status %d", res.StatusCode)
	}
}
