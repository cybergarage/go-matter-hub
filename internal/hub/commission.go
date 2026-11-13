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
	"time"
)

// CommissioningTimeout bounds one commissioning: discovery, PASE,
// attestation, the network and the operational credentials, and CASE.
const CommissioningTimeout = 3 * time.Minute

var (
	// ErrCommissioningBusy is returned while another commissioning runs:
	// the hub commissions one device at a time, since they share the
	// Bluetooth adapter.
	ErrCommissioningBusy = errors.New("another device is being commissioned")
	// ErrDeviceNotFound is returned when no device matching the setup code
	// advertised itself, over Bluetooth or on the network.
	ErrDeviceNotFound = errors.New("no device with this setup code was found")
	// ErrWiFiRequired is returned when the device was found over Bluetooth
	// only, and no Wi-Fi credentials were given for it to join a network.
	ErrWiFiRequired = errors.New("the device was found over Bluetooth only: give the Wi-Fi network it is to join")
	// ErrNotInCommissioningMode is returned when the device is on the
	// network, but has no commissioning window open.
	ErrNotInCommissioningMode = errors.New("the device is on the network but not in commissioning mode: open a commissioning window on it, or reset it to factory defaults")
)

// CommissioningState is the state of a commissioning.
type CommissioningState string

const (
	// CommissioningRunning is a commissioning in progress.
	CommissioningRunning CommissioningState = "running"
	// CommissioningSucceeded is a commissioning which added a node.
	CommissioningSucceeded CommissioningState = "succeeded"
	// CommissioningFailed is a commissioning which did not.
	CommissioningFailed CommissioningState = "failed"
)

// Commissioning is a commissioning started from the hub, as its clients
// see it. It holds neither the setup code nor the Wi-Fi password.
type Commissioning struct {
	ID    int                `json:"id"`
	State CommissioningState `json:"state"`
	// OverBluetooth is true when Wi-Fi credentials were given, so that a
	// device found over Bluetooth could join the network.
	OverBluetooth bool      `json:"overBluetooth"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt,omitzero"`
	// NodeID is the node the device became, once commissioned.
	NodeID uint64 `json:"-"`
	// Error and ErrorCode say why a commissioning failed. ErrorCode is one
	// of not_found, wifi_required, not_in_commissioning_mode, timeout and
	// failed.
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func commissioningErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrWiFiRequired):
		return "wifi_required"
	case errors.Is(err, ErrNotInCommissioningMode):
		return "not_in_commissioning_mode"
	case errors.Is(err, ErrDeviceNotFound):
		return "not_found"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "failed"
	}
}

// StartCommissioning starts commissioning the device with the setup code,
// in the background, and returns at once. wifi is nil for a device which is
// already on the network. The progress is told to the listeners as
// ChangeCommissioning changes, and a commissioned device joins the hub as a
// new node at once.
func (h *Hub) StartCommissioning(code string, wifi *WiFiCredentials) (Commissioning, error) {
	payload, err := ParseSetupCode(code)
	if err != nil {
		return Commissioning{}, err
	}
	h.mu.Lock()
	if !h.started {
		h.mu.Unlock()
		return Commissioning{}, ErrNotStarted
	}
	if h.commissioning != nil && h.commissioning.State == CommissioningRunning {
		h.mu.Unlock()
		return Commissioning{}, ErrCommissioningBusy
	}
	h.nextCommissioning++
	job := &Commissioning{
		ID:            h.nextCommissioning,
		State:         CommissioningRunning,
		OverBluetooth: wifi != nil,
		StartedAt:     time.Now(),
	}
	h.commissioning = job
	ctx := h.ctx
	snapshot := *job
	h.wg.Add(1)
	h.mu.Unlock()

	h.log.Info("commissioning started", "id", job.ID, "bluetooth", job.OverBluetooth)
	h.notify(Change{Kind: ChangeCommissioning, Commissioning: &snapshot})

	go func() {
		defer h.wg.Done()
		ctx, cancel := context.WithTimeout(ctx, CommissioningTimeout)
		defer cancel()
		rec, err := h.ctl.Commission(ctx, payload, wifi)
		h.finishCommissioning(job, rec, err)
	}()
	return snapshot, nil
}

func (h *Hub) finishCommissioning(job *Commissioning, rec NodeRecord, err error) {
	h.mu.Lock()
	job.FinishedAt = time.Now()
	var added *nodeWorker
	if err != nil {
		job.State = CommissioningFailed
		job.Error = err.Error()
		job.ErrorCode = commissioningErrorCode(err)
	} else {
		job.State = CommissioningSucceeded
		job.NodeID = rec.NodeID
		added = h.addNodeLocked(rec)
	}
	snapshot := *job
	h.mu.Unlock()

	if err != nil {
		h.log.Warn("commissioning failed", "id", job.ID, "code", job.ErrorCode, "error", job.Error)
	} else {
		h.log.Info("commissioning succeeded", "id", job.ID, "node", hexID(rec.NodeID))
	}
	if added != nil {
		node := added.snapshot()
		h.notify(Change{NodeID: rec.NodeID, Kind: ChangeNodeAdded, Status: node.Status, Node: &node})
	}
	h.notify(Change{NodeID: snapshot.NodeID, Kind: ChangeCommissioning, Commissioning: &snapshot})
}

// Commissioning returns the latest commissioning; ok is false when there
// has been none since the hub started.
func (h *Hub) Commissioning() (Commissioning, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.commissioning == nil {
		return Commissioning{}, false
	}
	return *h.commissioning, true
}

// addNodeLocked starts keeping a session with a node, unless the hub keeps
// one already; it returns the new worker, or nil. h.mu is held.
func (h *Hub) addNodeLocked(rec NodeRecord) *nodeWorker {
	if !h.started {
		return nil
	}
	if _, ok := h.nodes[rec.NodeID]; ok {
		return nil
	}
	w := newNodeWorker(h, rec)
	h.nodes[rec.NodeID] = w
	ctx := h.ctx
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		w.run(ctx)
	}()
	return w
}

// commissioningError wraps the cause of a failed commissioning with the
// hub's error for it, keeping the cause's message.
func commissioningError(kind, cause error) error {
	return fmt.Errorf("%w (%w)", kind, cause)
}
