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
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// A QR code payload and manual pairing codes from go-matter's tests.
	qrCode     = "MT:Y.ET0EDB00SWDX0IA00"
	manualCode = "3035-750-7966"
	// The manual code of the Matter SDK's examples: passcode 20202021,
	// discriminator 3840.
	exampleCode = "34970112332"
)

func TestParseSetupCode(t *testing.T) {
	for _, code := range []string{qrCode, manualCode, exampleCode, "  " + exampleCode + " ", "3035 750 7966"} {
		if _, err := ParseSetupCode(code); err != nil {
			t.Errorf("ParseSetupCode(%q) = %v", code, err)
		}
	}
	payload, err := ParseSetupCode(qrCode)
	if err != nil || payload.VendorID() != 5010 || payload.Discriminator() != 3136 {
		t.Fatalf("QR payload = %v, %v", payload, err)
	}
	for _, code := range []string{"", "MT:", "MT:not-base38", "3035-750-7967", "1234", "hello", "3035-750-7966x"} {
		_, err := ParseSetupCode(code)
		if !errors.Is(err, ErrInvalidSetupCode) {
			t.Errorf("ParseSetupCode(%q) = %v", code, err)
			continue
		}
		if code != "" && strings.Contains(err.Error(), code) {
			t.Errorf("the error repeats the code: %v", err)
		}
	}
}

func TestValidPasscode(t *testing.T) {
	for _, pin := range []uint32{0, 11111111, 22222222, 88888888, 12345678, 87654321, 99999999, 100000000} {
		if validPasscode(pin) {
			t.Errorf("passcode %d accepted", pin)
		}
	}
	for _, pin := range []uint32{1, 20202021, 13045239, 99999998} {
		if !validPasscode(pin) {
			t.Errorf("passcode %d refused", pin)
		}
	}
}

func TestParseWiFi(t *testing.T) {
	if wifi, err := ParseWiFi("", ""); wifi != nil || err != nil {
		t.Fatalf("no Wi-Fi: %v, %v", wifi, err)
	}
	if wifi, err := ParseWiFi("home", "correct horse"); err != nil || wifi.SSID != "home" || wifi.Password != "correct horse" {
		t.Fatalf("Wi-Fi: %v, %v", wifi, err)
	}
	psk := strings.Repeat("a1", 32)
	if _, err := ParseWiFi("home", psk); err != nil {
		t.Fatalf("PSK: %v", err)
	}
	for _, c := range [][2]string{
		{"", "password"},
		{"home", ""},
		{strings.Repeat("s", 33), "password"},
		{"home", "short"},
		{"home", strings.Repeat("p", 65)},
		{"home", strings.Repeat("z", 64)},
	} {
		_, err := ParseWiFi(c[0], c[1])
		if !errors.Is(err, ErrInvalidWiFi) {
			t.Errorf("ParseWiFi(%q, ...) = %v", c[0], err)
			continue
		}
		if c[1] != "" && strings.Contains(err.Error(), c[1]) {
			t.Errorf("the error repeats the password: %v", err)
		}
	}
}

// changeRecorder keeps the changes a hub tells.
type changeRecorder struct {
	mu      sync.Mutex
	changes []Change
}

func (r *changeRecorder) record(c Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

func (r *changeRecorder) commissionings() []Commissioning {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Commissioning
	for _, c := range r.changes {
		if c.Kind == ChangeCommissioning {
			out = append(out, *c.Commissioning)
		}
	}
	return out
}

func (r *changeRecorder) addedNodes() []Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Node
	for _, c := range r.changes {
		if c.Kind == ChangeNodeAdded {
			out = append(out, *c.Node)
		}
	}
	return out
}

