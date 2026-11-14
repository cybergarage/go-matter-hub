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

// Package logbridge passes the log of go-matter, which logs with go-logger,
// on to the hub's slog logger.
package logbridge

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"sync"

	"github.com/cybergarage/go-logger/log"
)

// Component is the value of the "component" attribute of each record the
// bridge makes.
const Component = "go-matter"

var (
	// A go-logger line: "2026-11-14 10:42:34 [INFO] message", with the
	// time maybe ending in fractional seconds.
	linePattern = regexp.MustCompile(`^\S+ \S+ \[([A-Z]+)\] (.*)$`)
	// The message of a hex dump line: an offset, then bytes, then their
	// ASCII, as in "0000 4D 54 3A ...  MT:...".
	hexdumpPattern = regexp.MustCompile(`^[0-9A-F]{4,} [0-9A-F]{2} `)
)

var levels = map[string]slog.Level{
	"TRACE": slog.LevelDebug - 4,
	"DEBUG": slog.LevelDebug,
	"INFO":  slog.LevelInfo,
	"WARN":  slog.LevelWarn,
	"ERROR": slog.LevelError,
	"FATAL": slog.LevelError,
}

// Writer is the io.Writer of a go-logger writer logger. It turns each line
// into a record of logger, with the line's level and a "component"
// attribute.
//
// Hex dump lines are dropped, whatever their level. go-matter dumps the
// messages it sends and receives, some after decryption, such as the
// AddOrUpdateWiFiNetwork command that carries the Wi-Fi password; the
// ASCII column of a dump would show it, and the hub never logs a secret.
type Writer struct {
	logger *slog.Logger

	mu      sync.Mutex
	pending []byte
}

// NewWriter returns a Writer which logs to logger.
func NewWriter(logger *slog.Logger) *Writer {
	return &Writer{logger: logger.With("component", Component)}
}

// Write logs each complete line of p. A line without its LF yet waits for
// the rest of it.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		w.logLine(string(bytes.TrimRight(w.pending[:i], "\r")))
		w.pending = w.pending[i+1:]
	}
	return len(p), nil
}

func (w *Writer) logLine(line string) {
	m := linePattern.FindStringSubmatch(line)
	if m == nil {
		// Not a go-logger line; nothing is known of what it holds.
		return
	}
	level, ok := levels[m[1]]
	if !ok {
		level = slog.LevelInfo
	}
	msg := m[2]
	if msg == "" || hexdumpPattern.MatchString(msg) {
		return
	}
	w.logger.Log(context.Background(), level, msg)
}

// LevelFor returns the go-logger level which lets the go-matter messages
// through that the hub logs at level: every one at debug, and only
// warnings and errors otherwise, since go-matter tells each step of each
// session at its info level.
func LevelFor(level slog.Level) log.Level {
	switch {
	case level <= slog.LevelDebug:
		return log.LevelDebug
	case level <= slog.LevelWarn:
		return log.LevelWarn
	default:
		return log.LevelError
	}
}

// Install makes go-matter log to logger, as LevelFor says for level.
func Install(logger *slog.Logger, level slog.Level) {
	log.SetDefault(log.NewWriterLogger(NewWriter(logger), LevelFor(level), log.WithTimeLayout(log.TimeLayoutMilliseconds)))
}
