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
	"net"
	"net/http"
	"net/url"
	"strings"
)

// hostGuard rejects requests a web page on another site could make a browser
// send to the hub.
//
// The Host header has to name the hub: an IP address literal, a loopback
// name, or a configured host name. A page which rebinds its own domain name
// to the address of the hub (DNS rebinding) sends that domain name as Host,
// and is refused.
//
// A request which changes state has to come from a page the hub served: its
// Origin, when the browser sends one, has to equal the Host of the request.
type hostGuard struct {
	names map[string]bool
}

func newHostGuard(names []string) *hostGuard {
	g := &hostGuard{names: map[string]bool{"localhost": true}}
	for _, n := range names {
		if n = normalizeHostName(n); n != "" {
			g.names[n] = true
		}
	}
	return g
}

func normalizeHostName(n string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
}

// hostOnly returns the host of a Host header value, without the port and
// without the brackets of an IPv6 literal.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
}

func (g *hostGuard) hostAllowed(hostport string) bool {
	h := hostOnly(hostport)
	if h == "" {
		return false
	}
	// Strip an IPv6 zone before parsing (fe80::1%en0).
	addr, _, _ := strings.Cut(h, "%")
	if net.ParseIP(addr) != nil {
		return true
	}
	return g.names[normalizeHostName(h)]
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func sameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
	}
	// No Origin: a browser still says where the request comes from.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

func (g *hostGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.hostAllowed(r.Host) {
			writeError(w, http.StatusForbidden, "host_not_allowed", "this host name is not allowed; use the IP address, or start the hub with -allowed-host")
			return
		}
		if !safeMethod(r.Method) && !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross_origin", "cross-origin requests are not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}
