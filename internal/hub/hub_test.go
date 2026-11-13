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
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cybergarage/go-matter/matter/encoding"
)

// fakeController is a Controller whose nodes are fakeSessions.
type fakeController struct {
	mu          sync.Mutex
	fabric      *Fabric
	records     []NodeRecord
	started     bool
	stopped     bool
	created     []uint16
	connects    map[uint64]int
	connectErrs map[uint64][]error
	sessions    map[uint64][]*fakeSession
	// connected receives every session Connect returns.
	connected chan *fakeSession
	// commissionResults answers the commissionings.
	commissionResults chan commissionResult
	commissions       []commissionCall
}

func newFakeController(records ...NodeRecord) *fakeController {
	return &fakeController{
		fabric:      &Fabric{FabricID: 0xFAB, AdminNodeID: 1, AdminVendorID: 0xFFF1},
		records:     records,
		connects:    map[uint64]int{},
		connectErrs: map[uint64][]error{},
		sessions:    map[uint64][]*fakeSession{},
		connected:   make(chan *fakeSession, 16),

		commissionResults: make(chan commissionResult, 4),
	}
}

func (c *fakeController) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = true
	return nil
}

func (c *fakeController) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	return nil
}

func (c *fakeController) Fabric() (Fabric, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fabric == nil {
		return Fabric{}, false, nil
	}
	return *c.fabric, true, nil
}

func (c *fakeController) CreateFabric(vendorID uint16) (Fabric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created = append(c.created, vendorID)
	c.fabric = &Fabric{FabricID: 0xC0FFEE, AdminNodeID: 1, AdminVendorID: vendorID}
	return *c.fabric, nil
}

func (c *fakeController) Nodes() ([]NodeRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]NodeRecord(nil), c.records...), nil
}

func (c *fakeController) Connect(_ context.Context, nodeID uint64) (Session, error) {
	c.mu.Lock()
	c.connects[nodeID]++
	if errs := c.connectErrs[nodeID]; len(errs) > 0 {
		err := errs[0]
		c.connectErrs[nodeID] = errs[1:]
		c.mu.Unlock()
		return nil, err
	}
	s := &fakeSession{nodeID: nodeID, subscribed: make(chan struct{}), done: make(chan struct{})}
	c.sessions[nodeID] = append(c.sessions[nodeID], s)
	c.mu.Unlock()
	c.connected <- s
	return s, nil
}

// Commission blocks until the test answers on commissionResults, or the
// context ends; a success adds the node to the store's records.
func (c *fakeController) Commission(ctx context.Context, payload encoding.OnboardingPayload, wifi *WiFiCredentials) (NodeRecord, error) {
	c.mu.Lock()
	c.commissions = append(c.commissions, commissionCall{discriminator: uint16(payload.Discriminator()), wifi: wifi})
	results := c.commissionResults
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return NodeRecord{}, ctx.Err()
	case r := <-results:
		if r.err != nil {
			return NodeRecord{}, r.err
		}
		c.mu.Lock()
		c.records = append(c.records, r.rec)
		c.mu.Unlock()
		return r.rec, nil
	}
}

type commissionCall struct {
	discriminator uint16
	wifi          *WiFiCredentials
}

type commissionResult struct {
	rec NodeRecord
	err error
}

func (c *fakeController) connectCount(nodeID uint64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connects[nodeID]
}

// fakeSession is a session whose reports and errors the test sends.
type fakeSession struct {
	nodeID     uint64
	subscribed chan struct{}

	mu        sync.Mutex
	handler   func(Report)
	onError   func(error)
	done      chan struct{}
	err       error
	ended     bool
	closed    bool
	subClose  bool
	invokes   []invokeCall
	answer    InvokeStatus
	invokeErr error
}

type invokeCall struct {
	endpoint         uint16
	cluster, command uint32
	fields           []byte
}

func (s *fakeSession) Invoke(endpoint uint16, cluster, command uint32, fields []byte) (InvokeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invokes = append(s.invokes, invokeCall{endpoint, cluster, command, fields})
	return s.answer, s.invokeErr
}

func (s *fakeSession) SubscribeAll(_ context.Context, handler func(Report), onError func(error)) (Subscription, error) {
	s.mu.Lock()
	s.handler, s.onError = handler, onError
	s.mu.Unlock()
	close(s.subscribed)
	return (*fakeSubscription)(s), nil
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeSession) report(r Report) {
	<-s.subscribed
	s.mu.Lock()
	h := s.handler
	s.mu.Unlock()
	h(r)
}

func (s *fakeSession) fail(err error) {
	<-s.subscribed
	s.mu.Lock()
	f := s.onError
	s.mu.Unlock()
	f(err)
}

