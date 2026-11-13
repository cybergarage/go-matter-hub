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
	"strings"

	"github.com/cybergarage/go-matter/matter/encoding"
)

var (
	// ErrInvalidSetupCode is returned for a setup code which is not a valid
	// QR code payload or manual pairing code.
	ErrInvalidSetupCode = errors.New("invalid setup code")
	// ErrInvalidWiFi is returned for Wi-Fi credentials which a device
	// cannot take.
	ErrInvalidWiFi = errors.New("invalid Wi-Fi credentials")
)

const (
	maxWiFiSSIDLen     = 32
	minWiFiPasswordLen = 8
	maxWiFiPasswordLen = 64
	qrPayloadPrefix    = "MT:"
)

// WiFiCredentials are the credentials of the Wi-Fi network a device found
// over Bluetooth is to join. 11.9. Network Commissioning Cluster.
type WiFiCredentials struct {
	SSID     string
	Password string
}

// ParseSetupCode reads the setup code of a device: the text of its QR code
// (MT:...) or its 11 or 21 digit manual pairing code. The errors never
// repeat the code. 5.1. Onboarding Payload.
func ParseSetupCode(s string) (encoding.OnboardingPayload, error) {
	s = strings.TrimSpace(s)
	var (
		payload encoding.OnboardingPayload
		err     error
	)
	if strings.HasPrefix(s, qrPayloadPrefix) {
		payload, err = encoding.NewQRPayloadFromString(s)
	} else {
		for _, r := range s {
			if (r < '0' || r > '9') && r != '-' && r != ' ' {
				return nil, fmt.Errorf("%w: enter the text of the QR code (MT:...) or the 11 or 21 digit code", ErrInvalidSetupCode)
			}
		}
		payload, err = encoding.NewPairingCodeFromString(s)
	}
	if err != nil || payload == nil {
		return nil, fmt.Errorf("%w: its length, checksum or encoding is wrong", ErrInvalidSetupCode)
	}
	if payload.Version() != 0 || payload.CommissioningFlow() != 0 {
		return nil, fmt.Errorf("%w: only the standard commissioning flow is supported", ErrInvalidSetupCode)
	}
	if !validPasscode(uint32(payload.Passcode())) {
		return nil, fmt.Errorf("%w: its setup PIN is not valid", ErrInvalidSetupCode)
	}
	return payload, nil
}

// validPasscode rejects the setup PIN values the specification reserves.
// 5.1.7.1. Invalid Passcodes.
func validPasscode(pin uint32) bool {
	if pin == 0 || pin > 99999998 || pin == 12345678 || pin == 87654321 {
		return false
	}
	for n := uint32(11111111); n <= 88888888; n += 11111111 {
		if pin == n {
			return false
		}
	}
	return true
}

// ParseWiFi checks the Wi-Fi credentials of a pairing: both empty for a
// device already on the network, both set for one found over Bluetooth.
// The errors never repeat the password.
func ParseWiFi(ssid, password string) (*WiFiCredentials, error) {
	switch {
	case ssid == "" && password == "":
		return nil, nil
	case ssid == "":
		return nil, fmt.Errorf("%w: enter the SSID too, or leave both empty", ErrInvalidWiFi)
	case password == "":
		return nil, fmt.Errorf("%w: enter the password too (open networks are not supported), or leave both empty", ErrInvalidWiFi)
	case len(ssid) > maxWiFiSSIDLen:
		return nil, fmt.Errorf("%w: the SSID is %d bytes; at most %d", ErrInvalidWiFi, len(ssid), maxWiFiSSIDLen)
	case len(password) < minWiFiPasswordLen || len(password) > maxWiFiPasswordLen:
		return nil, fmt.Errorf("%w: the password must be %d to %d characters", ErrInvalidWiFi, minWiFiPasswordLen, maxWiFiPasswordLen)
	case len(password) == maxWiFiPasswordLen && !isHex(password):
		return nil, fmt.Errorf("%w: a 64-character password must be a hexadecimal PSK; a passphrase has at most 63 characters", ErrInvalidWiFi)
	}
	return &WiFiCredentials{SSID: ssid, Password: password}, nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
