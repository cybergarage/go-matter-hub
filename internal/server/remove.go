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

package server

import (
	"errors"
	"net/http"

	"github.com/cybergarage/go-matter-hub/internal/hub"
)

// handleRemoveNode removes a node: the device leaves the hub's fabric,
// and the hub forgets it. With ?local=true, a device which is offline or
// fails to leave is forgotten anyway; it keeps the fabric until it is
// reset to factory defaults, which the answer tells with fromDevice
// false. The clients are told with a "removed" event.
func (s *Server) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}
	local := false
	switch r.URL.Query().Get("local") {
	case "", "false":
	case "true":
		local = true
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "local is true or false")
		return
	}
	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the hub has not started")
		return
	}
	removal, err := s.hub.RemoveNode(id, local)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, removal)
	case errors.Is(err, hub.ErrUnknownNode):
		writeError(w, http.StatusNotFound, "not_found", "no such node")
	case errors.Is(err, hub.ErrNodeOffline):
		writeError(w, http.StatusConflict, "node_offline", "the node is offline, so it cannot leave the fabric; remove it from the hub only, and reset it to factory defaults")
	case errors.Is(err, hub.ErrRemoving):
		writeError(w, http.StatusConflict, "busy", "the node is being removed")
	case errors.Is(err, hub.ErrRemoveFailed):
		writeError(w, http.StatusBadGateway, "remove_failed", err.Error())
	case errors.Is(err, hub.ErrNotStarted):
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the hub has not started")
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}
