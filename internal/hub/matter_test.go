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
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/cybergarage/go-matter/matter"
	"github.com/cybergarage/go-matter/matter/ble"
	"github.com/cybergarage/go-matter/matter/credentials/testcreds"
	"github.com/cybergarage/go-matter/matter/device"
	"github.com/cybergarage/go-matter/matter/device/cluster"
	"github.com/cybergarage/go-matter/matter/encoding"
	"github.com/cybergarage/go-matter/matter/protocol/im"
	"github.com/cybergarage/go-matter/matter/store"
	"github.com/cybergarage/go-matter/matter/types"
)

const (
	// The setup passcode and discriminator of the Matter SDK's examples,
	// and the manual pairing code which stands for the two.
	lightPasscode      = 20202021
	lightDiscriminator = 3840
	lightPairingCode   = "34970112332"

	lightEndpoint  = 1
	onOffCluster   = 0x0006
	onOffAttribute = 0x0000

	descriptorCluster   = 0x001D
	levelControlCluster = 0x0008
)

// noBLE is a BLE central which finds nothing: the test commissions over IP.
type noBLE struct{}

func (noBLE) DiscoveredDevices() []ble.Device { return nil }

func (noBLE) LookupDeviceByDiscriminator(any) (ble.Device, error) {
	return nil, errors.New("no BLE devices")
}

func (noBLE) Scan(context.Context, ...ble.ScannerOption) error { return nil }

func onOffValue(n Node) (value any, ok bool) {
	for _, a := range n.Attributes {
		if a.Endpoint == lightEndpoint && a.Cluster == onOffCluster && a.Attribute == onOffAttribute {
			return a.Value, true
		}
	}
	return nil, false
}

// startLight starts an On/Off Light built with go-matter's device role, in
// commissioning mode with the SDK example's setup code, and stops it when
// the test ends. It skips the test where the device cannot advertise.
func startLight(t *testing.T) *cluster.OnOff {
	t.Helper()
	light, _ := startLightWithStore(t)
	return light
}

// startLightWithStore is startLight, which also returns the store of the
// light, with its fabrics.
func startLightWithStore(t *testing.T) (*cluster.OnOff, store.DeviceStore) {
	t.Helper()
	deviceStore := store.NewMemDeviceStore()
	attestation, err := testcreds.AttestationProvider()
	if err != nil {
		t.Fatal(err)
	}
	dev, err := device.New(
		device.WithDeviceStore(deviceStore),
		device.WithPasscode(types.Passcode(lightPasscode)),
		device.WithAddress(":0"),
		device.WithDiscriminator(lightDiscriminator),
		device.WithVendorID(testcreds.VendorID),
		device.WithProductID(testcreds.ProductID),
		device.WithAttestationProvider(attestation),
	)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := dev.AddEndpoint(lightEndpoint, device.OnOffLightDeviceType)
	if err != nil {
		t.Fatal(err)
	}
	light := cluster.NewOnOff()
	light.Register(ep)
	if err := dev.Start(); err != nil {
		t.Skipf("the device cannot advertise here: %v", err)
	}
	t.Cleanup(func() { _ = dev.Stop() })
	return light, deviceStore
}

