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
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cybergarage/homewire/internal/auth"
	"github.com/cybergarage/homewire/internal/hub"
)

const lightID = 0xB923078EEB190343

func lightNode() hub.Node {
	return hub.Node{
		ID:        lightID,
		VendorID:  0xFFF1,
		ProductID: 0x8001,
		Status:    hub.StatusOnline,
		Attributes: []hub.Attribute{
			{AttributePath: hub.AttributePath{Endpoint: 1, Cluster: 6, Attribute: 0}, Value: false},
		},
	}
}

func nodesServer(required bool, nodes ...hub.Node) (*Server, *fakeHub) {
	h := &fakeHub{fabric: hub.Fabric{FabricID: 1}, nodes: nodes}
	s := New(Config{Auth: auth.NewAuthenticator(testToken, required), Hub: h, AllowedHosts: []string{"hub.local"}})
	return s, h
}

func TestNodes(t *testing.T) {
	s, _ := nodesServer(true, lightNode(), hub.Node{ID: 2, Status: hub.StatusConnecting})
	bearer := map[string]string{"Authorization": "Bearer " + testToken}

	for _, p := range []string{"/api/v1/nodes", "/api/v1/nodes/B923078EEB190343", "/api/v1/events"} {
		if res := do(s, call{method: "GET", path: p}); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without the token: status %d", p, res.StatusCode)
		}
	}

	res := do(s, call{method: "GET", path: "/api/v1/nodes", headers: bearer})
	var list struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Nodes) != 2 {
		t.Fatalf("nodes = %v", list.Nodes)
	}
	light := list.Nodes[0]
	// The ID is text: a JSON number would round it.
	if light["id"] != "B923078EEB190343" || light["status"] != "online" || light["vendorId"] != float64(0xFFF1) {
		t.Fatalf("light = %v", light)
	}
	attrs, _ := light["attributes"].([]any)
	if len(attrs) != 1 {
		t.Fatalf("attributes = %v", light["attributes"])
	}
	attr, _ := attrs[0].(map[string]any)
	if attr["endpoint"] != float64(1) || attr["cluster"] != float64(6) || attr["attribute"] != float64(0) || attr["value"] != false {
		t.Fatalf("attribute = %v", attr)
	}
	// A node without attributes has an empty list, not null.
	if got, ok := list.Nodes[1]["attributes"].([]any); !ok || len(got) != 0 {
		t.Fatalf("attributes of a node without any = %#v", list.Nodes[1]["attributes"])
	}

	res = do(s, call{method: "GET", path: "/api/v1/nodes/B923078EEB190343", headers: bearer})
	var one map[string]any
	if err := json.NewDecoder(res.Body).Decode(&one); err != nil || one["id"] != "B923078EEB190343" {
		t.Fatalf("node = %v, %v", one, err)
	}
	if res := do(s, call{method: "GET", path: "/api/v1/nodes/0000000000000009", headers: bearer}); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown node: status %d", res.StatusCode)
	}
	for _, id := range []string{"9", "B923078EEB19034Z", "0xB923078EEB1903"} {
		if res := do(s, call{method: "GET", path: "/api/v1/nodes/" + id, headers: bearer}); res.StatusCode != http.StatusBadRequest {
			t.Errorf("node ID %q: status %d", id, res.StatusCode)
		}
	}
}

func TestNodesWithoutAHub(t *testing.T) {
	res := do(newServer(false), call{method: "GET", path: "/api/v1/nodes"})
	var list map[string]any
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if got, ok := list["nodes"].([]any); !ok || len(got) != 0 {
		t.Fatalf("nodes = %#v", list["nodes"])
	}
}

// event is one Server-Sent Event as the stream carries it.
type event struct {
	name string
	data string
}

// readEvents sends the events of a stream to a channel, and closes it when
// the stream ends.
func readEvents(res *http.Response) <-chan event {
	out := make(chan event, 16)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(res.Body)
		var e event
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			case strings.HasPrefix(line, ":"):
				out <- event{name: "comment", data: line}
			case line == "" && e.name != "":
				out <- e
				e = event{}
			}
		}
	}()
	return out
}

