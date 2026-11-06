// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

package build

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRelativeToWorkDir(t *testing.T) {
	c := Config{WorkDir: t.TempDir()}
	got, err := c.resolveAllowMissing("apps/shellhttpd")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(c.WorkDir, "apps/shellhttpd"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, err = c.resolveAllowMissing("/abs/path"); err != nil || got != "/abs/path" {
		t.Errorf("absolute path not preserved: %q, %v", got, err)
	}
	if _, err := c.resolveAllowMissing(""); err == nil {
		t.Error("empty path should be an error")
	}
}

// A registry is the one thing the tools cannot default, so Register must
// refuse rather than produce confusing failures per call.
func TestValidateRequiresRegistry(t *testing.T) {
	if err := (Config{}).validate(); err == nil {
		t.Fatal("empty config should be rejected")
	}
	if err := (Config{Registry: "localhost:5000"}).validate(); err != nil {
		t.Fatalf("registry alone should be enough: %v", err)
	}
	if err := (Config{Registry: "localhost:5000", RegistryCA: "/nope/ca.crt"}).validate(); err == nil {
		t.Fatal("a missing CA file should be rejected at startup")
	}
}

func TestFromEnvDefaultsPushRegistry(t *testing.T) {
	t.Setenv("REGISTRY", "10.0.0.1:5000")
	t.Setenv("PUSH_REGISTRY", "")
	t.Setenv("BUILDER", "")
	c := FromEnv()
	if c.PushRegistry != "10.0.0.1:5000" {
		t.Errorf("push registry should fall back to REGISTRY, got %q", c.PushRegistry)
	}
	if c.Builder != "default" {
		t.Errorf("builder should default, got %q", c.Builder)
	}
}

func TestTailKeepsLastLines(t *testing.T) {
	in := "a\nb\nc\nd\n"
	if got := tail(in, 2); got != "c\nd" {
		t.Errorf("got %q", got)
	}
	if got := tail(in, 10); strings.Count(got, "\n") != 3 {
		t.Errorf("short input should pass through, got %q", got)
	}
}
