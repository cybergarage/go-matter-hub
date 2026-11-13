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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/cybergarage/go-matter-hub/internal/hub"
)

const (
	// streamBuffer is how many changes a stream may fall behind before it
	// is closed; the client then connects again and starts from a fresh
	// snapshot.
	streamBuffer = 256
	// streamHeartbeat is how often an idle stream sends a comment, which
	// keeps proxies from closing it and tells the client the hub is there.
	streamHeartbeat = 15 * time.Second
)

// nodeJSON is a node as the API reports it. A node ID is 16 hexadecimal
// digits: a JSON number cannot hold a 64-bit ID exactly.
type nodeJSON struct {
	ID string `json:"id"`
	hub.Node
}

func newNodeJSON(n hub.Node) nodeJSON {
	if n.Attributes == nil {
		n.Attributes = []hub.Attribute{}
	}
	return nodeJSON{ID: nodeIDText(n.ID), Node: n}
}

func nodeIDText(id uint64) string {
	return fmt.Sprintf("%016X", id)
}

type nodesResponse struct {
	Nodes []nodeJSON `json:"nodes"`
}

func (s *Server) nodes() nodesResponse {
	res := nodesResponse{Nodes: []nodeJSON{}}
	if s.hub == nil {
		return res
	}
	for _, n := range s.hub.Nodes() {
		res.Nodes = append(res.Nodes, newNodeJSON(n))
	}
	return res
}

func (s *Server) handleNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.nodes())
}

// nodeID reads the node ID of the request path, and answers the request
// when it is malformed.
func nodeID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	text := r.PathValue("id")
	id, err := strconv.ParseUint(text, 16, 64)
	if err != nil || len(text) != 16 {
		writeError(w, http.StatusBadRequest, "bad_request", "a node ID is 16 hexadecimal digits")
		return 0, false
	}
	return id, true
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}
	if s.hub != nil {
		if n, ok := s.hub.Node(id); ok {
			writeJSON(w, http.StatusOK, newNodeJSON(n))
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "no such node")
}

// streamEvent is one Server-Sent Event.
type streamEvent struct {
	name string
	data any
}

type statusEvent struct {
	Node   string     `json:"node"`
	Status hub.Status `json:"status"`
	Error  string     `json:"error,omitempty"`
}

type attributesEvent struct {
	Node string `json:"node"`
	// Priming is true when the attributes are the whole state of the
	// node, which replaces what the client holds.
	Priming    bool            `json:"priming"`
	Attributes []hub.Attribute `json:"attributes"`
}

type commissioningEvent struct {
	Commissioning commissioningJSON `json:"commissioning"`
}

func changeEvent(c hub.Change) streamEvent {
	switch {
	case c.Kind == hub.ChangeAttributes:
		return streamEvent{name: "attributes", data: attributesEvent{Node: nodeIDText(c.NodeID), Priming: c.Priming, Attributes: c.Attributes}}
	case c.Kind == hub.ChangeNodeAdded && c.Node != nil:
		return streamEvent{name: "node", data: newNodeJSON(*c.Node)}
	case c.Kind == hub.ChangeCommissioning && c.Commissioning != nil:
		return streamEvent{name: "commissioning", data: commissioningEvent{Commissioning: newCommissioningJSON(*c.Commissioning)}}
	}
	return streamEvent{name: "status", data: statusEvent{Node: nodeIDText(c.NodeID), Status: c.Status, Error: c.Error}}
}

func writeEvent(w http.ResponseWriter, e streamEvent) error {
	data, err := json.Marshal(e.data)
	if err != nil {
		return err
	}
	// json.Marshal writes no line breaks, so the data is one line.
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, data)
	return err
}

// handleEvents streams the state of the nodes as Server-Sent Events: a
// "nodes" event with every node first, then a "status" or an "attributes"
// event for each change of a node, a "node" event for a node which joined,
// and a "commissioning" event for the progress of a commissioning.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Listen before the snapshot is taken, so that no change falls between
	// the two; a change which the snapshot already holds is told again,
	// which sets the same value.
	changes := make(chan streamEvent, streamBuffer)
	overflow := make(chan struct{})
	var (
		mu         sync.Mutex
		overflowed bool
	)
	if s.hub != nil {
		remove := s.hub.Listen(func(c hub.Change) {
			// Called by the hub for one node at a time, but for
			// several nodes at once; it must not block.
			mu.Lock()
			defer mu.Unlock()
			if overflowed {
				return
			}
			select {
			case changes <- changeEvent(c):
			default:
				overflowed = true
				close(overflow)
			}
		})
		defer remove()
	}

	if err := writeEvent(w, streamEvent{name: "nodes", data: s.nodes()}); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	heartbeat := time.NewTicker(s.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.streams.Done():
			return
		case <-overflow:
			return
		case e := <-changes:
			if err := writeEvent(w, e); err != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// CloseStreams ends the event streams, which otherwise last as long as
// their clients stay; a server which shuts down calls it first.
func (s *Server) CloseStreams() {
	s.closeStreams()
}

func newStreams() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
