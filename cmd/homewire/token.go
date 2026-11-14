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

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// tokenCommand prints the access token of the data directory, for a
// script which calls the API: curl -H "Authorization: Bearer $(homewire
// token)". A browser is signed in with "homewire open" instead.
func tokenCommand(args []string) error {
	flags := newFlagSet("token", "prints the access token, for scripts which call the API")
	dataDir := dataDirFlag(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	dir, err := resolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	token, err := readToken(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no access token in %s: the hub creates %s when it first listens on an address other than loopback, or with -require-auth", dir, filepath.Join(dir, tokenFileName))
	}
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}
