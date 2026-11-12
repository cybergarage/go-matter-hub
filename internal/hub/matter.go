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
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/cybergarage/go-matter/matter"
	"github.com/cybergarage/go-matter/matter/credentials"
	caseprotocol "github.com/cybergarage/go-matter/matter/protocol/case"
	"github.com/cybergarage/go-matter/matter/protocol/im"
	"github.com/cybergarage/go-matter/matter/store"
)

// AppName names the hub's own store: by default the commissioner of the hub
// keeps its fabric and its commissioned nodes in ~/.go-matter-hub, apart
// from the ~/.go-matter of other go-matter applications. It is the name
// matter.WithCommissionerAppName would be given.
const AppName = "go-matter-hub"

// MatterController is the Controller backed by go-matter: a commissioner
// with a file store of its own.
type MatterController struct {
	dir  string
	opts []matter.CommissionerOption
	st   store.Store
	cmr  matter.Commissioner
	// paths are the attributes a session subscribes to; nil is every
	// attribute of the node.
	paths []im.AttributeRequestPath
}

// NewMatterController returns a controller which keeps its fabric and its
// commissioned nodes in dir. opts are added to the options of its
// commissioner, after the store.
func NewMatterController(dir string, opts ...matter.CommissionerOption) *MatterController {
	return &MatterController{dir: dir, opts: opts}
}

// Start opens the store and starts the commissioner, which takes the
// fabric of the store when there is one.
func (c *MatterController) Start() error {
	st, err := store.NewStore(c.dir)
	if err != nil {
		return err
	}
	c.st = st
	return c.startCommissioner()
}

func (c *MatterController) startCommissioner() error {
	opts := append([]matter.CommissionerOption{matter.WithCommissionerStore(c.st)}, c.opts...)
	cmr := matter.NewCommissioner(opts...)
	if err := cmr.Start(); err != nil {
		_ = cmr.Stop()
		return err
	}
	c.cmr = cmr
	return nil
}

// CreateFabric generates a fabric, saves it in the store, and starts the
// commissioner again so that it uses the new fabric.
func (c *MatterController) CreateFabric(vendorID uint16) (Fabric, error) {
	if c.st == nil {
		return Fabric{}, ErrNotStarted
	}
	id, err := credentials.GenerateControllerIdentity()
	if err != nil {
		return Fabric{}, err
	}
	rec := store.FabricRecord{
		FabricID:        id.FabricID,
		AdminNodeID:     id.NodeID,
		AdminVendorID:   vendorID,
		RootCertificate: id.RootCertificate,
		RootPrivateKey:  id.RootPrivateKey,
		NOC:             id.NOC,
		PrivateKey:      id.PrivateKey,
		IPK:             id.IPK,
		UpdatedAt:       time.Now().UTC(),
	}
	// store.CreateFabric does not replace a fabric another process has
	// saved in the meantime.
	if err := store.CreateFabric(c.st, rec); err != nil {
		return Fabric{}, err
	}
	// The commissioner reads the fabric when it starts.
	if c.cmr != nil {
		if err := c.cmr.Stop(); err != nil {
			return Fabric{}, err
		}
		c.cmr = nil
	}
	if err := c.startCommissioner(); err != nil {
		return Fabric{}, err
	}
	fabric, ok, err := c.Fabric()
	if err != nil {
		return Fabric{}, err
	}
	if !ok {
		return Fabric{}, errors.New("the new fabric cannot be read back")
	}
	return fabric, nil
}

// Stop stops the commissioner.
func (c *MatterController) Stop() error {
	if c.cmr == nil {
		return nil
	}
	return c.cmr.Stop()
}

