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
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// The Basic Information cluster and the attributes which name a node.
// 11.1.6. Attributes.
const (
	basicInformationCluster = 0x0028
	productNameAttribute    = 0x0003
	nodeLabelAttribute      = 0x0005
)

type hubStatus struct {
	Version      string `json:"version"`
	AuthRequired bool   `json:"authRequired"`
	FabricID     string `json:"fabricId"`
}

type nodeStatus struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	Attributes []struct {
		Endpoint  uint16 `json:"endpoint"`
		Cluster   uint32 `json:"cluster"`
		Attribute uint32 `json:"attribute"`
		Value     any    `json:"value"`
	} `json:"attributes"`
}

// name is the name a person gave the node, or else its product name, as
// the web UI shows it.
func (n nodeStatus) name() string {
	text := func(attribute uint32) string {
		for _, a := range n.Attributes {
			if a.Endpoint == 0 && a.Cluster == basicInformationCluster && a.Attribute == attribute {
				if s, ok := a.Value.(string); ok {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	if s := text(nodeLabelAttribute); s != "" {
		return s
	}
	return text(productNameAttribute)
}

// statusCommand prints the state of the running hub and of its nodes.
func statusCommand(args []string) error {
	fs := newFlagSet("status", "prints the state of the running hub and of its nodes")
	dataDir := dataDirFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	dir, err := resolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	c, err := connect(dir)
	if err != nil {
		return err
	}
	var hub hubStatus
	if err := c.call("GET", "/api/v1/hub", &hub); err != nil {
		return err
	}
	var nodes struct {
		Nodes []nodeStatus `json:"nodes"`
	}
	if err := c.call("GET", "/api/v1/nodes", &nodes); err != nil {
		return err
	}
	printStatus(os.Stdout, c.info, hub, nodes.Nodes)
	return nil
}

func printStatus(w io.Writer, info serveInfo, hub hubStatus, nodes []nodeStatus) {
	auth := "not required"
	if hub.AuthRequired {
		auth = "required"
	}
	fmt.Fprintf(w, "homewire %s, listening on %s (access token %s), since %s\n", hub.Version, info.Listen, auth, info.StartedAt.Local().Format("2006-01-02 15:04"))
	if hub.FabricID != "" {
		fmt.Fprintf(w, "Fabric %s\n", hub.FabricID)
	}
	online := 0
	for _, n := range nodes {
		if n.Status == "online" {
			online++
		}
	}
	fmt.Fprintf(w, "%d node(s), %d online\n", len(nodes), online)
	if len(nodes) == 0 {
		return
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tSTATUS\tNAME\tERROR")
	for _, n := range nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n.ID, n.Status, n.name(), n.Error)
	}
	tw.Flush()
}
