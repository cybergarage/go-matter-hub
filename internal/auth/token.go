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

// Package auth protects the HTTP API of the hub: an access token kept in a
// file, and the browser sessions and bearer credentials derived from it.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	tokenBytes    = 32
	tokenFileMode = 0o600
	tokenDirMode  = 0o700
)

// ErrTokenFilePermissions is returned when the token file can be read by
// other users of the host.
var ErrTokenFilePermissions = errors.New("token file is accessible by other users")

// NewToken returns a random access token of 256 bits, in URL-safe base64.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LoadOrCreateToken reads the access token from path, or writes a new one
// there with mode 0600 when the file does not exist. created reports whether
// the token is new.
func LoadOrCreateToken(path string) (string, bool, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.Mode().Perm()&0o077 != 0 {
			return "", false, fmt.Errorf("%w: %s (mode %04o, want 0600)", ErrTokenFilePermissions, path, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false, err
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", false, fmt.Errorf("token file is empty: %s", path)
		}
		return token, false, nil
	case !errors.Is(err, os.ErrNotExist):
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), tokenDirMode); err != nil {
		return "", false, err
	}
	token, err := NewToken()
	if err != nil {
		return "", false, err
	}
	// O_EXCL: never replace a token another process has just written.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, tokenFileMode)
	if err != nil {
		return "", false, err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	return token, true, nil
}
