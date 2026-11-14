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
	"log/slog"
	"testing"

	"github.com/cybergarage/homewire/internal/hub"
)

func TestLoopbackOnly(t *testing.T) {
	tests := []struct {
		listen   string
		loopback bool
		invalid  bool
	}{
		{"127.0.0.1:8580", true, false},
		{"localhost:8580", true, false},
		{"[::1]:8580", true, false},
		{"127.0.0.2:8580", true, false},
		{":8580", false, false},
		{"0.0.0.0:8580", false, false},
		{"[::]:8580", false, false},
		{"192.168.1.10:8580", false, false},
		{"hub.local:8580", false, false},
		{"8580", false, true},
	}
	for _, tt := range tests {
		got, err := loopbackOnly(tt.listen)
		if (err != nil) != tt.invalid {
			t.Errorf("loopbackOnly(%q) error = %v", tt.listen, err)
			continue
		}
		if got != tt.loopback {
			t.Errorf("loopbackOnly(%q) = %v, want %v", tt.listen, got, tt.loopback)
		}
	}
}

func TestParseOptionsDefaultsToLoopback(t *testing.T) {
	o, err := parseServeOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	loopback, err := loopbackOnly(o.listen)
	if err != nil || !loopback {
		t.Fatalf("default listen address %q is not loopback", o.listen)
	}
	if o.requireAuth {
		t.Fatal("the access token is required by default on loopback")
	}
}

func TestParseOptionsVendorIDAndLogLevel(t *testing.T) {
	o, err := parseServeOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.vendorID != hub.DefaultVendorID || o.logLevel != slog.LevelInfo {
		t.Fatalf("defaults: vendor 0x%04X, level %v", o.vendorID, o.logLevel)
	}
	o, err = parseServeOptions([]string{"-vendor-id", "0x1217", "-log-level", "debug"})
	if err != nil || o.vendorID != 0x1217 || o.logLevel != slog.LevelDebug {
		t.Fatalf("options = %+v, %v", o, err)
	}
	if o, err = parseServeOptions([]string{"-vendor-id", "65521"}); err != nil || o.vendorID != 0xFFF1 {
		t.Fatalf("decimal vendor ID: %+v, %v", o, err)
	}
	for _, args := range [][]string{
		{"-vendor-id", "0"},
		{"-vendor-id", "0xFFF5"},
		{"-vendor-id", "apple"},
		{"-log-level", "loud"},
		{"extra"},
	} {
		if _, err := parseServeOptions(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
