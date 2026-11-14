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

package hub

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// startWithOnlineNode starts a hub with node 7, online.
func startWithOnlineNode(t *testing.T, records ...NodeRecord) (*Hub, *fakeController, *fakeSession) {
	t.Helper()
	ctl := newFakeController(append([]NodeRecord{{NodeID: 7}}, records...)...)
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	var s *fakeSession
	for s == nil || s.nodeID != 7 {
		s = nextSession(t, ctl)
	}
	s.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, true)}})
	waitFor(t, h, 7, "online", func(n Node) bool { return n.Status == StatusOnline })
	return h, ctl, s
}

func listen(h *Hub) (func() []Change, func()) {
	var mu sync.Mutex
	var changes []Change
	remove := h.Listen(func(c Change) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	})
	return func() []Change {
		mu.Lock()
		defer mu.Unlock()
		return append([]Change(nil), changes...)
	}, remove
}

func TestRemoveNode(t *testing.T) {
	h, ctl, s := startWithOnlineNode(t)
	changes, stop := listen(h)
	defer stop()

	removal, err := h.RemoveNode(7, false)
	if err != nil || !removal.FromDevice {
		t.Fatalf("RemoveNode() = %+v, %v", removal, err)
	}
	if s.removed != 1 {
		t.Errorf("RemoveFabric calls = %d, want 1", s.removed)
	}
	if closed, _ := s.isClosed(); !closed {
		t.Error("the session is still open")
	}
	if _, ok := h.Node(7); ok {
		t.Error("the node is still listed")
	}
	ctl.mu.Lock()
	forgotten, records := ctl.forgotten, len(ctl.records)
	ctl.mu.Unlock()
	if len(forgotten) != 1 || forgotten[0] != 7 || records != 0 {
		t.Errorf("forgotten = %v, records = %d", forgotten, records)
	}
	last := changes()[len(changes())-1]
	if last.Kind != ChangeNodeRemoved || last.NodeID != 7 {
		t.Errorf("last change = %+v", last)
	}

	// The node is gone, and not connected to again.
	if _, err := h.RemoveNode(7, false); !errors.Is(err, ErrUnknownNode) {
		t.Errorf("RemoveNode() again: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if n := ctl.connectCount(7); n != 1 {
		t.Errorf("connects after the removal = %d, want 1", n)
	}
}

func TestRemoveNodeThatRefuses(t *testing.T) {
	h, ctl, s := startWithOnlineNode(t)
	s.mu.Lock()
	s.removeErr = errors.New("NOCResponse status=2")
	s.mu.Unlock()

	if _, err := h.RemoveNode(7, false); !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("RemoveNode() error = %v, want ErrRemoveFailed", err)
	}
	if n, ok := h.Node(7); !ok || n.Status != StatusOnline {
		t.Fatalf("after the refusal: node = %+v, %v", n, ok)
	}

	// Removed from the hub only, when asked.
	removal, err := h.RemoveNode(7, true)
	if err != nil || removal.FromDevice {
		t.Fatalf("RemoveNode(local) = %+v, %v", removal, err)
	}
	if _, ok := h.Node(7); ok {
		t.Error("the node is still listed")
	}
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	if len(ctl.forgotten) != 1 {
		t.Errorf("forgotten = %v", ctl.forgotten)
	}
}

func TestRemoveOfflineNode(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7})
	ctl.connectErrs[7] = []error{errors.New("unreachable"), errors.New("unreachable"), errors.New("unreachable"), errors.New("unreachable"), errors.New("unreachable"), errors.New("unreachable")}
	h, err := New(Config{Controller: ctl, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RetryMin: time.Hour, RetryMax: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop() })
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, h, 7, "offline", func(n Node) bool { return n.Status == StatusOffline })

	if _, err := h.RemoveNode(7, false); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("RemoveNode() error = %v, want ErrNodeOffline", err)
	}
	removal, err := h.RemoveNode(7, true)
	if err != nil || removal.FromDevice {
		t.Fatalf("RemoveNode(local) = %+v, %v", removal, err)
	}
	if _, ok := h.Node(7); ok {
		t.Error("the node is still listed")
	}
}

func TestRemoveNodeKeepsTheOthers(t *testing.T) {
	h, _, _ := startWithOnlineNode(t, NodeRecord{NodeID: 8})
	if _, err := h.RemoveNode(7, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Node(8); !ok {
		t.Fatal("node 8 was removed too")
	}
	if _, err := h.RemoveNode(9, false); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("RemoveNode(9) error = %v", err)
	}
}

func TestRemoveNodeBeforeStart(t *testing.T) {
	h := newHub(t, newFakeController(NodeRecord{NodeID: 7}))
	if _, err := h.RemoveNode(7, false); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("RemoveNode() before Start: %v", err)
	}
}
