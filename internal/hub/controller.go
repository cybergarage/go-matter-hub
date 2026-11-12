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

// Package hub is the core of the Matter hub: it keeps a session and a
// subscription with every node commissioned into the hub's fabric, and the
// state the nodes report.
package hub

import (
	"context"
	"time"
)

// Controller is what the hub needs from a Matter controller. The go-matter
// implementation is MatterController.
type Controller interface {
	// Start opens the store and starts discovery.
	Start() error
	// Stop stops discovery.
	Stop() error
	// Fabric returns the fabric of the hub. ok is false when the store has
	// no fabric yet.
	Fabric() (fabric Fabric, ok bool, err error)
	// CreateFabric creates the fabric of a store which has none, with the
	// administrator vendor ID, and makes the controller use it. It never
	// replaces a fabric.
	CreateFabric(vendorID uint16) (Fabric, error)
	// Nodes returns the nodes commissioned into the fabric.
	Nodes() ([]NodeRecord, error)
	// Connect establishes a session with a commissioned node.
	Connect(ctx context.Context, nodeID uint64) (Session, error)
}

// Fabric identifies the fabric of the hub.
type Fabric struct {
	FabricID           uint64
	CompressedFabricID uint64
	AdminNodeID        uint64
	AdminVendorID      uint16
}

// NodeRecord is a commissioned node, as the store keeps it.
type NodeRecord struct {
	NodeID         uint64
	VendorID       uint16
	ProductID      uint16
	CommissionedAt time.Time
}

// Session is a session with a node.
type Session interface {
	// SubscribeAll subscribes to every attribute of the node. handler
	// receives the reports, the first of which is the priming report with
	// the current value of every attribute; onError receives the errors of
	// a subscription which is being re-established.
	SubscribeAll(ctx context.Context, handler func(Report), onError func(error)) (Subscription, error)
	// Close closes the session and its subscriptions.
	Close() error
}

// Subscription is a subscription to the attributes of a node.
type Subscription interface {
	// Done is closed when the subscription has ended for good.
	Done() <-chan struct{}
	// Err returns why the subscription ended.
	Err() error
	// Close ends the subscription.
	Close() error
}

// AttributePath locates an attribute on a node.
type AttributePath struct {
	Endpoint  uint16 `json:"endpoint"`
	Cluster   uint32 `json:"cluster"`
	Attribute uint32 `json:"attribute"`
}

// AttributeReport is one attribute of a report: its value, or the status
// with which the node refused to report it.
type AttributeReport struct {
	Path AttributePath
	// Value is the value in a form encoding/json can write; see
	// jsonValue.
	Value any
	// DataVersion is the version of the cluster's data, when reported.
	DataVersion    uint32
	HasDataVersion bool
	// Status is the Interaction Model status of an attribute the node
	// did not report a value for; Value is then nil.
	Status *uint8
}

// Report is a report of a subscription.
type Report struct {
	// Priming marks the first report of a subscription, which carries the
	// current value of every attribute rather than changes.
	Priming    bool
	Attributes []AttributeReport
}
