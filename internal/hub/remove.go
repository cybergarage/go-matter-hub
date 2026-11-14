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
)

var (
	// ErrRemoveFailed is a node which could not be removed from the
	// fabric: it refused, or did not answer, RemoveFabric.
	ErrRemoveFailed = errors.New("the device could not be removed from the fabric")
	// ErrRemoving is a node which is being removed already.
	ErrRemoving = errors.New("the node is being removed")
)

// Removal tells how a node was removed.
type Removal struct {
	// FromDevice is true when the device itself left the fabric. It is
	// false when only the hub forgot the node, which is then still on
	// the fabric until it is reset to factory defaults.
	FromDevice bool `json:"fromDevice"`
}

// RemoveNode removes a node from the hub. It first removes the hub's
// fabric from the device, over the session the hub keeps with it; then
// stops following the node and deletes its record.
//
// A node which is offline, or which fails to leave the fabric, is not
// removed, with ErrNodeOffline or ErrRemoveFailed, unless local is true:
// the hub then forgets the node without the device, which keeps the
// fabric until it is reset to factory defaults.
func (h *Hub) RemoveNode(nodeID uint64, local bool) (Removal, error) {
	h.mu.Lock()
	if !h.started {
		h.mu.Unlock()
		return Removal{}, ErrNotStarted
	}
	w, ok := h.nodes[nodeID]
	if !ok {
		h.mu.Unlock()
		return Removal{}, ErrUnknownNode
	}
	if w.removing {
		h.mu.Unlock()
		return Removal{}, ErrRemoving
	}
	w.removing = true
	h.mu.Unlock()

	removal, err := h.removeFromDevice(w, local)
	if err != nil {
		h.mu.Lock()
		w.removing = false
		h.mu.Unlock()
		return Removal{}, err
	}

	// The worker ends, and with it the session.
	h.mu.Lock()
	delete(h.nodes, nodeID)
	h.mu.Unlock()
	w.stop()
	<-w.done

	if err := h.ctl.Forget(nodeID); err != nil {
		// The node is no longer followed, but its record is still there:
		// it comes back when the hub starts again.
		h.log.Error("node removed, but its record could not be deleted", "node", hexID(nodeID), "error", err)
		h.notify(Change{NodeID: nodeID, Kind: ChangeNodeRemoved})
		return removal, fmt.Errorf("the node was removed, but its record could not be deleted: %w", err)
	}
	h.log.Info("node removed", "node", hexID(nodeID), "fromDevice", removal.FromDevice)
	h.notify(Change{NodeID: nodeID, Kind: ChangeNodeRemoved})
	return removal, nil
}

// removeFromDevice removes the fabric from the device of w, or, when that
// is not possible and local is true, gives up on it.
func (h *Hub) removeFromDevice(w *nodeWorker, local bool) (Removal, error) {
	sess := w.currentSession()
	if sess == nil {
		if !local {
			return Removal{}, ErrNodeOffline
		}
		return Removal{FromDevice: false}, nil
	}
	if err := sess.RemoveFabric(); err != nil {
		if !local {
			return Removal{}, fmt.Errorf("%w: %w", ErrRemoveFailed, err)
		}
		h.log.Warn("the device did not leave the fabric; forgetting it anyway", "node", hexID(w.rec.NodeID), "error", err)
		return Removal{FromDevice: false}, nil
	}
	return Removal{FromDevice: true}, nil
}
