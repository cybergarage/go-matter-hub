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

package logbridge

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/cybergarage/go-logger/log"
)

type record struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	Component string `json:"component"`
}

// install makes go-logger log through the bridge into a JSON buffer, as the
// hub does at level, and restores go-logger's logger after the test.
func install(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	saved := log.Default()
	t.Cleanup(func() { log.SetDefault(saved) })
	var buf bytes.Buffer
	Install(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})), level)
	return &buf
}

func records(t *testing.T, buf *bytes.Buffer) []record {
	t.Helper()
	var out []record
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func TestBridgeAtDebug(t *testing.T) {
	buf := install(t, slog.LevelDebug)
	log.Infof("Commissioning: %s", "Operational Credentials")
	log.Debugf("a debug message")
	log.Warnf("a warning")
	log.Errorf("an error")
	log.Tracef("a trace message")

	got := records(t, buf)
	want := []record{
		{"INFO", "Commissioning: Operational Credentials", Component},
		{"DEBUG", "a debug message", Component},
		{"WARN", "a warning", Component},
		{"ERROR", "an error", Component},
	}
	if len(got) != len(want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// At info and above, go-matter's step-by-step info is left out.
func TestBridgeAtInfo(t *testing.T) {
	buf := install(t, slog.LevelInfo)
	log.Infof("CASE Sigma1: session_id=0x1234")
	log.Debugf("a debug message")
	log.Warnf("a warning")
	log.Errorf("an error")
	got := records(t, buf)
	if len(got) != 2 || got[0].Msg != "a warning" || got[1].Msg != "an error" {
		t.Fatalf("records = %+v", got)
	}

	buf = install(t, slog.LevelError)
	log.Warnf("a warning")
	log.Errorf("an error")
	if got := records(t, buf); len(got) != 1 || got[0].Msg != "an error" {
		t.Fatalf("at error: records = %+v", got)
	}
}

// go-matter dumps the messages it sends and receives, some decrypted, like
// AddOrUpdateWiFiNetwork with the Wi-Fi password. No dump reaches the log,
// at any level, neither its bytes nor its ASCII column.
func TestBridgeDropsHexDumps(t *testing.T) {
	const secret = "correct horse battery staple"
	buf := install(t, slog.LevelDebug)
	payload := append([]byte{0x15, 0x30, 0x00, byte(len(secret))}, secret...)
	log.HexDebug(payload)
	log.HexInfo(payload)
	log.HexError(payload)
	log.Infof("after the dump")

	out := buf.String()
	if strings.Contains(out, "horse") || strings.Contains(out, "63 6F 72 72") {
		t.Fatalf("the log holds the secret:\n%s", out)
	}
	if got := records(t, buf); len(got) != 1 || got[0].Msg != "after the dump" {
		t.Fatalf("records = %+v", got)
	}
}

func TestWriterJoinsPartialLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(slog.New(slog.NewJSONHandler(&buf, nil)))
	for _, part := range []string{"2026-11-14 10:42:34.507 [WA", "RN] first\n2026-11-14 10:42:35 [ERROR] sec", "ond\r\n", "not a go-logger line\n"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	got := records(t, &buf)
	if len(got) != 2 || got[0].Msg != "first" || got[0].Level != "WARN" || got[1].Msg != "second" || got[1].Level != "ERROR" {
		t.Fatalf("records = %+v", got)
	}
}
