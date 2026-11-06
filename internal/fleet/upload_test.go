// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

package fleet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The server reads the archive expecting apps/ and ostree_repo/ at its root,
// so paths must be relative to the uploaded directory, not absolute.
func TestWriteTarGzUsesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "apps", "shellhttpd-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apps", "shellhttpd-app", "manifest"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := writeTarGz(context.Background(), &buf, dir); err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if strings.HasPrefix(hdr.Name, "/") || strings.Contains(hdr.Name, dir) {
			t.Errorf("archive holds a non-relative path: %q", hdr.Name)
		}
	}
	want := "apps/shellhttpd-app/manifest"
	found := false
	for _, n := range names {
		if n == want {
			found = true
		}
	}
	if !found {
		t.Errorf("missing %q in archive; got %v", want, names)
	}
}

func TestWriteTarGzRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := writeTarGz(context.Background(), io.Discard, dir)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want a symlink refusal, got %v", err)
	}
}

func TestCountFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"one", "a/two", "a/b/three"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := countFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("counted %d files, want 3 (directories must not count)", n)
	}
}
