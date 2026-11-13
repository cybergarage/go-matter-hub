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
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var errSubscriptionEnded = errors.New("the subscription ended")

// nodeWorker keeps the session with one node: it connects, subscribes to
// every attribute, and records what the reports carry. A subscription
// re-establishes itself after a loss; the worker starts over only when the
// session could not be made, or the subscription ended for good.
type nodeWorker struct {
	hub *Hub
	rec NodeRecord

	// reportMu orders the reports and status changes of the node, so that
	// the listeners are told of them in the order they happened.
	reportMu sync.Mutex

	mu sync.Mutex
	// sess is the session while the subscription on it is up; nil
	// otherwise.
	sess         Session
	status       Status
	err          string
	lastReportAt time.Time
	attrs        map[AttributePath]Attribute
}

func newNodeWorker(h *Hub, rec NodeRecord) *nodeWorker {
	return &nodeWorker{
		hub:    h,
		rec:    rec,
		status: StatusConnecting,
		attrs:  map[AttributePath]Attribute{},
	}
}

func (w *nodeWorker) snapshot() Node {
	w.mu.Lock()
	defer w.mu.Unlock()
	attrs := make([]Attribute, 0, len(w.attrs))
	for _, a := range w.attrs {
		attrs = append(attrs, a)
	}
	sortAttributes(attrs)
	return Node{
		ID:             w.rec.NodeID,
		VendorID:       w.rec.VendorID,
		ProductID:      w.rec.ProductID,
		CommissionedAt: w.rec.CommissionedAt,
		Status:         w.status,
		Error:          w.err,
		LastReportAt:   w.lastReportAt,
		Attributes:     attrs,
	}
}

// setStatus records the status, and tells the listeners when it changed.
func (w *nodeWorker) setStatus(status Status, err error) {
	w.reportMu.Lock()
	defer w.reportMu.Unlock()
	w.setStatusLocked(status, err)
}

func (w *nodeWorker) setStatusLocked(status Status, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	w.mu.Lock()
	changed := w.status != status || w.err != message
	w.status, w.err = status, message
	w.mu.Unlock()
	if !changed {
		return
	}
	log := w.hub.log.With("node", hexID(w.rec.NodeID), "status", string(status))
	if err != nil {
		log.Warn("node status", "error", message)
	} else {
		log.Info("node status")
	}
	w.hub.notify(Change{NodeID: w.rec.NodeID, Kind: ChangeStatus, Status: status, Error: message})
}

// handleReport records the attributes of a report. A report is the proof
// that the node is reachable, so it also brings the node online.
func (w *nodeWorker) handleReport(r Report) {
	w.reportMu.Lock()
	defer w.reportMu.Unlock()

	now := time.Now()
	reported := make([]Attribute, 0, len(r.Attributes))
	w.mu.Lock()
	if r.Priming {
		// The priming report holds every attribute: what it leaves out
		// is gone from the node.
		w.attrs = make(map[AttributePath]Attribute, len(r.Attributes))
	}
	for _, a := range r.Attributes {
		attr := Attribute{AttributePath: a.Path, Value: a.Value, Status: a.Status, UpdatedAt: now}
		w.attrs[attr.AttributePath] = attr
		reported = append(reported, attr)
	}
	w.lastReportAt = now
	w.mu.Unlock()

	w.setStatusLocked(StatusOnline, nil)
	if len(reported) == 0 {
		return
	}
	sortAttributes(reported)
	if r.Priming {
		w.hub.log.Info("node state received", "node", hexID(w.rec.NodeID), "attributes", len(reported))
	} else {
		for _, a := range reported {
			w.hub.log.Info("attribute changed", "node", hexID(w.rec.NodeID), "endpoint", a.Endpoint,
				"cluster", fmt.Sprintf("0x%04X", a.Cluster), "attribute", fmt.Sprintf("0x%04X", a.Attribute), "value", logValue(a))
		}
	}
	w.hub.notify(Change{NodeID: w.rec.NodeID, Kind: ChangeAttributes, Status: StatusOnline, Attributes: reported, Priming: r.Priming})
}

// run keeps a session with the node until ctx is done.
func (w *nodeWorker) run(ctx context.Context) {
	wait := w.hub.cfg.RetryMin
	for ctx.Err() == nil {
		established, err := w.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if established {
			wait = w.hub.cfg.RetryMin
		}
		if err == nil {
			err = errSubscriptionEnded
		}
		w.setStatus(StatusOffline, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, w.hub.cfg.RetryMax)
	}
}

// session connects to the node and subscribes to all its attributes, and
// returns when the subscription has ended or ctx is done. established
// reports whether the subscription was made.
func (w *nodeWorker) session(ctx context.Context) (established bool, err error) {
	connectCtx, cancel := context.WithTimeout(ctx, w.hub.cfg.ConnectTimeout)
	sess, err := w.hub.ctl.Connect(connectCtx, w.rec.NodeID)
	cancel()
	if err != nil {
		return false, err
	}
	defer sess.Close()

	sub, err := sess.SubscribeAll(ctx, w.handleReport, func(err error) {
		// The subscription re-establishes itself; until its next report
		// the node counts as offline.
		w.setStatus(StatusOffline, err)
	})
	if err != nil {
		return false, err
	}
	defer sub.Close()

	w.mu.Lock()
	w.sess = sess
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.sess = nil
		w.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return true, nil
	case <-sub.Done():
		return true, sub.Err()
	}
}

// logValue renders the value of an attribute for the log, as JSON.
func logValue(a Attribute) string {
	if a.Status != nil {
		return fmt.Sprintf("status 0x%02X", *a.Status)
	}
	b, err := json.Marshal(a.Value)
	if err != nil {
		return fmt.Sprint(a.Value)
	}
	const limit = 200
	if len(b) > limit {
		return string(b[:limit]) + "..."
	}
	return string(b)
}

// currentSession returns the session with the node, or nil while there is none.
func (w *nodeWorker) currentSession() Session {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sess
}
