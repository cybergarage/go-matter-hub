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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hubClient calls the API of the hub of a data directory, as the
// commands other than serve do: with the access token from the data
// directory when the hub requires it.
type hubClient struct {
	info  serveInfo
	token string
	http  *http.Client
}

// connect returns a client of the running hub of the data directory.
func connect(dataDir string) (*hubClient, error) {
	info, err := readServeInfo(dataDir)
	if err != nil {
		return nil, err
	}
	c := &hubClient{info: info, http: &http.Client{Timeout: 10 * time.Second}}
	if info.AuthRequired {
		if c.token, err = readToken(dataDir); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// readToken reads the access token of the data directory.
func readToken(dataDir string) (string, error) {
	path := filepath.Join(dataDir, tokenFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("access token: %w", err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("access token: %s is empty", path)
	}
	return token, nil
}

// apiError is an error answer of the API.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call sends a request to the API and decodes the answer into out.
func (c *hubClient) call(method, path string, out any) error {
	req, err := http.NewRequest(method, c.info.URL+path, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("the hub does not answer at %s; is it running? (%w)", c.info.URL, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		var e apiError
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			return fmt.Errorf("%s %s: %s (%s)", method, path, e.Error.Message, e.Error.Code)
		}
		return fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.Join(fmt.Errorf("%s %s: unexpected answer", method, path), err)
	}
	return nil
}
