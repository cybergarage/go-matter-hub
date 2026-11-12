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

// Command go-matter-hub is a Matter hub: a controller which stays up, with a
// web UI and an HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cybergarage/go-matter-hub/internal/auth"
	"github.com/cybergarage/go-matter-hub/internal/hub"
	"github.com/cybergarage/go-matter-hub/internal/server"
	"github.com/cybergarage/go-matter-hub/internal/version"
	"github.com/cybergarage/go-matter-hub/web"
)

const (
	defaultListen  = "127.0.0.1:8580"
	defaultDataDir = ".go-matter-hub"
	tokenFileName  = "token"
)

type hostList []string

func (l *hostList) String() string     { return strings.Join(*l, ",") }
func (l *hostList) Set(v string) error { *l = append(*l, v); return nil }

type options struct {
	listen       string
	dataDir      string
	allowedHosts hostList
	requireAuth  bool
	showVersion  bool
	vendorID     uint16
	logLevel     slog.Level
}

func parseOptions(args []string) (options, error) {
	var o options
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("go-matter-hub", flag.ContinueOnError)
	fs.StringVar(&o.listen, "listen", defaultListen, "address to listen on; an address other than loopback always requires the access token")
	fs.StringVar(&o.dataDir, "data-dir", filepath.Join(home, defaultDataDir), "directory for the hub's own fabric, its commissioned nodes and the access token")
	vendorID := fs.String("vendor-id", fmt.Sprintf("0x%04X", hub.DefaultVendorID), "administrator vendor ID of the fabric the hub creates at its first start")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	fs.Var(&o.allowedHosts, "allowed-host", "host name by which browsers may reach the hub (repeatable); IP addresses and localhost are always allowed")
	fs.BoolVar(&o.requireAuth, "require-auth", false, "require the access token on the loopback interface too")
	fs.BoolVar(&o.showVersion, "version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 {
		return o, fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	// Base 0: 0x-prefixed hexadecimal, or decimal.
	id, err := strconv.ParseUint(*vendorID, 0, 16)
	if err != nil || id == 0 || id > 0xFFF4 {
		return o, fmt.Errorf("invalid -vendor-id %q: expected 0x0001..0xFFF4", *vendorID)
	}
	o.vendorID = uint16(id)
	if err := o.logLevel.UnmarshalText([]byte(*logLevel)); err != nil {
		return o, fmt.Errorf("invalid -log-level %q", *logLevel)
	}
	return o, nil
}

// loopbackOnly reports whether the listen address accepts connections from
// this host only.
func loopbackOnly(listen string) (bool, error) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false, fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	if host == "localhost" {
		return true, nil
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback(), nil
}

// hostNames returns the names under which the hub is likely known on the
// network, in addition to the ones given with -allowed-host.
func hostNames(listen string, configured []string) []string {
	names := append([]string{}, configured...)
	if host, _, err := net.SplitHostPort(listen); err == nil && host != "" && net.ParseIP(host) == nil {
		names = append(names, host)
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, h)
		if !strings.Contains(h, ".") {
			names = append(names, h+".local")
		}
	}
	return names
}

func run(args []string) error {
	o, err := parseOptions(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if o.showVersion {
		fmt.Println(version.Version)
		return nil
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: o.logLevel})))

	loopback, err := loopbackOnly(o.listen)
	if err != nil {
		return err
	}
	authRequired := !loopback || o.requireAuth

	token := ""
	if authRequired {
		tokenPath := filepath.Join(o.dataDir, tokenFileName)
		var created bool
		token, created, err = auth.LoadOrCreateToken(tokenPath)
		if err != nil {
			return fmt.Errorf("access token: %w", err)
		}
		if created {
			slog.Info("created a new access token", "file", tokenPath)
		}
		slog.Info("the access token is required", "file", tokenPath)
	} else {
		slog.Info("listening on loopback only; the access token is not required")
	}

	// The hub keeps a fabric of its own in the data directory; it does not
	// use the store of matterctl or of another go-matter application.
	h, err := hub.New(hub.Config{
		Controller: hub.NewMatterController(o.dataDir),
		VendorID:   o.vendorID,
	})
	if err != nil {
		return err
	}
	if err := h.Start(); err != nil {
		return err
	}
	defer func() {
		if err := h.Stop(); err != nil {
			slog.Warn("stopping the hub", "error", err)
		}
	}()

	ui, err := web.Dist()
	if err != nil {
		return err
	}
	api := server.New(server.Config{
		Auth:         auth.NewAuthenticator(token, authRequired),
		AllowedHosts: hostNames(o.listen, o.allowedHosts),
		Hub:          h,
		UI:           ui,
	})
	srv := &http.Server{
		Handler:           api,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// The event streams stay open as long as their clients do; they are
	// ended first, or Shutdown would wait for them.
	srv.RegisterOnShutdown(api.CloseStreams)

	ln, err := net.Listen("tcp", o.listen)
	if err != nil {
		return err
	}
	slog.Info("go-matter-hub started", "version", version.Version, "url", "http://"+ln.Addr().String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "go-matter-hub:", err)
		os.Exit(1)
	}
}
