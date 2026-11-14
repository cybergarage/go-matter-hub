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

// Command homewire is a Matter hub: a controller which stays up, with a web
// UI and an HTTP API. "homewire serve" runs it; open, status and token work
// with the hub which runs with the same data directory.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cybergarage/homewire/internal/version"
)

const (
	// commandName is the name of the command, in its messages.
	commandName = "homewire"
	// defaultDataDirName is the data directory under the home directory.
	defaultDataDirName = ".homewire"
	// legacyDataDirName is the data directory of the hub before it was
	// named homewire, still used when it is the only one there.
	legacyDataDirName = ".go-matter-hub"
)

// command is a subcommand of homewire.
type command struct {
	name    string
	summary string
	run     func(args []string) error
}

func commands() []command {
	return []command{
		{"serve", "run the hub: the controller, its web UI and its HTTP API", serve},
		{"open", "open the web UI of the running hub in a browser, signed in", openCommand},
		{"status", "print the state of the running hub and of its nodes", statusCommand},
		{"token", "print the access token, for scripts which call the API", tokenCommand},
		{"version", "print the version", func([]string) error {
			fmt.Println(version.Version)
			return nil
		}},
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "%s is a Matter hub.\n\nUsage:\n  %s <command> [options]\n\nCommands:\n", commandName, commandName)
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "\nRun \"%s <command> -h\" for the options of a command.\n", commandName)
}

// errUsage is a command line which names no command, or an unknown one;
// the usage has been printed.
var errUsage = errors.New("usage")

func run(args []string, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errUsage
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		usage(os.Stdout)
		return nil
	case "-version", "--version":
		fmt.Println(version.Version)
		return nil
	}
	for _, c := range commands() {
		if c.name == args[0] {
			err := c.run(args[1:])
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
	}
	fmt.Fprintf(stderr, "%s: unknown command %q\n\n", commandName, args[0])
	usage(stderr)
	return errUsage
}

// newFlagSet returns the flag set of a command, whose errors are returned
// to the caller.
func newFlagSet(name, summary string) *flag.FlagSet {
	fs := flag.NewFlagSet(commandName+" "+name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s %s [options]\n\n%s %s.\n\nOptions:\n", commandName, name, commandName, summary)
		fs.PrintDefaults()
	}
	return fs
}

// dataDirFlag adds -data-dir to fs. Its default, empty, stands for the
// default data directory, which resolveDataDir finds.
func dataDirFlag(fs *flag.FlagSet) *string {
	return fs.String("data-dir", "", "directory for the hub's own fabric, its commissioned nodes and the access token (default ~/"+defaultDataDirName+")")
}

// resolveDataDir returns the data directory: dir when given; otherwise
// ~/.homewire, or ~/.go-matter-hub when only that one exists, the data
// directory from before the hub was named homewire.
func resolveDataDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory for the data directory; give -data-dir: %w", err)
	}
	return defaultDataDir(home), nil
}

// defaultDataDir returns the default data directory under home.
func defaultDataDir(home string) string {
	current := filepath.Join(home, defaultDataDirName)
	legacy := filepath.Join(home, legacyDataDirName)
	if _, err := os.Stat(current); err == nil {
		return current
	}
	if fi, err := os.Stat(legacy); err == nil && fi.IsDir() {
		warnLegacyDataDir(legacy, current)
		return legacy
	}
	return current
}

// warnLegacyDataDir is set by tests; it tells that the legacy data
// directory is used.
var warnLegacyDataDir = func(legacy, current string) {
	slog.Warn("using the data directory from before the hub was named homewire; rename it", "dir", legacy, "to", current)
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", commandName, err)
		os.Exit(1)
	}
}
