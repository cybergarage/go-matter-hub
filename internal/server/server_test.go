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

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cybergarage/go-matter-hub/internal/auth"
	"github.com/cybergarage/go-matter-hub/internal/hub"
)

const testToken = "test-token"

func newServer(required bool) *Server {
	return New(Config{
		Auth:         auth.NewAuthenticator(testToken, required),
		AllowedHosts: []string{"hub.local"},
		UI: fstest.MapFS{
			"index.html":    {Data: []byte("<!doctype html><title>hub</title>")},
			"assets/app.js": {Data: []byte("console.log(1)")},
		},
	})
}

type call struct {
	method  string
	path    string
	host    string
	body    string
	headers map[string]string
	cookies []*http.Cookie
}

func do(s *Server, c call) *http.Response {
	var body *strings.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(c.method, c.path, body)
	if c.host != "" {
		r.Host = c.host
	} else {
		r.Host = "127.0.0.1:8580"
	}
	if c.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.headers {
		r.Header.Set(k, v)
	}
	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Result()
}

func errorCode(t *testing.T, res *http.Response) string {
	t.Helper()
	var body errorBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Error.Code
}

func TestHostHeader(t *testing.T) {
	s := newServer(false)
	allowed := []string{"127.0.0.1:8580", "localhost:8580", "[::1]:8580", "192.168.1.20:8580", "hub.local:8580", "HUB.local", "10.0.0.5"}
	for _, host := range allowed {
		if res := do(s, call{method: "GET", path: "/api/v1/health", host: host}); res.StatusCode != http.StatusOK {
			t.Errorf("host %q: status %d", host, res.StatusCode)
		}
	}
	// A page which rebinds its own name to the hub's address sends that name.
	refused := []string{"attacker.example:8580", "hub.local.attacker.example", "localhost.attacker.example:8580"}
	for _, host := range refused {
		res := do(s, call{method: "GET", path: "/api/v1/health", host: host})
		if res.StatusCode != http.StatusForbidden || errorCode(t, res) != "host_not_allowed" {
			t.Errorf("host %q: status %d", host, res.StatusCode)
		}
		if res := do(s, call{method: "GET", path: "/", host: host}); res.StatusCode != http.StatusForbidden {
			t.Errorf("host %q: UI status %d", host, res.StatusCode)
		}
	}
}

func TestCrossOriginWritesAreRefused(t *testing.T) {
	s := newServer(true)
	login := `{"token":"` + testToken + `"}`
	tests := []struct {
		name    string
		headers map[string]string
		status  int
	}{
		{"same origin", map[string]string{"Origin": "http://127.0.0.1:8580"}, http.StatusOK},
		{"no origin (script or curl)", nil, http.StatusOK},
		{"other site", map[string]string{"Origin": "https://attacker.example"}, http.StatusForbidden},
		{"other port on this host", map[string]string{"Origin": "http://127.0.0.1:9000"}, http.StatusForbidden},
		{"opaque origin", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"cross-site without origin", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site without origin", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
	}
	for _, tt := range tests {
		res := do(s, call{method: "POST", path: "/api/v1/session", body: login, headers: tt.headers})
		if res.StatusCode != tt.status {
			t.Errorf("%s: status %d, want %d", tt.name, res.StatusCode, tt.status)
		}
	}
	// Reads are not subject to the origin check.
	if res := do(s, call{method: "GET", path: "/api/v1/health", headers: map[string]string{"Origin": "https://attacker.example"}}); res.StatusCode != http.StatusOK {
		t.Errorf("cross-origin read: status %d", res.StatusCode)
	}
}

func TestProtectedAPIWithoutRequiredAuth(t *testing.T) {
	s := newServer(false)
	if res := do(s, call{method: "GET", path: "/api/v1/hub"}); res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	res := do(s, call{method: "GET", path: "/api/v1/session"})
	var st sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil || st.AuthRequired || !st.Authenticated {
		t.Fatalf("session = %+v, %v", st, err)
	}
}

func TestSignInFlow(t *testing.T) {
	s := newServer(true)

	res := do(s, call{method: "GET", path: "/api/v1/hub"})
	if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/health"}); res.StatusCode != http.StatusOK {
		t.Fatalf("health needs a session: %d", res.StatusCode)
	}
	res = do(s, call{method: "GET", path: "/api/v1/session"})
	var st sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&st); err != nil || !st.AuthRequired || st.Authenticated {
		t.Fatalf("session before sign-in = %+v, %v", st, err)
	}

	res = do(s, call{method: "POST", path: "/api/v1/session", body: `{"token":"wrong"}`})
	if res.StatusCode != http.StatusUnauthorized || errorCode(t, res) != "invalid_token" {
		t.Fatalf("wrong token: status %d", res.StatusCode)
	}

	res = do(s, call{method: "POST", path: "/api/v1/session", body: `{"token":"` + testToken + `"}`})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: status %d", res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/hub", cookies: cookies}); res.StatusCode != http.StatusOK {
		t.Fatalf("with session: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/hub", headers: map[string]string{"Authorization": "Bearer " + testToken}}); res.StatusCode != http.StatusOK {
		t.Fatalf("with bearer token: status %d", res.StatusCode)
	}

	if res := do(s, call{method: "DELETE", path: "/api/v1/session", cookies: cookies}); res.StatusCode != http.StatusOK {
		t.Fatalf("sign-out: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/hub", cookies: cookies}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after sign-out: status %d", res.StatusCode)
	}
}

