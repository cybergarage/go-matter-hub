// Copyright (C) 2026 The go-matter-hub Authors. All rights reserved.
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
	"fmt"
	"net/http"
	"testing"

	"github.com/cybergarage/go-matter-hub/internal/hub"
)

const lightPath = "/api/v1/nodes/B923078EEB190343"

func TestRemoveNode(t *testing.T) {
	s, h := nodesServer(true)
	if res := do(s, call{method: "DELETE", path: lightPath}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "DELETE", path: lightPath, headers: map[string]string{
		"Authorization": "Bearer " + testToken, "Origin": "https://attacker.example",
	}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin: status %d", res.StatusCode)
	}

	res := do(s, call{method: "DELETE", path: lightPath, headers: bearer})
	var removal hub.Removal
	if err := json.NewDecoder(res.Body).Decode(&removal); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !removal.FromDevice {
		t.Fatalf("status %d, %+v", res.StatusCode, removal)
	}
	res = do(s, call{method: "DELETE", path: lightPath + "?local=true", headers: bearer})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("local: status %d", res.StatusCode)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.removed) != 2 || h.removed[0] != (removeCall{lightID, false}) || h.removed[1] != (removeCall{lightID, true}) {
		t.Fatalf("removed = %+v", h.removed)
	}
}

func TestRemoveNodeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		path   string
		err    error
		status int
		code   string
	}{
		"unknown":     {lightPath, hub.ErrUnknownNode, http.StatusNotFound, "not_found"},
		"offline":     {lightPath, hub.ErrNodeOffline, http.StatusConflict, "node_offline"},
		"removing":    {lightPath, hub.ErrRemoving, http.StatusConflict, "busy"},
		"refused":     {lightPath, fmt.Errorf("%w: NOCResponse status=2", hub.ErrRemoveFailed), http.StatusBadGateway, "remove_failed"},
		"not started": {lightPath, hub.ErrNotStarted, http.StatusServiceUnavailable, "not_ready"},
		"bad local":   {lightPath + "?local=yes", nil, http.StatusBadRequest, "bad_request"},
		"bad ID":      {"/api/v1/nodes/B923", nil, http.StatusBadRequest, "bad_request"},
	} {
		s, h := nodesServer(true)
		h.removeErr = tc.err
		res := do(s, call{method: "DELETE", path: tc.path, headers: bearer})
		var body errorBody
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.StatusCode != tc.status || body.Error.Code != tc.code {
			t.Errorf("%s: status %d, code %q", name, res.StatusCode, body.Error.Code)
		}
	}
}

func TestEventStreamTellsRemovals(t *testing.T) {
	s, h := nodesServer(true)
	_, events, _ := openStream(t, s)
	if e := nextEvent(t, events); e.name != "nodes" {
		t.Fatalf("first event = %+v", e)
	}
	waitListeners(t, h, 1)
	h.emit(hub.Change{NodeID: lightID, Kind: hub.ChangeNodeRemoved})
	e := nextEvent(t, events)
	var removed removedEvent
	if e.name != "removed" || json.Unmarshal([]byte(e.data), &removed) != nil || removed.Node != "B923078EEB190343" {
		t.Fatalf("removed event = %+v", e)
	}
}