// end ends the subscription for good, as a session closed by the node does.
func (s *fakeSession) end(err error) {
	<-s.subscribed
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.ended, s.err = true, err
		close(s.done)
	}
}

func (s *fakeSession) isClosed() (session, subscription bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed, s.subClose
}

type fakeSubscription fakeSession

func (s *fakeSubscription) Done() <-chan struct{} { return s.done }

func (s *fakeSubscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *fakeSubscription) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subClose = true
	return nil
}

func newHub(t *testing.T, ctl Controller) *Hub {
	t.Helper()
	h, err := New(Config{
		Controller: ctl,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		RetryMin:   5 * time.Millisecond,
		RetryMax:   20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Stop() })
	return h
}

func nextSession(t *testing.T, c *fakeController) *fakeSession {
	t.Helper()
	select {
	case s := <-c.connected:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("the hub did not connect")
		return nil
	}
}

// waitFor polls until the node satisfies ok.
func waitFor(t *testing.T, h *Hub, id uint64, what string, ok func(Node) bool) Node {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n, found := h.Node(id)
		if found && ok(n) {
			return n
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: node = %+v", what, n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func onOff(endpoint uint16, value bool) AttributeReport {
	return AttributeReport{Path: AttributePath{Endpoint: endpoint, Cluster: 0x0006, Attribute: 0}, Value: value}
}

func TestNewValidatesItsConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("a hub without a controller")
	}
	if _, err := New(Config{Controller: newFakeController(), VendorID: 0xFFF5}); !errors.Is(err, ErrInvalidVendorID) {
		t.Fatalf("vendor ID 0xFFF5: %v", err)
	}
}

func TestStartCreatesTheFabricOfANewStore(t *testing.T) {
	ctl := newFakeController()
	ctl.fabric = nil
	h := newHub(t, ctl)
	if _, ok := h.Fabric(); ok {
		t.Fatal("a fabric before Start")
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	fabric, ok := h.Fabric()
	if !ok || fabric.FabricID != 0xC0FFEE || fabric.AdminVendorID != DefaultVendorID {
		t.Fatalf("fabric = %+v, %v", fabric, ok)
	}
	if len(ctl.created) != 1 || ctl.created[0] != DefaultVendorID {
		t.Fatalf("created = %v", ctl.created)
	}
	if len(h.Nodes()) != 0 {
		t.Fatal("a new fabric has nodes")
	}
}

func TestStartKeepsAnExistingFabric(t *testing.T) {
	ctl := newFakeController()
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if fabric, _ := h.Fabric(); fabric.FabricID != 0xFAB {
		t.Fatalf("fabric = %+v", fabric)
	}
	if len(ctl.created) != 0 {
		t.Fatal("an existing fabric was replaced")
	}
}

func TestStartRefusesNodesWithoutAFabric(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7})
	ctl.fabric = nil
	h := newHub(t, ctl)
	if err := h.Start(); !errors.Is(err, ErrOrphanedNodes) {
		t.Fatalf("Start() = %v", err)
	}
	if len(ctl.created) != 0 {
		t.Fatal("a fabric was created over orphaned nodes")
	}
	if !ctl.stopped {
		t.Fatal("the controller was left running")
	}
}

func TestNodeStateFollowsReports(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7, VendorID: 0x1217, ProductID: 0x0001})
	h := newHub(t, ctl)

	var mu sync.Mutex
	var changes []Change
	remove := h.Listen(func(c Change) {
		mu.Lock()
		changes = append(changes, c)
		mu.Unlock()
	})
	defer remove()

	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if n, ok := h.Node(7); !ok || n.VendorID != 0x1217 || len(n.Attributes) != 0 {
		t.Fatalf("node before any report = %+v, %v", n, ok)
	}
	s := nextSession(t, ctl)

	status := uint8(0x7E)
	s.report(Report{Priming: true, Attributes: []AttributeReport{
		{Path: AttributePath{Endpoint: 1, Cluster: 0x0008, Attribute: 0}, Value: uint64(254)},
		onOff(1, false),
		{Path: AttributePath{Endpoint: 0, Cluster: 0x0028, Attribute: 5}, Value: "lamp"},
		{Path: AttributePath{Endpoint: 0, Cluster: 0x003E, Attribute: 0}, Status: &status},
	}})
	n := waitFor(t, h, 7, "online after the priming report", func(n Node) bool { return n.Status == StatusOnline })
	if n.Error != "" || n.LastReportAt.IsZero() || len(n.Attributes) != 4 {
		t.Fatalf("node = %+v", n)
	}
	// Ordered by endpoint, cluster and attribute.
	if n.Attributes[0].Cluster != 0x0028 || n.Attributes[1].Cluster != 0x003E || n.Attributes[2].Cluster != 0x0006 || n.Attributes[3].Cluster != 0x0008 {
		t.Fatalf("attribute order = %+v", n.Attributes)
	}
	if n.Attributes[1].Status == nil || *n.Attributes[1].Status != 0x7E || n.Attributes[1].Value != nil {
		t.Fatalf("status attribute = %+v", n.Attributes[1])
	}

	s.report(Report{Attributes: []AttributeReport{onOff(1, true)}})
	n, _ = h.Node(7)
	if len(n.Attributes) != 4 || n.Attributes[2].Value != true {
		t.Fatalf("after a change: %+v", n.Attributes)
	}

	// A new priming report replaces the state: what it leaves out is gone.
	s.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, false)}})
	n, _ = h.Node(7)
	if len(n.Attributes) != 1 || n.Attributes[0].Value != false {
		t.Fatalf("after a second priming report: %+v", n.Attributes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 4 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].Kind != ChangeStatus || changes[0].Status != StatusOnline || changes[0].NodeID != 7 {
		t.Fatalf("first change = %+v", changes[0])
	}
	if changes[1].Kind != ChangeAttributes || !changes[1].Priming || len(changes[1].Attributes) != 4 {
		t.Fatalf("second change = %+v", changes[1])
	}
	if changes[2].Kind != ChangeAttributes || changes[2].Priming || len(changes[2].Attributes) != 1 || changes[2].Attributes[0].Value != true {
		t.Fatalf("third change = %+v", changes[2])
	}
}