// TestHubCommissionsAGoMatterLight commissions go-matter's On/Off Light from
// the hub itself, over IP, into a fabric the hub creates, follows the new
// node without a restart, and then removes it.
func TestHubCommissionsAGoMatterLight(t *testing.T) {
	if testing.Short() {
		t.Skip("uses mDNS on the network; skipped with -short")
	}
	ctl := NewMatterController(t.TempDir(), matter.WithCommissionerCentral(noBLE{}))
	// The endpoint of the light only; see TestHubFollowsAGoMatterLight.
	endpoint := im.EndpointID(lightEndpoint)
	ctl.paths = []im.AttributeRequestPath{{Endpoint: &endpoint}}
	h, err := New(Config{Controller: ctl, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RetryMin: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Skipf("the hub cannot start here: %v", err)
	}
	defer func() { _ = h.Stop() }()
	light, lightStore := startLightWithStore(t)
	fabrics := func() int {
		t.Helper()
		recs, err := lightStore.ListDeviceFabrics()
		if err != nil {
			t.Fatal(err)
		}
		return len(recs)
	}

	job, err := h.StartCommissioning(lightPairingCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != CommissioningRunning || job.OverBluetooth {
		t.Fatalf("job = %+v", job)
	}
	waitJob := func() Commissioning {
		t.Helper()
		deadline := time.Now().Add(CommissioningTimeout)
		for {
			c, _ := h.Commissioning()
			if c.ID == job.ID && c.State != CommissioningRunning {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatal("the commissioning did not end")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	done := waitJob()
	if done.State != CommissioningSucceeded || done.NodeID == 0 {
		t.Fatalf("commissioning = %+v", done)
	}

	// The node joined the running hub.
	deadline := time.Now().Add(60 * time.Second)
	for {
		n, ok := h.Node(done.NodeID)
		if ok && n.Status == StatusOnline {
			if v, found := onOffValue(n); !found || v != false {
				t.Fatalf("OnOff = %v, %v", v, found)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the new node did not come online: %+v", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := h.Invoke(done.NodeID, Command{Endpoint: lightEndpoint, Cluster: onOffCluster, Command: 0x01}); err != nil {
		t.Fatalf("On: %v", err)
	}
	if !light.On() {
		t.Fatal("the light is off after On")
	}

	// The light's commissioning window closed with the commissioning: the
	// same code finds no device to commission any more.
	job, err = h.StartCommissioning(lightPairingCode, nil)
	if err != nil {
		t.Fatal(err)
	}
	again := waitJob()
	if again.State != CommissioningFailed || (again.ErrorCode != "not_found" && again.ErrorCode != "not_in_commissioning_mode") {
		t.Fatalf("second commissioning = %+v", again)
	}

	// Removing the node takes the fabric off the light itself, and the
	// hub forgets the node.
	if n := fabrics(); n != 1 {
		t.Fatalf("the light has %d fabrics before the removal, want 1", n)
	}
	removal, err := h.RemoveNode(done.NodeID, false)
	if err != nil || !removal.FromDevice {
		t.Fatalf("RemoveNode() = %+v, %v", removal, err)
	}
	if _, ok := h.Node(done.NodeID); ok {
		t.Fatal("the node is still listed")
	}
	if nodes, err := ctl.Nodes(); err != nil || len(nodes) != 0 {
		t.Fatalf("records after the removal = %v, %v", nodes, err)
	}
	if n := fabrics(); n != 0 {
		t.Fatalf("the light has %d fabrics after the removal, want 0", n)
	}
}

// TestHubFollowsAGoMatterLight runs the hub against an On/Off Light built
// with go-matter's device role, over mDNS and UDP on this host: the hub
// creates its fabric in a new store, a commissioner on the same store
// commissions the light, and a hub started on that store connects to the
// light and follows its OnOff attribute.
func TestHubFollowsAGoMatterLight(t *testing.T) {
	if testing.Short() {
		t.Skip("uses mDNS on the network; skipped with -short")
	}
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	noBLECentral := matter.WithCommissionerCentral(noBLE{})

	// A first start creates the fabric of the hub.
	first, err := New(Config{Controller: NewMatterController(dir, noBLECentral), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Skipf("the hub cannot start here: %v", err)
	}
	fabric, ok := first.Fabric()
	if !ok || fabric.FabricID == 0 || fabric.CompressedFabricID == 0 || fabric.AdminVendorID != DefaultVendorID {
		t.Fatalf("fabric = %+v, %v", fabric, ok)
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}

	light := startLight(t)

	// Commission the light into the hub's fabric, as the hub will do from
	// its UI later.
	cmr := matter.NewCommissioner(matter.WithCommissionerStoreDir(dir), noBLECentral)
	if err := cmr.Start(); err != nil {
		t.Skipf("the commissioner cannot start here: %v", err)
	}
	code, err := encoding.NewPairingCodeFromString(lightPairingCode)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), matter.DefaultCommissioningTimeout)
	cme, err := cmr.Commission(ctx, code)
	cancel()
	if err != nil {
		_ = cmr.Stop()
		t.Fatalf("Commission() error = %v", err)
	}
	nodeID, ok := cme.NodeID()
	if !ok {
		t.Fatal("commissioning returned no node ID")
	}
	if err := cmr.Stop(); err != nil {
		t.Fatal(err)
	}

	// The hub, started on the store, finds the light and follows it.
	//
	// The device role of go-matter does not split a report into chunks yet,
	// and the report of every attribute of a node does not fit one message.
	// The test therefore subscribes to the endpoint of the light only; the
	// hub itself subscribes to everything.
	ctl := NewMatterController(dir, noBLECentral)
	endpoint := im.EndpointID(lightEndpoint)
	ctl.paths = []im.AttributeRequestPath{{Endpoint: &endpoint}}
	h, err := New(Config{Controller: ctl, Logger: logger, RetryMin: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Stop() }()
	if again, _ := h.Fabric(); again != fabric {
		t.Fatalf("the fabric changed: %+v, was %+v", again, fabric)
	}
	nodes := h.Nodes()
	if len(nodes) != 1 || nodes[0].ID != uint64(nodeID) || nodes[0].VendorID != testcreds.VendorID {
		t.Fatalf("nodes = %+v", nodes)
	}

	wait := func(what string, cond func(Node) bool) Node {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for {
			n, _ := h.Node(uint64(nodeID))
			if cond(n) {
				return n
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: status %s, error %q, %d attributes", what, n.Status, n.Error, len(n.Attributes))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	n := wait("online", func(n Node) bool { return n.Status == StatusOnline })
	if v, ok := onOffValue(n); !ok || v != false {
		t.Fatalf("OnOff after the priming report = %v, %v", v, ok)
	}
	// The wildcards report the other clusters of the endpoint too.
	clusters := map[uint32]bool{}
	for _, a := range n.Attributes {
		if a.Endpoint != lightEndpoint {
			t.Fatalf("attribute of endpoint %d reported", a.Endpoint)
		}
		clusters[a.Cluster] = true
	}
	if len(clusters) < 2 || !clusters[descriptorCluster] {
		t.Fatalf("clusters reported = %v", clusters)
	}

	light.Set(true)
	wait("OnOff true", func(n Node) bool { v, _ := onOffValue(n); return v == true })
	light.Set(false)
	wait("OnOff false", func(n Node) bool { v, _ := onOffValue(n); return v == false })

	// The hub controls the light over the same session, and sees the new
	// state through its subscription.
	if err := h.Invoke(uint64(nodeID), Command{Endpoint: lightEndpoint, Cluster: onOffCluster, Command: 0x01}); err != nil {
		t.Fatalf("On: %v", err)
	}
	wait("OnOff true after On", func(n Node) bool { v, _ := onOffValue(n); return v == true })
	if !light.On() {
		t.Fatal("the light is off after On")
	}
	if err := h.Invoke(uint64(nodeID), Command{Endpoint: lightEndpoint, Cluster: onOffCluster, Command: 0x02}); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	wait("OnOff false after Toggle", func(n Node) bool { v, _ := onOffValue(n); return v == false })

	// The light has no Level Control cluster: the node refuses the command.
	err = h.Invoke(uint64(nodeID), Command{Endpoint: lightEndpoint, Cluster: levelControlCluster, Command: 0x04, Fields: map[string]any{
		"Level": 127, "TransitionTime": 0, "OptionsMask": 0, "OptionsOverride": 0,
	}})
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("Level Control on an On/Off light: %v", err)
	}
}