func waitCommissioning(t *testing.T, h *Hub, state CommissioningState) Commissioning {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, ok := h.Commissioning()
		if ok && c.State == state {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("commissioning = %+v, %v; want %s", c, ok, state)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestCommissioningAddsTheNode(t *testing.T) {
	ctl := newFakeController()
	h := newHub(t, ctl)
	rec := &changeRecorder{}
	defer h.Listen(rec.record)()
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Commissioning(); ok {
		t.Fatal("a commissioning before any was started")
	}

	wifi := &WiFiCredentials{SSID: "home", Password: "correct horse"}
	job, err := h.StartCommissioning(exampleCode, wifi)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != CommissioningRunning || job.ID != 1 || !job.OverBluetooth || job.StartedAt.IsZero() {
		t.Fatalf("job = %+v", job)
	}
	// One at a time.
	if _, err := h.StartCommissioning(exampleCode, nil); !errors.Is(err, ErrCommissioningBusy) {
		t.Fatalf("second commissioning: %v", err)
	}

	ctl.commissionResults <- commissionResult{rec: NodeRecord{NodeID: 0x03B2AA7FA56E38D0, VendorID: 5010, ProductID: 769}}
	done := waitCommissioning(t, h, CommissioningSucceeded)
	if done.NodeID != 0x03B2AA7FA56E38D0 || done.FinishedAt.IsZero() || done.Error != "" {
		t.Fatalf("done = %+v", done)
	}
	ctl.mu.Lock()
	call := ctl.commissions[0]
	ctl.mu.Unlock()
	if call.discriminator != 3840 || call.wifi == nil || call.wifi.SSID != "home" {
		t.Fatalf("commission call = %+v", call)
	}

	// The node joins the running hub, and the hub connects to it.
	if n, ok := h.Node(0x03B2AA7FA56E38D0); !ok || n.VendorID != 5010 {
		t.Fatalf("node = %+v, %v", n, ok)
	}
	s := nextSession(t, ctl)
	if s.nodeID != 0x03B2AA7FA56E38D0 {
		t.Fatalf("connected to %X", s.nodeID)
	}
	s.report(Report{Priming: true, Attributes: []AttributeReport{onOff(1, true)}})
	waitFor(t, h, 0x03B2AA7FA56E38D0, "online", func(n Node) bool { return n.Status == StatusOnline })

	jobs := rec.commissionings()
	if len(jobs) != 2 || jobs[0].State != CommissioningRunning || jobs[1].State != CommissioningSucceeded {
		t.Fatalf("commissioning changes = %+v", jobs)
	}
	if added := rec.addedNodes(); len(added) != 1 || added[0].ID != 0x03B2AA7FA56E38D0 || added[0].Status != StatusConnecting {
		t.Fatalf("added nodes = %+v", added)
	}

	// A new commissioning may start once the last one is done.
	if _, err := h.StartCommissioning(exampleCode, nil); err != nil {
		t.Fatalf("next commissioning: %v", err)
	}
}

func TestCommissioningFailures(t *testing.T) {
	cause := errors.New("no matching commissionable device found")
	tests := []struct {
		err  error
		code string
	}{
		{commissioningError(ErrDeviceNotFound, cause), "not_found"},
		{commissioningError(ErrWiFiRequired, cause), "wifi_required"},
		{commissioningError(ErrNotInCommissioningMode, cause), "not_in_commissioning_mode"},
		{errors.New("failed to commission device: PASE: timeout"), "failed"},
	}
	for _, tt := range tests {
		ctl := newFakeController()
		h := newHub(t, ctl)
		if err := h.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err := h.StartCommissioning(exampleCode, nil); err != nil {
			t.Fatal(err)
		}
		ctl.commissionResults <- commissionResult{err: tt.err}
		job := waitCommissioning(t, h, CommissioningFailed)
		if job.ErrorCode != tt.code || job.Error != tt.err.Error() || job.NodeID != 0 {
			t.Errorf("%v: job = %+v", tt.err, job)
		}
		if len(h.Nodes()) != 0 {
			t.Errorf("%v: a node was added", tt.err)
		}
	}
}

func TestCommissioningRefusals(t *testing.T) {
	ctl := newFakeController()
	h := newHub(t, ctl)
	if _, err := h.StartCommissioning(exampleCode, nil); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("before Start: %v", err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.StartCommissioning("12345", nil); !errors.Is(err, ErrInvalidSetupCode) {
		t.Fatalf("invalid code: %v", err)
	}
	if _, ok := h.Commissioning(); ok {
		t.Fatal("a refused commissioning was recorded")
	}
	ctl.mu.Lock()
	calls := len(ctl.commissions)
	ctl.mu.Unlock()
	if calls != 0 {
		t.Fatal("a refused commissioning reached the controller")
	}
}

// Stop ends a commissioning in progress, and waits for it.
func TestStopEndsACommissioning(t *testing.T) {
	ctl := newFakeController()
	h := newHub(t, ctl)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.StartCommissioning(exampleCode, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited for the commissioning")
	}
	if job, ok := h.Commissioning(); !ok || job.State != CommissioningFailed {
		t.Fatalf("job after Stop = %+v, %v", job, ok)
	}
}
