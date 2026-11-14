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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWithoutACommand(t *testing.T) {
	var stderr bytes.Buffer
	if err := run(nil, &stderr); !errors.Is(err, errUsage) {
		t.Fatalf("run() error = %v, want errUsage", err)
	}
	if !strings.Contains(stderr.String(), "serve") {
		t.Errorf("usage does not list serve:\n%s", stderr.String())
	}

	stderr.Reset()
	if err := run([]string{"start"}, &stderr); !errors.Is(err, errUsage) {
		t.Fatalf("run(start) error = %v, want errUsage", err)
	}
	if !strings.Contains(stderr.String(), `unknown command "start"`) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"version"}, {"--version"}, {"serve", "-h"}} {
		if err := run(args, &bytes.Buffer{}); err != nil {
			t.Errorf("run(%v) error = %v", args, err)
		}
	}
	if err := run([]string{"serve", "-vendor-id", "0"}, &bytes.Buffer{}); err == nil || errors.Is(err, errUsage) {
		t.Errorf("run(serve -vendor-id 0) error = %v, want an option error", err)
	}
}

// The data directory is ~/.homewire. ~/.go-matter-hub, from before the
// hub was named homewire, is used when it is the only one there, so that
// an existing fabric and its nodes are kept.
func TestDefaultDataDir(t *testing.T) {
	var warned string
	saved := warnLegacyDataDir
	warnLegacyDataDir = func(legacy, _ string) { warned = legacy }
	t.Cleanup(func() { warnLegacyDataDir = saved })

	home := t.TempDir()
	current := filepath.Join(home, defaultDataDirName)
	legacy := filepath.Join(home, legacyDataDirName)

	if got := defaultDataDir(home); got != current || warned != "" {
		t.Fatalf("with neither: %q (warned %q), want %q", got, warned, current)
	}
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := defaultDataDir(home); got != legacy || warned != legacy {
		t.Fatalf("with the legacy one only: %q (warned %q), want %q", got, warned, legacy)
	}
	warned = ""
	if err := os.Mkdir(current, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := defaultDataDir(home); got != current || warned != "" {
		t.Fatalf("with both: %q (warned %q), want %q", got, warned, current)
	}
	if got, err := resolveDataDir("/srv/hub"); err != nil || got != "/srv/hub" {
		t.Fatalf("resolveDataDir(/srv/hub) = %q, %v", got, err)
	}
}
