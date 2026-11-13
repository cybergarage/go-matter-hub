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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cybergarage/go-matter-hub/internal/hub"
)

const (
	commissioningPath = "/api/v1/commissioning"
	// The manual pairing code of the Matter SDK's example devices.
	setupCode    = "34970112332"
	wifiPassword = "correct horse battery"
)

var bearer = map[string]string{"Authorization": "Bearer " + testToken}

func decodeCommissioning(t *testing.T, res *http.Response) *commissioningJSON {
	t.Helper()
	var body commissioningResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Commissioning
}

func TestStartCommissioning(t *testing.T) {
	s, h := nodesServer(true)
	body := `{"code":"` + setupCode + `","wifi":{"ssid":"home","password":"` + wifiPassword + `"}}`

	if res := do(s, call{method: "POST", path: commissioningPath, body: body}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: status %d", res.StatusCode)
	}
	if res := do(s, call{method: "POST", path: commissioningPath, body: body, headers: map[string]string{
		"Authorization": "Bearer " + testToken, "Origin": "https://attacker.example",
	}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin: status %d", res.StatusCode)
	}

	// Before any, there is none.
	res := do(s, call{method: "GET", path: commissioningPath, headers: bearer})
	if res.StatusCode != http.StatusOK || decodeCommissioning(t, res) != nil {
		t.Fatalf("before: status %d", res.StatusCode)
	}

	res = do(s, call{method: "POST", path: commissioningPath, body: body, headers: bearer})
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	// Neither the setup code nor the password is told back.
	if strings.Contains(string(raw), setupCode) || strings.Contains(string(raw), wifiPassword) {
		t.Fatalf("the response tells a secret: %s", raw)
	}
	var started commissioningResponse
	if err := json.Unmarshal(raw, &started); err != nil || started.Commissioning == nil ||
		started.Commissioning.State != hub.CommissioningRunning || !started.Commissioning.OverBluetooth || started.Commissioning.Node != "" {
		t.Fatalf("started = %s", raw)
	}
	h.mu.Lock()
	calls := h.commissions
	h.mu.Unlock()
	if len(calls) != 1 || calls[0].code != setupCode || calls[0].wifi == nil ||
		calls[0].wifi.SSID != "home" || calls[0].wifi.Password != wifiPassword {
		t.Fatalf("commissions = %+v", calls)
	}

	// Without Wi-Fi, for a device on the network.
	res = do(s, call{method: "POST", path: commissioningPath, body: `{"code":"` + setupCode + `"}`, headers: bearer})
	if c := decodeCommissioning(t, res); res.StatusCode != http.StatusAccepted || c == nil || c.OverBluetooth {
		t.Fatalf("without Wi-Fi: status %d, %+v", res.StatusCode, c)
	}

	res = do(s, call{method: "GET", path: commissioningPath, headers: bearer})
	if c := decodeCommissioning(t, res); c == nil || c.ID != 2 {
		t.Fatalf("latest = %+v", c)
	}
}

func TestStartCommissioningRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		err    error
		status int
		code   string
	}{
		"bad code":       {`{"code":"12345"}`, nil, http.StatusBadRequest, "invalid_code"},
		"short password": {`{"code":"` + setupCode + `","wifi":{"ssid":"home","password":"short"}}`, nil, http.StatusBadRequest, "invalid_wifi"},
		"no SSID":        {`{"code":"` + setupCode + `","wifi":{"ssid":"","password":"` + wifiPassword + `"}}`, nil, http.StatusBadRequest, "invalid_wifi"},
		"busy":           {`{"code":"` + setupCode + `"}`, hub.ErrCommissioningBusy, http.StatusConflict, "busy"},
		"not started":    {`{"code":"` + setupCode + `"}`, hub.ErrNotStarted, http.StatusServiceUnavailable, "not_ready"},
		"unknown field":  {`{"code":"` + setupCode + `","pin":1}`, nil, http.StatusBadRequest, "bad_request"},
	} {
		s, h := nodesServer(true)
		h.commissionErr = tc.err
		res := do(s, call{method: "POST", path: commissioningPath, body: tc.body, headers: bearer})
		var body errorBody
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != tc.status || body.Error.Code != tc.code {
			t.Errorf("%s: status %d, code %q", name, res.StatusCode, body.Error.Code)
		}
		if strings.Contains(body.Error.Message, "short") || strings.Contains(body.Error.Message, wifiPassword) {
			t.Errorf("%s: the error tells the password: %q", name, body.Error.Message)
		}
	}
}

func TestEventStreamTellsCommissioning(t *testing.T) {
	s, h := nodesServer(true)
	_, events, _ := openStream(t, s)
	if e := nextEvent(t, events); e.name != "nodes" {
		t.Fatalf("first event = %+v", e)
	}
	waitListeners(t, h, 1)

	node := lightNode()
	h.emit(hub.Change{NodeID: node.ID, Kind: hub.ChangeNodeAdded, Status: node.Status, Node: &node})
	e := nextEvent(t, events)
	var added map[string]any
	if e.name != "node" || json.Unmarshal([]byte(e.data), &added) != nil || added["id"] != "B923078EEB190343" {
		t.Fatalf("node event = %+v", e)
	}

	done := hub.Commissioning{ID: 1, State: hub.CommissioningSucceeded, NodeID: node.ID, StartedAt: time.Now(), FinishedAt: time.Now()}
	h.emit(hub.Change{NodeID: node.ID, Kind: hub.ChangeCommissioning, Commissioning: &done})
	e = nextEvent(t, events)
	var progress commissioningEvent
	if e.name != "commissioning" || json.Unmarshal([]byte(e.data), &progress) != nil ||
		progress.Commissioning.State != hub.CommissioningSucceeded || progress.Commissioning.Node != "B923078EEB190343" {
		t.Fatalf("commissioning event = %+v", e)
	}

	failed := hub.Commissioning{ID: 2, State: hub.CommissioningFailed, Error: "no device", ErrorCode: "not_found"}
	h.emit(hub.Change{Kind: hub.ChangeCommissioning, Commissioning: &failed})
	e = nextEvent(t, events)
	progress = commissioningEvent{}
	if e.name != "commissioning" || json.Unmarshal([]byte(e.data), &progress) != nil ||
		progress.Commissioning.ErrorCode != "not_found" || progress.Commissioning.Node != "" {
		t.Fatalf("failure event = %+v", e)
	}
}
