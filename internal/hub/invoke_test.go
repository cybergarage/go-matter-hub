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
	"encoding/hex"
	"errors"
	"testing"

	"github.com/cybergarage/go-matter/matter/datamodel/command"
)

const (
	onOffClusterID    = 0x0006
	levelClusterID    = 0x0008
	doorLockClusterID = 0x0101
)

func onlineHub(t *testing.T) (*Hub, *fakeSession) {
	t.Helper()
	ctl := newFakeController(NodeRecord{NodeID: 7})
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	s := nextSession(t, ctl)
	s.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, false)}})
	waitFor(t, h, 7, "online", func(n Node) bool { return n.Status == StatusOnline })
	return h, s
}

func TestInvokeEncodesTheFieldsFromTheCatalog(t *testing.T) {
	h, s := onlineHub(t)
	err := h.Invoke(7, Command{Endpoint: 1, Cluster: levelClusterID, Command: 0x04, Fields: map[string]any{
		"Level": 127, "TransitionTime": 5, "OptionsMask": 0, "OptionsOverride": 0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Invoke(7, Command{Endpoint: 1, Cluster: onOffClusterID, Command: 0x01}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.invokes) != 2 {
		t.Fatalf("invokes = %+v", s.invokes)
	}
	level := s.invokes[0]
	// MoveToLevelWithOnOff: Level 127, TransitionTime 5, OptionsMask 0,
	// OptionsOverride 0 in a CommandFields structure.
	if level.endpoint != 1 || level.cluster != levelClusterID || level.command != 0x04 || hex.EncodeToString(level.fields) != "350124007f240105240200240300"+"18" {
		t.Fatalf("level invoke = %+v (%x)", level, level.fields)
	}
	if on := s.invokes[1]; on.cluster != onOffClusterID || on.command != 0x01 || hex.EncodeToString(on.fields) != "350118" {
		t.Fatalf("on invoke = %+v (%x)", on, on.fields)
	}
}

func TestInvokeErrors(t *testing.T) {
	h, s := onlineHub(t)
	tests := []struct {
		name string
		node uint64
		cmd  Command
		want error
	}{
		{"unknown node", 9, Command{Endpoint: 1, Cluster: onOffClusterID, Command: 0x01}, ErrUnknownNode},
		{"command outside the catalog", 7, Command{Endpoint: 1, Cluster: 0x0003, Command: 0x00}, command.ErrUnknownCommand},
		{"missing field", 7, Command{Endpoint: 1, Cluster: levelClusterID, Command: 0x04, Fields: map[string]any{"Level": 1}}, command.ErrMissingField},
		{"value out of range", 7, Command{Endpoint: 1, Cluster: levelClusterID, Command: 0x04, Fields: map[string]any{"Level": 255, "TransitionTime": 0, "OptionsMask": 0, "OptionsOverride": 0}}, command.ErrInvalidValue},
		{"timed command", 7, Command{Endpoint: 1, Cluster: doorLockClusterID, Command: 0x00}, ErrTimedCommand},
	}
	for _, tt := range tests {
		if err := h.Invoke(tt.node, tt.cmd); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	s.mu.Lock()
	sent := len(s.invokes)
	s.mu.Unlock()
	if sent != 0 {
		t.Fatalf("%d invalid commands reached the node", sent)
	}

	// The node refuses the command.
	s.mu.Lock()
	s.answer = InvokeStatus{IMStatus: 0x81}
	s.mu.Unlock()
	err := h.Invoke(7, Command{Endpoint: 1, Cluster: onOffClusterID, Command: 0x01})
	var refused *CommandError
	if !errors.Is(err, ErrCommandFailed) || !errors.As(err, &refused) || refused.Status.IMStatus != 0x81 {
		t.Fatalf("refused command: %v", err)
	}
	if got := err.Error(); got != "the node refused the command: UNSUPPORTED_COMMAND (0x81)" {
		t.Fatalf("message = %q", got)
	}

	// The session fails.
	s.mu.Lock()
	s.answer, s.invokeErr = InvokeStatus{}, errors.New("response timeout")
	s.mu.Unlock()
	if err := h.Invoke(7, Command{Endpoint: 1, Cluster: onOffClusterID, Command: 0x01}); err == nil || err.Error() != "response timeout" {
		t.Fatalf("session error: %v", err)
	}
}

func TestInvokeNeedsASession(t *testing.T) {
	ctl := newFakeController(NodeRecord{NodeID: 7})
	ctl.connectErrs[7] = []error{errors.New("unreachable"), errors.New("unreachable"), errors.New("unreachable")}
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if err := h.Invoke(7, Command{Endpoint: 1, Cluster: onOffClusterID, Command: 0x01}); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("Invoke() while connecting = %v", err)
	}

	// Once the subscription has ended, the session is no longer used.
	s := nextSession(t, ctl)
	s.report(Report{Priming: true})
	waitFor(t, h, 7, "online", func(n Node) bool { return n.Status == StatusOnline })
	// The node stays unreachable once the subscription has ended.
	ctl.mu.Lock()
	for range 1000 {
		ctl.connectErrs[7] = append(ctl.connectErrs[7], errors.New("unreachable"))
	}
	ctl.mu.Unlock()
	s.end(errors.New("session closed"))
	waitFor(t, h, 7, "offline", func(n Node) bool { return n.Status == StatusOffline })
	if err := h.Invoke(7, Command{Endpoint: 1, Cluster: onOffClusterID, Command: 0x01}); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("Invoke() after the subscription ended = %v", err)
	}
}
