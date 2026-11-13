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
	"github.com/cybergarage/go-matter/matter/datamodel/command"
)

// maxEndpoint is the largest endpoint ID. 7.18.2.18. Endpoint ID.
const maxEndpoint = 0xFFFE

type commandRequest struct {
	Endpoint *uint32        `json:"endpoint"`
	Cluster  *uint32        `json:"cluster"`
	Command  *uint32        `json:"command"`
	Fields   map[string]any `json:"fields"`
}

type commandResponse struct {
	OK bool `json:"ok"`
}

// handleCommand invokes a command on a node. The fields are checked
// against the definition of the command in the data model catalog; the new
// state reaches the clients over the event stream, as any other change.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}
	var req commandRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Endpoint == nil || req.Cluster == nil || req.Command == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "endpoint, cluster and command are required")
		return
	}
	if *req.Endpoint > maxEndpoint {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid endpoint")
		return
	}
	if s.hub == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such node")
		return
	}
	err := s.hub.Invoke(id, hub.Command{
		Endpoint: uint16(*req.Endpoint),
		Cluster:  *req.Cluster,
		Command:  *req.Command,
		Fields:   req.Fields,
	})
	if err == nil {
		writeJSON(w, http.StatusOK, commandResponse{OK: true})
		return
	}
	status, code := commandErrorStatus(err)
	writeError(w, status, code, err.Error())
}

// commandErrorStatus maps an error of Hub.Invoke to an HTTP status and an
// error code of the API.
func commandErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, hub.ErrUnknownNode):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, hub.ErrNodeOffline):
		return http.StatusConflict, "node_offline"
	case errors.Is(err, command.ErrUnknownCommand), errors.Is(err, hub.ErrTimedCommand):
		return http.StatusBadRequest, "unsupported_command"
	case errors.Is(err, command.ErrUnknownField), errors.Is(err, command.ErrMissingField),
		errors.Is(err, command.ErrInvalidValue), errors.Is(err, command.ErrUnsupportedField):
		return http.StatusBadRequest, "invalid_fields"
	case errors.Is(err, hub.ErrCommandFailed):
		return http.StatusBadGateway, "command_failed"
	default:
		// No answer from the node, or the session failed.
		return http.StatusGatewayTimeout, "node_error"
	}
}
