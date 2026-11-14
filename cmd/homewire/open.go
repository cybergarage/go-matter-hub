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
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// openCommand opens the web UI of the running hub in a browser, signed in. With
// -print it prints the link instead, for a browser on another machine.
func openCommand(args []string) error {
	fs := newFlagSet("open", "opens the web UI of the running hub in a browser, signed in")
	dataDir := dataDirFlag(fs)
	printOnly := fs.Bool("print", false, "print the link instead of opening a browser, for a browser on another machine")
	host := fs.String("host", "", "host name or address of the hub in the link (default: this host's loopback address; with -print, its .local name)")
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

	base := c.info.URL
	switch {
	case *host != "":
		base = c.info.hostURL(*host)
	case *printOnly && !c.info.loopbackOnly():
		if name := localHostName(); name != "" {
			base = c.info.hostURL(name)
		}
	}

	link := base + "/"
	note := ""
	if c.info.AuthRequired {
		var code struct {
			Path      string    `json:"path"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		if err := c.call("POST", "/api/v1/session/codes", &code); err != nil {
			return err
		}
		link = base + code.Path
		note = fmt.Sprintf("The link signs one browser in, once, within %s.", time.Until(code.ExpiresAt).Round(time.Second))
	}

	if *printOnly {
		fmt.Println(link)
		if note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
		return nil
	}
	if err := openURL(link); err != nil {
		fmt.Fprintf(os.Stderr, "%s: could not open a browser (%v); open this link:\n", commandName, err)
		fmt.Println(link)
		if note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
	}
	return nil
}

// localHostName returns the name under which other machines on the home
// network likely reach this host: its host name, with .local when it has
// no domain.
func localHostName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return ""
	}
	if !strings.Contains(name, ".") {
		name += ".local"
	}
	return name
}

// openURL opens url in the default browser; tests replace it.
var openURL = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return fmt.Errorf("no graphical session")
		}
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Run()
}
