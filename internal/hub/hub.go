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
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

const (
	// DefaultVendorID is the vendor ID a new fabric gives its administrator:
	// the first of the test vendor IDs. 2.5.2. Vendor Identifier.
	DefaultVendorID = 0xFFF1

	maxVendorID = 0xFFF4

	defaultRetryMin       = 2 * time.Second
	defaultRetryMax       = 2 * time.Minute
	defaultConnectTimeout = 30 * time.Second
)

var (
	// ErrOrphanedNodes is returned when the store holds commissioned nodes
	// but no fabric: a new fabric would not reach them.
	ErrOrphanedNodes = errors.New("the store holds commissioned nodes but no fabric")
	// ErrInvalidVendorID is returned for an administrator vendor ID outside
	// 0x0001..0xFFF4.
	ErrInvalidVendorID = errors.New("invalid administrator vendor ID")
	// ErrNotStarted is returned by operations which need a started hub.
	ErrNotStarted = errors.New("the hub is not started")
)

// Config configures a Hub.
type Config struct {
	// Controller is the Matter controller of the hub, with a store of its
	// own: the hub does not share the fabric of another controller.
	Controller Controller
	// VendorID is the administrator vendor ID of a fabric the hub creates;
	// zero means DefaultVendorID. An existing fabric keeps its own.
	VendorID uint16
	// Logger receives the hub's log; nil means slog.Default().
	Logger *slog.Logger
	// RetryMin and RetryMax bound the wait between attempts to reach a
	// node; zero means 2 seconds and 2 minutes.
	RetryMin, RetryMax time.Duration
	// ConnectTimeout bounds one attempt to connect to a node; zero means
	// 30 seconds.
	ConnectTimeout time.Duration
}

// Hub connects to the commissioned nodes of its fabric and keeps their
// state.
type Hub struct {
	cfg Config
	log *slog.Logger
	ctl Controller

	mu        sync.Mutex
	started   bool
	fabric    Fabric
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	nodes     map[uint64]*nodeWorker
	listeners map[int]func(Change)
	nextID    int
}

// New returns a hub. Nothing is opened until Start.
func New(cfg Config) (*Hub, error) {
	if cfg.Controller == nil {
		return nil, errors.New("hub: a controller is required")
	}
	if cfg.VendorID == 0 {
		cfg.VendorID = DefaultVendorID
	}
	if cfg.VendorID > maxVendorID {
		return nil, fmt.Errorf("%w: 0x%04X", ErrInvalidVendorID, cfg.VendorID)
	}
	if cfg.RetryMin <= 0 {
		cfg.RetryMin = defaultRetryMin
	}
	if cfg.RetryMax < cfg.RetryMin {
		cfg.RetryMax = max(defaultRetryMax, cfg.RetryMin)
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		cfg:       cfg,
		log:       logger,
		ctl:       cfg.Controller,
		nodes:     map[uint64]*nodeWorker{},
		listeners: map[int]func(Change){},
	}, nil
}

// Start starts the controller, creates the fabric of a store which is new,
// and connects to every node commissioned into the fabric. It returns once
// the connections are under way; a node which cannot be reached is retried
// until Stop.
func (h *Hub) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return nil
	}
	if err := h.ctl.Start(); err != nil {
		return fmt.Errorf("hub: start the controller: %w", err)
	}
	fabric, records, err := h.openFabric()
	if err != nil {
		_ = h.ctl.Stop()
		return err
	}
	h.fabric = fabric

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.started = true
	// A hub which is started again connects anew.
	h.nodes = make(map[uint64]*nodeWorker, len(records))
	for _, rec := range records {
		if _, ok := h.nodes[rec.NodeID]; ok {
			continue
		}
		w := newNodeWorker(h, rec)
		h.nodes[rec.NodeID] = w
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			w.run(ctx)
		}()
	}
	h.log.Info("hub started", "fabric", hexID(fabric.FabricID), "nodes", len(h.nodes))
	return nil
}

// openFabric returns the fabric of the controller and its nodes, creating
// the fabric when the store has neither.
func (h *Hub) openFabric() (Fabric, []NodeRecord, error) {
	fabric, ok, err := h.ctl.Fabric()
	if err != nil {
		return Fabric{}, nil, fmt.Errorf("hub: read the fabric: %w", err)
	}
	records, err := h.ctl.Nodes()
	if err != nil {
		return Fabric{}, nil, fmt.Errorf("hub: read the commissioned nodes: %w", err)
	}
	if ok {
		return fabric, records, nil
	}
	if len(records) != 0 {
		return Fabric{}, nil, fmt.Errorf("hub: %w", ErrOrphanedNodes)
	}
	fabric, err = h.ctl.CreateFabric(h.cfg.VendorID)
	if err != nil {
		return Fabric{}, nil, fmt.Errorf("hub: create the fabric: %w", err)
	}
	h.log.Info("created a new fabric", "fabric", hexID(fabric.FabricID), "vendor", fmt.Sprintf("0x%04X", fabric.AdminVendorID))
	return fabric, nil, nil
}

// Stop closes the sessions with the nodes and stops the controller.
func (h *Hub) Stop() error {
	h.mu.Lock()
	if !h.started {
		h.mu.Unlock()
		return nil
	}
	h.started = false
	cancel := h.cancel
	h.mu.Unlock()

	cancel()
	h.wg.Wait()
	return h.ctl.Stop()
}

// Fabric returns the fabric of the hub; ok is false before Start.
func (h *Hub) Fabric() (Fabric, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fabric, h.fabric.FabricID != 0
}

// Nodes returns the state of every node, ordered by node ID.
func (h *Hub) Nodes() []Node {
	h.mu.Lock()
	workers := make([]*nodeWorker, 0, len(h.nodes))
	for _, w := range h.nodes {
		workers = append(workers, w)
	}
	h.mu.Unlock()
	nodes := make([]Node, 0, len(workers))
	for _, w := range workers {
		nodes = append(nodes, w.snapshot())
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}

// Node returns the state of a node.
func (h *Hub) Node(id uint64) (Node, bool) {
	h.mu.Lock()
	w, ok := h.nodes[id]
	h.mu.Unlock()
	if !ok {
		return Node{}, false
	}
	return w.snapshot(), true
}

// Listen registers a function called for every change of a node's state,
// and returns a function which removes it. The changes of one node are
// told in order, from the goroutine which received them; the function has
// to return promptly.
func (h *Hub) Listen(fn func(Change)) (remove func()) {
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.listeners[id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.listeners, id)
		h.mu.Unlock()
	}
}

func (h *Hub) notify(c Change) {
	h.mu.Lock()
	ids := make([]int, 0, len(h.listeners))
	for id := range h.listeners {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fns := make([]func(Change), 0, len(ids))
	for _, id := range ids {
		fns = append(fns, h.listeners[id])
	}
	h.mu.Unlock()
	for _, fn := range fns {
		fn(c)
	}
}

func hexID(id uint64) string {
	return fmt.Sprintf("%016X", id)
}
