// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

// Command update-server-mcp serves the update-server fleet API over MCP on
// stdio: devices, updates, rollouts, configs and per-device app selection.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/foundriesio/update-server-mcp/internal/client"
	"github.com/foundriesio/update-server-mcp/internal/fleet"
)

var version = "dev"

func main() {
	var (
		auth       = flag.String("auth", "", "credential source: auto (default), fiocli or token (env AUTH)")
		configPath = flag.String("config", "", "fiocli auth: config file (default: the one fiocli uses)")
		ctxName    = flag.String("context", "", "fiocli auth: context in that file (default: its active context)")
		apiUrl     = flag.String("url", "", "token auth: server base URL (env UPDATE_SERVER_URL)")
		tokenFile  = flag.String("token-file", "", "token auth: file holding the bearer token (env UPDATE_SERVER_TOKEN_FILE)")
		readOnly   = flag.Bool("read-only", false, "expose only tools that do not change server state")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	// No --token flag on purpose: a token in argv is readable by any process
	// on the host. It comes from UPDATE_SERVER_TOKEN or --token-file instead.
	api, err := client.New(client.Options{
		Mode:       client.Mode(*auth),
		ConfigPath: *configPath,
		Context:    *ctxName,
		URL:        *apiUrl,
		TokenFile:  *tokenFile,
	}.FromEnv())
	if err != nil {
		// stderr, never stdout: stdout carries the JSON-RPC stream.
		fmt.Fprintln(os.Stderr, "update-server-mcp:", err)
		os.Exit(1)
	}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "update-server",
		Title:   "Foundries update-server",
		Version: version,
	}, nil)
	fleet.Register(s, api, *readOnly)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "update-server-mcp:", err)
		os.Exit(1)
	}
}
