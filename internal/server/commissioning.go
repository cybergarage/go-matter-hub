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

package server

import (
	"errors"
	"net/http"

	"github.com/cybergarage/homewire/internal/hub"
)

type commissioningRequest struct {
	// Code is the setup code: the QR code text (MT:...) or the manual
	// pairing code.
	Code string `json:"code"`
	// WiFi is the network a device found over Bluetooth is to join; nil
	// for a device which is on the network already.
	WiFi *wifiRequest `json:"wifi"`
}

type wifiRequest struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

// commissioningJSON is a commissioning as the API tells it.
type commissioningJSON struct {
	hub.Commissioning
	// Node is the node the device became, once commissioned.
	Node string `json:"node,omitempty"`
}

func newCommissioningJSON(c hub.Commissioning) commissioningJSON {
	j := commissioningJSON{Commissioning: c}
	if c.State == hub.CommissioningSucceeded {
		j.Node = nodeIDText(c.NodeID)
	}
	return j
}

type commissioningResponse struct {
	// Commissioning is the latest commissioning, or null when there has
	// been none.
	Commissioning *commissioningJSON `json:"commissioning"`
}

// handleStartCommissioning starts commissioning a device. It answers once
// the commissioning has started; its progress reaches the clients over the
// event stream as "commissioning" events, and the new node as a "node"
// event. Neither the setup code nor the Wi-Fi password is ever told back
// or logged.
func (s *Server) handleStartCommissioning(w http.ResponseWriter, r *http.Request) {
	var req commissioningRequest
	if !readJSON(w, r, &req) {
		return
	}
	if s.hub == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the hub has not started")
		return
	}
	var wifi *hub.WiFiCredentials
	if req.WiFi != nil {
		var err error
		if wifi, err = hub.ParseWiFi(req.WiFi.SSID, req.WiFi.Password); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_wifi", err.Error())
			return
		}
	}
	c, err := s.hub.StartCommissioning(req.Code, wifi)
	switch {
	case err == nil:
		j := newCommissioningJSON(c)
		writeJSON(w, http.StatusAccepted, commissioningResponse{Commissioning: &j})
	case errors.Is(err, hub.ErrInvalidSetupCode):
		writeError(w, http.StatusBadRequest, "invalid_code", err.Error())
	case errors.Is(err, hub.ErrInvalidWiFi):
		writeError(w, http.StatusBadRequest, "invalid_wifi", err.Error())
	case errors.Is(err, hub.ErrCommissioningBusy):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, hub.ErrNotStarted):
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the hub has not started")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "could not start commissioning")
	}
}

// handleCommissioning tells the latest commissioning, so that a page which
// is opened or reloaded during one can follow it.
func (s *Server) handleCommissioning(w http.ResponseWriter, _ *http.Request) {
	res := commissioningResponse{}
	if s.hub != nil {
		if c, ok := s.hub.Commissioning(); ok {
			j := newCommissioningJSON(c)
			res.Commissioning = &j
		}
	}
	writeJSON(w, http.StatusOK, res)
}