func TestNodeGoesOfflineAndComesBack(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7})
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	s := nextSession(t, ctl)
	s.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, true)}})
	waitFor(t, h, 7, "online", func(n Node) bool { return n.Status == StatusOnline })

	// The subscription is lost and re-establishes itself on the session.
	s.fail(errors.New("subscription lost"))
	n := waitFor(t, h, 7, "offline after the loss", func(n Node) bool { return n.Status == StatusOffline })
	if n.Error != "subscription lost" || len(n.Attributes) != 1 {
		t.Fatalf("offline node = %+v", n)
	}
	s.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, false)}})
	n = waitFor(t, h, 7, "online again", func(n Node) bool { return n.Status == StatusOnline })
	if n.Error != "" || n.Attributes[0].Value != false {
		t.Fatalf("node = %+v", n)
	}
	if got := ctl.connectCount(7); got != 1 {
		t.Fatalf("connects = %d; the session was replaced", got)
	}
}

func TestNodeIsRetriedUntilItConnects(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7})
	ctl.connectErrs[7] = []error{errors.New("no such node"), errors.New("no such node")}
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	n := waitFor(t, h, 7, "offline while unreachable", func(n Node) bool { return n.Status == StatusOffline })
	if n.Error != "no such node" {
		t.Fatalf("error = %q", n.Error)
	}
	s := nextSession(t, ctl)
	if got := ctl.connectCount(7); got != 3 {
		t.Fatalf("connects = %d", got)
	}
	s.report(Report{Priming: true})
	waitFor(t, h, 7, "online", func(n Node) bool { return n.Status == StatusOnline })
}

func TestEndedSubscriptionIsReplaced(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7})
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	first := nextSession(t, ctl)
	first.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, true)}})
	first.end(errors.New("session closed"))

	second := nextSession(t, ctl)
	if closed, subClosed := first.isClosed(); !closed || !subClosed {
		t.Fatalf("the ended session was left open: session %v, subscription %v", closed, subClosed)
	}
	second.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, false)}})
	n := waitFor(t, h, 7, "online over the new session", func(n Node) bool { return n.Status == StatusOnline && n.Attributes[0].Value == false })
	if n.Error != "" {
		t.Fatalf("node = %+v", n)
	}
}

func TestStopClosesTheSessions(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7}, NodeRecord{NodeID: 9})
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	a, b := nextSession(t, ctl), nextSession(t, ctl)
	<-a.subscribed
	<-b.subscribed
	if nodes := h.Nodes(); len(nodes) != 2 || nodes[0].ID != 7 || nodes[1].ID != 9 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*fakeSession{a, b} {
		if closed, subClosed := s.isClosed(); !closed || !subClosed {
			t.Fatalf("node %d left open: session %v, subscription %v", s.nodeID, closed, subClosed)
		}
	}
	if !ctl.stopped {
		t.Fatal("the controller was left running")
	}
	if err := h.Stop(); err != nil {
		t.Fatalf("second Stop() = %v", err)
	}
	// A hub which is started again connects anew.
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	nextSession(t, ctl)
	nextSession(t, ctl)
	if n, _ := h.Node(7); n.Status != StatusConnecting {
		t.Fatalf("node after a restart = %+v", n)
	}
}
