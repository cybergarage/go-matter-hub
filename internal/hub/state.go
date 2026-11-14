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

package hub

import (
	"sort"
	"time"
)

// Status is the state of the hub's connection to a node.
type Status string

const (
	// StatusConnecting is a node the hub has not reached yet since it
	// started.
	StatusConnecting Status = "connecting"
	// StatusOnline is a node whose subscription delivers reports.
	StatusOnline Status = "online"
	// StatusOffline is a node the hub cannot reach, or whose subscription
	// is lost; the hub keeps trying.
	StatusOffline Status = "offline"
)

// Attribute is the last value a node reported for an attribute.
type Attribute struct {
	AttributePath
	// Value is the value as jsonValue describes it; nil is null, and also
	// the value of an attribute which reported a status instead.
	Value any `json:"value"`
	// Status is the Interaction Model status the node reported in place
	// of a value, such as UnsupportedAccess.
	Status *uint8 `json:"status,omitempty"`
	// UpdatedAt is when the hub received the value.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Node is the state of a commissioned node as the hub knows it.
type Node struct {
	ID             uint64    `json:"-"`
	VendorID       uint16    `json:"vendorId"`
	ProductID      uint16    `json:"productId"`
	CommissionedAt time.Time `json:"commissionedAt"`
	Status         Status    `json:"status"`
	// Error is why the node is offline, or why the last attempt to reach
	// it failed; empty while it is online.
	Error string `json:"error,omitempty"`
	// LastReportAt is when the last report arrived; zero when none has.
	LastReportAt time.Time `json:"lastReportAt"`
	// Attributes are the attributes reported so far, ordered by endpoint,
	// cluster and attribute. They are kept while the node is offline.
	Attributes []Attribute `json:"attributes"`
}

// ChangeKind says what a Change reports.
type ChangeKind string

const (
	// ChangeStatus is a change of Status or Error.
	ChangeStatus ChangeKind = "status"
	// ChangeAttributes is a report of attribute values.
	ChangeAttributes ChangeKind = "attributes"
	// ChangeNodeAdded is a node which joined the hub, with its state in
	// Node.
	ChangeNodeAdded ChangeKind = "node"
	// ChangeCommissioning is the progress of a commissioning, in
	// Commissioning.
	ChangeCommissioning ChangeKind = "commissioning"
	// ChangeNodeRemoved is a node which was removed from the hub.
	ChangeNodeRemoved ChangeKind = "removed"
)

// Change is told to the listeners of a hub when the state of a node changes.
type Change struct {
	NodeID uint64
	Kind   ChangeKind
	Status Status
	Error  string
	// Attributes are the attributes of the report, for ChangeAttributes.
	Attributes []Attribute
	// Priming is true for the report which opens a subscription and holds
	// every attribute of the node.
	Priming bool
	// Node is the state of a node which joined the hub, for
	// ChangeNodeAdded.
	Node *Node
	// Commissioning is the commissioning, for ChangeCommissioning.
	Commissioning *Commissioning
}

func sortAttributes(attrs []Attribute) {
	sort.Slice(attrs, func(i, j int) bool {
		a, b := attrs[i].AttributePath, attrs[j].AttributePath
		if a.Endpoint != b.Endpoint {
			return a.Endpoint < b.Endpoint
		}
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		return a.Attribute < b.Attribute
	})
}