func nextEvent(t *testing.T, events <-chan event) event {
	t.Helper()
	select {
	case e, ok := <-events:
		if !ok {
			t.Fatal("the stream ended")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return event{}
	}
}

func openStream(t *testing.T, s *Server) (*http.Response, <-chan event, context.CancelFunc) {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res, readEvents(res), cancel
}

func waitListeners(t *testing.T, h *fakeHub, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for h.listenerCount() != want {
		if time.Now().After(deadline) {
			t.Fatalf("listeners = %d, want %d", h.listenerCount(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestEventStream(t *testing.T) {
	s, h := nodesServer(true, lightNode())
	res, events, cancel := openStream(t, s)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, headers %v", res.StatusCode, res.Header)
	}

	// The stream opens with every node.
	first := nextEvent(t, events)
	var snapshot struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if first.name != "nodes" || json.Unmarshal([]byte(first.data), &snapshot) != nil || len(snapshot.Nodes) != 1 || snapshot.Nodes[0]["id"] != "B923078EEB190343" {
		t.Fatalf("first event = %+v", first)
	}

	h.emit(hub.Change{NodeID: lightID, Kind: hub.ChangeAttributes, Status: hub.StatusOnline, Attributes: []hub.Attribute{
		{AttributePath: hub.AttributePath{Endpoint: 1, Cluster: 6, Attribute: 0}, Value: true},
	}})
	e := nextEvent(t, events)
	var attrs attributesEvent
	if e.name != "attributes" || json.Unmarshal([]byte(e.data), &attrs) != nil {
		t.Fatalf("event = %+v", e)
	}
	if attrs.Node != "B923078EEB190343" || attrs.Priming || len(attrs.Attributes) != 1 || attrs.Attributes[0].Value != true || attrs.Attributes[0].Cluster != 6 {
		t.Fatalf("attributes event = %+v", attrs)
	}

	h.emit(hub.Change{NodeID: lightID, Kind: hub.ChangeStatus, Status: hub.StatusOffline, Error: "no report"})
	e = nextEvent(t, events)
	var status statusEvent
	if e.name != "status" || json.Unmarshal([]byte(e.data), &status) != nil || status.Status != hub.StatusOffline || status.Error != "no report" || status.Node != "B923078EEB190343" {
		t.Fatalf("event = %+v", e)
	}

	// A client which leaves is forgotten.
	cancel()
	waitListeners(t, h, 0)
}

func TestEventStreamHeartbeatAndShutdown(t *testing.T) {
	s, h := nodesServer(true)
	s.heartbeat = 20 * time.Millisecond
	_, events, _ := openStream(t, s)
	if e := nextEvent(t, events); e.name != "nodes" {
		t.Fatalf("first event = %+v", e)
	}
	if e := nextEvent(t, events); e.name != "comment" {
		t.Fatalf("no heartbeat: %+v", e)
	}
	waitListeners(t, h, 1)

	// Closing the streams ends them, as a server which shuts down does.
	s.CloseStreams()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				waitListeners(t, h, 0)
				return
			}
		case <-deadline:
			t.Fatal("the stream stayed open")
		}
	}
}

func TestEventStreamClosesAClientWhichFallsBehind(t *testing.T) {
	s, h := nodesServer(true, lightNode())
	_, events, _ := openStream(t, s)
	if e := nextEvent(t, events); e.name != "nodes" {
		t.Fatalf("first event = %+v", e)
	}
	waitListeners(t, h, 1)
	// More changes at once than the stream buffers; the handler cannot
	// write them out as fast as they come.
	change := hub.Change{NodeID: lightID, Kind: hub.ChangeStatus, Status: hub.StatusOnline}
	h.mu.Lock()
	fns := make([]func(hub.Change), 0, len(h.listeners))
	for _, fn := range h.listeners {
		fns = append(fns, fn)
	}
	h.mu.Unlock()
	for i := 0; i < 50*streamBuffer; i++ {
		for _, fn := range fns {
			fn(change)
		}
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the stream stayed open")
		}
	}
}
