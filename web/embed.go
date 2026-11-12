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

// Package web holds the built web UI of the hub.
//
// The files under dist are produced by `npm run build` in this directory
// (see the Makefile target `web`); dist/.gitkeep keeps the directory in the
// repository so that the hub also builds without them.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built web UI, with index.html at its root when the UI has
// been built.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
