// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

// Package client resolves the update-server API client from the same
// configuration fiocli uses, so an MCP server inherits whatever context the
// operator already logged into.
package client

import (
	"fmt"
	"os"

	"github.com/foundriesio/update-server/cli/api"
	"github.com/foundriesio/update-server/cli/config"
)

// Options selects which configuration and context to use. Both are empty by
// default, which means "the file fiocli would read, and its active context".
type Options struct {
	ConfigPath string // FIOCLI_CONFIG, or --config
	Context    string // FIOCLI_CONTEXT, or --context
}

// FromEnv fills unset options from the environment.
func (o Options) FromEnv() Options {
	if o.ConfigPath == "" {
		o.ConfigPath = os.Getenv("FIOCLI_CONFIG")
	}
	if o.Context == "" {
		o.Context = os.Getenv("FIOCLI_CONTEXT")
	}
	return o
}

// New builds an API client. The token is read from the config file at call
// time and never logged: tool results must not leak it.
func New(o Options) (*api.Api, error) {
	cfg, err := config.LoadConfig(o.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("no usable fiocli config (run `fiocli login`): %w", err)
	}
	ctx, err := cfg.GetContext(o.Context)
	if err != nil {
		return nil, err
	}
	return api.NewClient(*ctx), nil
}
