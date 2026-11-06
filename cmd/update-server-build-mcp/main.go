// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

// Command update-server-build-mcp serves the container-build half of the OTA
// workflow over MCP on stdio: build or mirror multi-arch images, publish a
// compose app, and collect an offline-update payload.
//
// It is a separate binary from update-server-mcp because it needs a different
// environment (docker, skopeo, composectl, a registry) and a different trust
// posture: these tools run local commands, where the fleet server only talks
// to the update-server API.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/foundriesio/update-server-mcp/internal/build"
)

var version = "dev"

func main() {
	cfg := build.FromEnv()
	flag.StringVar(&cfg.Registry, "registry", cfg.Registry, "registry composectl and devices use (env REGISTRY)")
	flag.StringVar(&cfg.PushRegistry, "push-registry", cfg.PushRegistry, "registry docker pushes to (env PUSH_REGISTRY)")
	flag.StringVar(&cfg.RegistryCA, "registry-ca", cfg.RegistryCA, "PEM file the registry cert chains to (env REGISTRY_CA)")
	flag.StringVar(&cfg.Builder, "builder", cfg.Builder, "buildx builder name (env BUILDER)")
	flag.StringVar(&cfg.WorkDir, "workdir", cfg.WorkDir, "relative tool paths resolve against this (env BUILD_WORKDIR)")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "update-server-build",
		Title:   "Foundries update-server container builds",
		Version: version,
	}, nil)
	if err := build.Register(s, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "update-server-build-mcp:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "update-server-build-mcp:", err)
		os.Exit(1)
	}
}
