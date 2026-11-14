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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"
)

// serveInfoFileName is the file in the data directory where a running hub
// tells the other commands how to reach it.
const serveInfoFileName = "serve.json"

// serveInfo is what a running hub tells the other commands.
type serveInfo struct {
	// URL reaches the hub from this host: the listen address, with a
	// loopback address for an unspecified one.
	URL string `json:"url"`
	// Listen is the address the hub listens on.
	Listen string `json:"listen"`
	// AuthRequired is whether the hub requires the access token.
	AuthRequired bool      `json:"authRequired"`
	PID          int       `json:"pid"`
	StartedAt    time.Time `json:"startedAt"`
}

func newServeInfo(addr net.Addr, authRequired bool) serveInfo {
	listen := addr.String()
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		host, port = "127.0.0.1", "8580"
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		if ip != nil && ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	return serveInfo{
		URL:          "http://" + net.JoinHostPort(host, port),
		Listen:       listen,
		AuthRequired: authRequired,
		PID:          os.Getpid(),
		StartedAt:    time.Now().UTC().Truncate(time.Second),
	}
}

// port returns the port of the hub.
func (i serveInfo) port() string {
	if _, port, err := net.SplitHostPort(i.Listen); err == nil {
		return port
	}
	return ""
}

// loopbackOnly reports whether the hub can be reached from this host only.
func (i serveInfo) loopbackOnly() bool {
	host, _, err := net.SplitHostPort(i.Listen)
	if err != nil {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// writeServeInfo writes info into the data directory, readable by the
// owner only, replacing it at once so that a reader never sees half of it.
func writeServeInfo(dir string, info serveInfo) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, serveInfoFileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, serveInfoFileName))
}

// removeServeInfo removes the file a hub wrote, unless another hub has
// written it since.
func removeServeInfo(dir string, info serveInfo) {
	path := filepath.Join(dir, serveInfoFileName)
	if current, err := readServeInfoFile(path); err == nil && current.PID == info.PID {
		os.Remove(path)
	}
}

// errNotRunning is a data directory without a running hub.
var errNotRunning = errors.New("the hub is not running")

// readServeInfo returns what the hub of the data directory told.
func readServeInfo(dir string) (serveInfo, error) {
	path := filepath.Join(dir, serveInfoFileName)
	info, err := readServeInfoFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return info, fmt.Errorf("%w with the data directory %s; start it with \"%s serve\"", errNotRunning, dir, commandName)
	}
	return info, err
}

func readServeInfoFile(path string) (serveInfo, error) {
	var info serveInfo
	b, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return info, fmt.Errorf("%s: %w", path, err)
	}
	if info.URL == "" {
		return info, fmt.Errorf("%s: no URL", path)
	}
	return info, nil
}

// hostURL returns the URL of the hub under another host name, at its port.
func (i serveInfo) hostURL(host string) string {
	return "http://" + net.JoinHostPort(host, i.port())
}
