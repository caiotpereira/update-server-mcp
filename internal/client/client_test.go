// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Token mode must never consult the fiocli config, so it has to work with no
// config file present at all.
func TestTokenModeNeedsNoFiocliConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, err := New(Options{Mode: ModeToken, URL: "http://localhost:8080/", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if a.URL != "http://localhost:8080" {
		t.Errorf("trailing slash not trimmed: %q", a.URL)
	}
}

// A token read from a file keeps the editor's trailing newline, which would be
// sent as part of the bearer value.
func TestTokenFileIsTrimmed(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("  tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Mode: ModeToken, URL: "http://x", TokenFile: f}); err != nil {
		t.Fatal(err)
	}
}

func TestTokenModeRejectsMissingInputs(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		o          Options
	}{
		{"no url", "server URL", Options{Mode: ModeToken, Token: "tok"}},
		{"no token", "needs a token", Options{Mode: ModeToken, URL: "http://x"}},
		{"blank token", "needs a token", Options{Mode: ModeToken, URL: "http://x", Token: "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestUnknownModeIsRejected(t *testing.T) {
	if _, err := New(Options{Mode: "oauth"}); err == nil || !strings.Contains(err.Error(), "unknown auth mode") {
		t.Fatalf("got %v", err)
	}
}

// Auto must not pick token mode from a half-configured environment, or the
// operator gets an unauthenticated client instead of their fiocli login.
func TestAutoIgnoresIncompleteTokenConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("UPDATE_SERVER_URL", "http://x")
	if _, err := New(Options{}.FromEnv()); err == nil || !strings.Contains(err.Error(), "fiocli") {
		t.Fatalf("got %v, want the fiocli fallback to be used", err)
	}
}

func TestFiocliModeIgnoresTokenEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("UPDATE_SERVER_URL", "http://x")
	t.Setenv("UPDATE_SERVER_TOKEN", "tok")
	if _, err := New(Options{Mode: ModeFiocli}.FromEnv()); err == nil || !strings.Contains(err.Error(), "fiocli") {
		t.Fatalf("got %v, want fiocli mode to refuse rather than use the token", err)
	}
}
