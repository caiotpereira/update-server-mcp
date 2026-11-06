// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

// Package client resolves the update-server API client from either the
// configuration fiocli uses or an explicitly supplied URL and bearer token,
// so an MCP server can inherit an operator's login or run unattended with a
// service token.
package client

import (
	"fmt"
	"os"
	"strings"

	"github.com/foundriesio/update-server/cli/api"
	"github.com/foundriesio/update-server/cli/config"
)

// Mode selects where credentials come from.
type Mode string

const (
	// ModeAuto uses token auth when a URL and a token are both available,
	// and falls back to the fiocli config otherwise.
	ModeAuto Mode = "auto"
	// ModeFiocli reads the fiocli config file and fails if it is unusable,
	// even when a token happens to be in the environment.
	ModeFiocli Mode = "fiocli"
	// ModeToken uses the supplied URL and token only, never touching the
	// fiocli config.
	ModeToken Mode = "token"
)

// Options selects the auth mode and the inputs each mode needs. The zero value
// means "auto, with whatever fiocli would read".
type Options struct {
	Mode Mode // AUTH, or --auth

	// fiocli mode
	ConfigPath string // FIOCLI_CONFIG, or --config
	Context    string // FIOCLI_CONTEXT, or --context

	// token mode
	URL       string // UPDATE_SERVER_URL, or --url
	Token     string // UPDATE_SERVER_TOKEN
	TokenFile string // UPDATE_SERVER_TOKEN_FILE, or --token-file
}

// FromEnv fills unset options from the environment. There is deliberately no
// --token flag: a token on the command line is visible to every process on the
// host via /proc, so it arrives by environment or file only.
func (o Options) FromEnv() Options {
	if o.Mode == "" {
		o.Mode = Mode(os.Getenv("AUTH"))
	}
	if o.ConfigPath == "" {
		o.ConfigPath = os.Getenv("FIOCLI_CONFIG")
	}
	if o.Context == "" {
		o.Context = os.Getenv("FIOCLI_CONTEXT")
	}
	if o.URL == "" {
		o.URL = os.Getenv("UPDATE_SERVER_URL")
	}
	if o.Token == "" {
		o.Token = os.Getenv("UPDATE_SERVER_TOKEN")
	}
	if o.TokenFile == "" {
		o.TokenFile = os.Getenv("UPDATE_SERVER_TOKEN_FILE")
	}
	return o
}

// New builds an API client. In either mode the token is held only in memory
// for the process lifetime and never logged: tool results must not leak it.
func New(o Options) (*api.Api, error) {
	switch o.Mode {
	case "", ModeAuto:
		if o.URL != "" && (o.Token != "" || o.TokenFile != "") {
			return newToken(o)
		}
		return newFiocli(o)
	case ModeToken:
		return newToken(o)
	case ModeFiocli:
		return newFiocli(o)
	default:
		return nil, fmt.Errorf("unknown auth mode %q: want auto, fiocli or token", o.Mode)
	}
}

func newFiocli(o Options) (*api.Api, error) {
	cfg, err := config.LoadConfig(o.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("no usable fiocli config (run `fiocli login`, or use token auth): %w", err)
	}
	ctx, err := cfg.GetContext(o.Context)
	if err != nil {
		return nil, err
	}
	return api.NewClient(*ctx), nil
}

func newToken(o Options) (*api.Api, error) {
	if o.URL == "" {
		return nil, fmt.Errorf("token auth needs a server URL: set UPDATE_SERVER_URL or --url")
	}
	token := o.Token
	if o.TokenFile != "" {
		buf, err := os.ReadFile(o.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("reading token file: %w", err)
		}
		token = string(buf)
	}
	// A trailing newline from `echo >` or a file editor would be sent as part
	// of the bearer value and rejected as a bad token.
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("token auth needs a token: set UPDATE_SERVER_TOKEN or UPDATE_SERVER_TOKEN_FILE")
	}
	return api.NewClient(config.Context{URL: strings.TrimRight(o.URL, "/"), Token: token}), nil
}