// compressedFabricID computes the compressed fabric ID of a fabric record
// from the public key of its root certificate. 4.3.2.2. Compressed Fabric
// Identifier.
func compressedFabricID(rec store.FabricRecord) (uint64, error) {
	der := rec.RootCertificate
	if block, _ := pem.Decode(der); block != nil {
		der = block.Bytes
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return 0, fmt.Errorf("parse the root certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return 0, errors.New("the root certificate has no ECDSA public key")
	}
	key, err := pub.ECDH()
	if err != nil {
		return 0, fmt.Errorf("root public key: %w", err)
	}
	// The uncompressed point, as the specification hashes it.
	return caseprotocol.ComputeCompressedFabricID(key.Bytes(), rec.FabricID)
}

// Fabric returns the fabric in the store.
func (c *MatterController) Fabric() (Fabric, bool, error) {
	rec, ok, err := c.st.LoadFabric()
	if err != nil || !ok {
		return Fabric{}, false, err
	}
	compressed, err := compressedFabricID(rec)
	if err != nil {
		return Fabric{}, false, err
	}
	return Fabric{
		FabricID:           rec.FabricID,
		CompressedFabricID: compressed,
		AdminNodeID:        rec.AdminNodeID,
		AdminVendorID:      rec.AdminVendorID,
	}, true, nil
}

// Nodes returns the nodes commissioned into the fabric in the store.
func (c *MatterController) Nodes() ([]NodeRecord, error) {
	records, err := c.st.ListCommissionees()
	if err != nil {
		return nil, err
	}
	fabric, ok, err := c.Fabric()
	if err != nil {
		return nil, err
	}
	nodes := make([]NodeRecord, 0, len(records))
	seen := map[uint64]bool{}
	for _, r := range records {
		// A record of another fabric is not reachable with this one. With
		// no fabric at all, every record is reported, so that the caller
		// can tell an empty store from an incomplete one.
		if ok && (r.FabricID != fabric.FabricID || r.CompressedFabricID != fabric.CompressedFabricID) {
			continue
		}
		if seen[r.NodeID] {
			continue
		}
		seen[r.NodeID] = true
		nodes = append(nodes, NodeRecord{
			NodeID:         r.NodeID,
			VendorID:       r.VendorID,
			ProductID:      r.ProductID,
			CommissionedAt: r.CommissionedAt,
		})
	}
	return nodes, nil
}

// Connect establishes a CASE session with a commissioned node.
func (c *MatterController) Connect(ctx context.Context, nodeID uint64) (Session, error) {
	if c.cmr == nil {
		return nil, ErrNotStarted
	}
	node, err := c.cmr.Connect(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return &matterSession{node: node, paths: c.paths}, nil
}

type matterSession struct {
	node  matter.Node
	paths []im.AttributeRequestPath
}

func (s *matterSession) Close() error { return s.node.Close() }

// SubscribeAll subscribes to every attribute of every endpoint with a
// wildcard path. 8.5. Subscribe Interaction.
func (s *matterSession) SubscribeAll(ctx context.Context, handler func(Report), onError func(error)) (Subscription, error) {
	subscriber, ok := s.node.(matter.AttributeSubscriber)
	if !ok {
		return nil, errors.New("the node does not support subscriptions")
	}
	paths := s.paths
	if paths == nil {
		// A path without endpoint, cluster and attribute is a wildcard for
		// all three. 10.6.2. AttributePathIB.
		paths = []im.AttributeRequestPath{{}}
	}
	return subscriber.Subscribe(ctx,
		paths,
		func(r *im.Report) { handler(convertReport(r)) },
		im.WithSubscribeErrorHandler(onError))
}

func convertReport(r *im.Report) Report {
	out := Report{Priming: r.Priming, Attributes: make([]AttributeReport, 0, len(r.Attributes))}
	for _, a := range r.Attributes {
		rep := AttributeReport{
			Path: AttributePath{
				Endpoint:  uint16(a.Path.Endpoint),
				Cluster:   uint32(a.Path.Cluster),
				Attribute: uint32(a.Path.Attribute),
			},
			DataVersion:    a.DataVersion,
			HasDataVersion: a.HasDataVersion,
		}
		if a.Status != nil {
			status := a.Status.IMStatus
			rep.Status = &status
		} else {
			rep.Value = jsonValue(a.Value)
		}
		out.Attributes = append(out.Attributes, rep)
	}
	return out
}
