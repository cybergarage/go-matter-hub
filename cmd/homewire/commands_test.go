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

package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cybergarage/homewire/internal/auth"
	"github.com/cybergarage/homewire/internal/server"
)

const testToken = "test-token-0123456789"

// runningHub starts the hub's HTTP server, requiring the access token, and
// a data directory as "homewire serve" leaves it.
func runningHub(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tokenFileName), []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(server.Config{Auth: auth.NewAuthenticator(testToken, true)}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().(*net.TCPAddr)
	info := newServeInfo(&net.TCPAddr{IP: net.IPv4zero, Port: addr.Port}, true)
	if err := writeServeInfo(dir, info); err != nil {
		t.Fatal(err)
	}
	return dir, srv
}

// captureStdout runs f with stdout captured.
func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	err = f()
	os.Stdout = saved
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out), err
}

func TestServeInfo(t *testing.T) {
	dir := t.TempDir()
	info := newServeInfo(&net.TCPAddr{IP: net.IPv4zero, Port: 8580}, true)
	if info.URL != "http://127.0.0.1:8580" || info.loopbackOnly() {
		t.Fatalf("info for 0.0.0.0:8580 = %+v", info)
	}
	if v6 := newServeInfo(&net.TCPAddr{IP: net.IPv6unspecified, Port: 8580}, true); v6.URL != "http://[::1]:8580" {
		t.Fatalf("URL for [::]:8580 = %q", v6.URL)
	}
	if lo := newServeInfo(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8580}, false); !lo.loopbackOnly() {
		t.Fatal("127.0.0.1 is not loopback only")
	}

	if err := writeServeInfo(dir, info); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, serveInfoFileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("serve.json: %v, %v", fi, err)
	}
	got, err := readServeInfo(dir)
	if err != nil || got.URL != info.URL || got.PID != os.Getpid() {
		t.Fatalf("readServeInfo() = %+v, %v", got, err)
	}

	// Another hub's file is left alone; the hub's own goes.
	other := info
	other.PID++
	if err := writeServeInfo(dir, other); err != nil {
		t.Fatal(err)
	}
	removeServeInfo(dir, info)
	if _, err := readServeInfo(dir); err != nil {
		t.Fatal("removed the file of another hub")
	}
	removeServeInfo(dir, other)
	if _, err := readServeInfo(dir); !errors.Is(err, errNotRunning) {
		t.Fatalf("readServeInfo() after the hub stopped: %v", err)
	}
}

// homewire open opens a link which signs the browser in once.
func TestOpen(t *testing.T) {
	dir, srv := runningHub(t)
	var opened string
	saved := openURL
	openURL = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { openURL = saved })

	if err := run([]string{"open", "-data-dir", dir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opened, server.SignInPath+"?code=") || !strings.HasPrefix(opened, "http://127.0.0.1:") {
		t.Fatalf("opened %q", opened)
	}

	// The link signs a browser in, and is sent on to the web UI.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(opened)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || len(res.Cookies()) != 1 {
		t.Fatalf("the link: status %d, cookies %v", res.StatusCode, res.Cookies())
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/hub", nil)
	req.AddCookie(res.Cookies()[0])
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("the session from the link: %v, %v", res, err)
	}

	// A browser which cannot be opened gets the link printed.
	openURL = func(string) error { return errors.New("no graphical session") }
	out, err := captureStdout(t, func() error { return run([]string{"open", "-data-dir", dir}, &bytes.Buffer{}) })
	if err != nil || !strings.Contains(out, server.SignInPath+"?code=") {
		t.Fatalf("without a browser: %q, %v", out, err)
	}
}

func TestOpenPrint(t *testing.T) {
	dir, _ := runningHub(t)
	out, err := captureStdout(t, func() error {
		return run([]string{"open", "-data-dir", dir, "-print", "-host", "hub.example"}, &bytes.Buffer{})
	})
	if err != nil || !strings.HasPrefix(out, "http://hub.example:") || !strings.Contains(out, server.SignInPath+"?code=") {
		t.Fatalf("open -print -host = %q, %v", out, err)
	}
	// Without -host, the link names this host on the home network.
	out, err = captureStdout(t, func() error { return run([]string{"open", "-data-dir", dir, "-print"}, &bytes.Buffer{}) })
	if name := localHostName(); err != nil || name != "" && !strings.HasPrefix(out, "http://"+name+":") {
		t.Fatalf("open -print = %q, %v", out, err)
	}
}

func TestStatusAndToken(t *testing.T) {
	dir, _ := runningHub(t)
	out, err := captureStdout(t, func() error { return run([]string{"status", "-data-dir", dir}, &bytes.Buffer{}) })
	if err != nil || !strings.Contains(out, "access token required") || !strings.Contains(out, "0 node(s)") {
		t.Fatalf("status = %q, %v", out, err)
	}
	out, err = captureStdout(t, func() error { return run([]string{"token", "-data-dir", dir}, &bytes.Buffer{}) })
	if err != nil || strings.TrimSpace(out) != testToken {
		t.Fatalf("token = %q, %v", out, err)
	}

	// With a wrong token, the hub refuses.
	if err := os.WriteFile(filepath.Join(dir, tokenFileName), []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"status", "-data-dir", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("status with a wrong token: no error")
	}
}

func TestCommandsWithoutAHub(t *testing.T) {
	dir := t.TempDir()
	for _, cmd := range []string{"open", "status"} {
		if err := run([]string{cmd, "-data-dir", dir}, &bytes.Buffer{}); !errors.Is(err, errNotRunning) {
			t.Errorf("%s without a hub: %v", cmd, err)
		}
	}
	if err := run([]string{"token", "-data-dir", dir}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "no access token") {
		t.Errorf("token without one: %v", err)
	}

	// A hub which stopped without removing its file does not answer.
	info := newServeInfo(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, false)
	info.StartedAt = time.Now()
	if err := writeServeInfo(dir, info); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"status", "-data-dir", dir}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "does not answer") {
		t.Errorf("status of a stopped hub: %v", err)
	}
}

func TestNodeName(t *testing.T) {
	var n nodeStatus
	n.Attributes = append(n.Attributes, struct {
		Endpoint  uint16 `json:"endpoint"`
		Cluster   uint32 `json:"cluster"`
		Attribute uint32 `json:"attribute"`
		Value     any    `json:"value"`
	}{0, basicInformationCluster, productNameAttribute, " Color Bulb "})
	if n.name() != "Color Bulb" {
		t.Fatalf("name() = %q", n.name())
	}
	var buf bytes.Buffer
	printStatus(&buf, serveInfo{Listen: "0.0.0.0:8580"}, hubStatus{Version: "0.1.0", FabricID: "874CD694686ACA14"}, []nodeStatus{{ID: "87AE777C98261661", Status: "online", Attributes: n.Attributes}})
	if !strings.Contains(buf.String(), "87AE777C98261661  online  Color Bulb") || !strings.Contains(buf.String(), "1 node(s), 1 online") {
		t.Fatalf("status:\n%s", buf.String())
	}
}
