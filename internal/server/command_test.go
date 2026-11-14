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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cybergarage/go-matter/matter/datamodel/command"
	"github.com/cybergarage/homewire/internal/hub"
)

const commandPath = "/api/v1/nodes/B923078EEB190343/commands"

func TestCommand(t *testing.T) {
	s, h := nodesServer(true, lightNode())
	bearer := map[string]string{"Authorization": "Bearer " + testToken}
	body := `{"endpoint":1,"cluster":8,"command":4,"fields":{"Level":127,"TransitionTime":5,"OptionsMask":0,"OptionsOverride":0}}`

	if res := do(s, call{method: "POST", path: commandPath, body: body}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: status %d", res.StatusCode)
	}
	// A page on another site cannot send a command through a browser.
	if res := do(s, call{method: "POST", path: commandPath, body: body, headers: map[string]string{
		"Authorization": "Bearer " + testToken, "Origin": "https://attacker.example",
	}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin: status %d", res.StatusCode)
	}

	res := do(s, call{method: "POST", path: commandPath, body: body, headers: bearer})
	var ok commandResponse
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&ok) != nil || !ok.OK {
		t.Fatalf("status %d", res.StatusCode)
	}
	h.mu.Lock()
	got := h.invoked
	h.mu.Unlock()
	if len(got) != 1 || got[0].node != lightID {
		t.Fatalf("invoked = %+v", got)
	}
	c := got[0].cmd
	if c.Endpoint != 1 || c.Cluster != 8 || c.Command != 4 || len(c.Fields) != 4 {
		t.Fatalf("command = %+v", c)
	}
	// Numbers reach the hub exactly, as json.Number.
	if level, isNumber := c.Fields["Level"].(json.Number); !isNumber || level != "127" {
		t.Fatalf("Level = %#v", c.Fields["Level"])
	}
	// A command without fields.
	if res := do(s, call{method: "POST", path: commandPath, body: `{"endpoint":1,"cluster":6,"command":1}`, headers: bearer}); res.StatusCode != http.StatusOK {
		t.Fatalf("On: status %d", res.StatusCode)
	}
}

func TestCommandRequestErrors(t *testing.T) {
	s, _ := nodesServer(true, lightNode())
	bearer := map[string]string{"Authorization": "Bearer " + testToken}
	tests := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{"malformed node ID", "/api/v1/nodes/12/commands", `{"endpoint":1,"cluster":6,"command":1}`, http.StatusBadRequest},
		{"missing command", commandPath, `{"endpoint":1,"cluster":6}`, http.StatusBadRequest},
		{"missing endpoint", commandPath, `{"cluster":6,"command":1}`, http.StatusBadRequest},
		{"endpoint out of range", commandPath, `{"endpoint":65535,"cluster":6,"command":1}`, http.StatusBadRequest},
		{"negative cluster", commandPath, `{"endpoint":1,"cluster":-1,"command":1}`, http.StatusBadRequest},
		{"unknown member", commandPath, `{"endpoint":1,"cluster":6,"command":1,"node":"x"}`, http.StatusBadRequest},
		{"not JSON", commandPath, `endpoint=1`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		if res := do(s, call{method: "POST", path: tt.path, body: tt.body, headers: bearer}); res.StatusCode != tt.status {
			t.Errorf("%s: status %d, want %d", tt.name, res.StatusCode, tt.status)
		}
	}
}

func TestCommandErrorsOfTheHub(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{hub.ErrUnknownNode, http.StatusNotFound, "not_found"},
		{hub.ErrNodeOffline, http.StatusConflict, "node_offline"},
		{fmt.Errorf("%w: Identify", command.ErrUnknownCommand), http.StatusBadRequest, "unsupported_command"},
		{fmt.Errorf("%w: Door Lock LockDoor", hub.ErrTimedCommand), http.StatusBadRequest, "unsupported_command"},
		{fmt.Errorf("%w: MoveToLevel needs Level", command.ErrMissingField), http.StatusBadRequest, "invalid_fields"},
		{fmt.Errorf("%w: Level: 255 is outside 0..254", command.ErrInvalidValue), http.StatusBadRequest, "invalid_fields"},
		{&hub.CommandError{Status: hub.InvokeStatus{IMStatus: 0x81}}, http.StatusBadGateway, "command_failed"},
		{errors.New("response timeout"), http.StatusGatewayTimeout, "node_error"},
	}
	for _, tt := range tests {
		s, h := nodesServer(true, lightNode())
		h.invokeErr = tt.err
		res := do(s, call{method: "POST", path: commandPath, body: `{"endpoint":1,"cluster":6,"command":1}`, headers: map[string]string{"Authorization": "Bearer " + testToken}})
		if res.StatusCode != tt.status {
			t.Errorf("%v: status %d, want %d", tt.err, res.StatusCode, tt.status)
			continue
		}
		var body errorBody
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Error.Code != tt.code || body.Error.Message != tt.err.Error() {
			t.Errorf("%v: body %+v", tt.err, body)
		}
	}
}
