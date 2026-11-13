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
	"fmt"
	"sync"

	"github.com/cybergarage/go-matter/matter/datamodel"
	"github.com/cybergarage/go-matter/matter/datamodel/command"
	"github.com/cybergarage/go-matter/matter/protocol/im"
)

var (
	// ErrUnknownNode is returned for a node which is not commissioned into
	// the hub's fabric.
	ErrUnknownNode = errors.New("no such node")
	// ErrNodeOffline is returned when the hub has no session with the node.
	ErrNodeOffline = errors.New("the node is offline")
	// ErrTimedCommand is returned for a command which has to be sent as a
	// timed invoke, which the hub does not send yet. 8.7.2. Timed
	// Interaction.
	ErrTimedCommand = errors.New("the command requires a timed invoke, which the hub does not send yet")
	// ErrCommandFailed is returned when the node answered with a status
	// other than success; errors.As finds a *CommandError.
	ErrCommandFailed = errors.New("the node refused the command")
)

// CommandError is the status a node refused a command with.
type CommandError struct {
	Status InvokeStatus
}

func (e *CommandError) Error() string {
	name := imStatusNames[e.Status.IMStatus]
	if name == "" {
		name = "status"
	}
	msg := fmt.Sprintf("the node refused the command: %s (0x%02X)", name, e.Status.IMStatus)
	if e.Status.ClusterStatus != 0 {
		msg += fmt.Sprintf(", cluster status 0x%02X", e.Status.ClusterStatus)
	}
	return msg
}

// Is makes errors.Is(err, ErrCommandFailed) true.
func (e *CommandError) Is(target error) bool { return target == ErrCommandFailed }

// imStatusNames names the Interaction Model statuses a command is most
// often refused with. 8.10. Status Code Table.
var imStatusNames = map[uint8]string{
	0x01: "FAILURE",
	0x7E: "UNSUPPORTED_ACCESS",
	0x7F: "UNSUPPORTED_ENDPOINT",
	0x80: "INVALID_ACTION",
	0x81: "UNSUPPORTED_COMMAND",
	0x85: "CONSTRAINT_ERROR",
	0x87: "INVALID_COMMAND",
	0x89: "BUSY",
	0xC3: "UNSUPPORTED_CLUSTER",
	0xC6: "NEEDS_TIMED_INTERACTION",
	0xCB: "INVALID_IN_STATE",
}

// Command is a command to invoke on a node.
type Command struct {
	Endpoint uint16
	Cluster  uint32
	Command  uint32
	// Fields are the values of the command's fields by name, in a form
	// command.Schema.Encode accepts.
	Fields map[string]any
}

var (
	catalogOnce sync.Once
	catalog     *datamodel.Catalog
	catalogErr  error
)

func loadCatalog() (*datamodel.Catalog, error) {
	catalogOnce.Do(func() { catalog, catalogErr = datamodel.Load() })
	return catalog, catalogErr
}

// EncodeCommand checks a command against its definition in the data model
// catalog and encodes its fields. The errors of the command package
// (command.ErrUnknownCommand, ErrMissingField, ...) say what is wrong.
func EncodeCommand(c Command) ([]byte, error) {
	cat, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	schema, err := command.New(cat, im.ClusterID(c.Cluster), im.CommandID(c.Command))
	if err != nil {
		return nil, err
	}
	if schema.Timed {
		return nil, fmt.Errorf("%w: %s %s", ErrTimedCommand, schema.ClusterName, schema.CommandName)
	}
	return schema.Encode(c.Fields)
}

// Invoke invokes a command on a node over the session the hub keeps with
// it. The new state reaches the hub through the subscription, as any
// other change does.
func (h *Hub) Invoke(nodeID uint64, c Command) error {
	fields, err := EncodeCommand(c)
	if err != nil {
		return err
	}
	h.mu.Lock()
	w, ok := h.nodes[nodeID]
	h.mu.Unlock()
	if !ok {
		return ErrUnknownNode
	}
	sess := w.currentSession()
	if sess == nil {
		return ErrNodeOffline
	}
	status, err := sess.Invoke(c.Endpoint, c.Cluster, c.Command, fields)
	if err != nil {
		return err
	}
	if !status.Success() {
		return &CommandError{Status: status}
	}
	h.log.Info("command invoked", "node", hexID(nodeID), "endpoint", c.Endpoint,
		"cluster", fmt.Sprintf("0x%04X", c.Cluster), "command", fmt.Sprintf("0x%02X", c.Command))
	return nil
}