func TestSignInBodyHasToBeJSON(t *testing.T) {
	s := newServer(true)
	res := do(s, call{method: "POST", path: "/api/v1/session", body: "token=" + testToken, headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}})
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form body: status %d", res.StatusCode)
	}
	res = do(s, call{method: "POST", path: "/api/v1/session", body: `{"token":"` + testToken + `","extra":1}`})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: status %d", res.StatusCode)
	}
}

func TestUIAndHeaders(t *testing.T) {
	s := newServer(true)
	// The UI loads without a session, so that it can show the sign-in form.
	for _, p := range []string{"/", "/devices", "/devices/12"} {
		res := do(s, call{method: "GET", path: p})
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: status %d, type %q", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
		if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: security headers missing", p)
		}
	}
	res := do(s, call{method: "GET", path: "/assets/app.js"})
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("asset: status %d, cache %q", res.StatusCode, res.Header.Get("Cache-Control"))
	}
	if res := do(s, call{method: "GET", path: "/assets/missing.js"}); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/unknown"}); res.StatusCode != http.StatusNotFound || errorCode(t, res) != "not_found" {
		t.Errorf("unknown API: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/health"}); res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("API responses may be cached")
	}
}

func TestUIMissing(t *testing.T) {
	s := New(Config{Auth: auth.NewAuthenticator("", false), UI: fstest.MapFS{".gitkeep": {}}})
	if res := do(s, call{method: "GET", path: "/"}); res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.StatusCode)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/health"}); res.StatusCode != http.StatusOK {
		t.Fatalf("API without UI: status %d", res.StatusCode)
	}
}

type fakeHub struct {
	fabric hub.Fabric
	nodes  []hub.Node
}

func (h fakeHub) Fabric() (hub.Fabric, bool) { return h.fabric, h.fabric.FabricID != 0 }
func (h fakeHub) Nodes() []hub.Node          { return h.nodes }

func TestHubInfo(t *testing.T) {
	// Without a hub, the fields about it are left out.
	res := do(newServer(false), call{method: "GET", path: "/api/v1/hub"})
	var bare map[string]any
	if err := json.NewDecoder(res.Body).Decode(&bare); err != nil {
		t.Fatal(err)
	}
	if _, ok := bare["fabricId"]; ok {
		t.Fatalf("fabricId without a hub: %v", bare)
	}
	if _, ok := bare["nodes"]; ok {
		t.Fatalf("nodes without a hub: %v", bare)
	}

	s := New(Config{
		Auth: auth.NewAuthenticator(testToken, true),
		Hub: fakeHub{
			fabric: hub.Fabric{FabricID: 0xB923078EEB190343},
			nodes: []hub.Node{
				{ID: 1, Status: hub.StatusOnline},
				{ID: 2, Status: hub.StatusOffline},
				{ID: 3, Status: hub.StatusConnecting},
			},
		},
	})
	// The fabric is not told without the access token.
	if res := do(s, call{method: "GET", path: "/api/v1/hub"}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: status %d", res.StatusCode)
	}
	res = do(s, call{method: "GET", path: "/api/v1/hub", headers: map[string]string{"Authorization": "Bearer " + testToken}})
	var info hubResponse
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.FabricID != "B923078EEB190343" || info.Nodes == nil || info.Nodes.Total != 3 || info.Nodes.Online != 1 {
		t.Fatalf("hub info = %+v, nodes %+v", info, info.Nodes)
	}
}
