// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

package fleet

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/foundriesio/update-server/cli/api"
)

type uploadIn struct {
	Tag        string            `json:"tag" jsonschema:"tag devices follow, e.g. main"`
	Update     string            `json:"update" jsonschema:"name for this update; must be unique"`
	Dir        string            `json:"dir" jsonschema:"local offline-update directory (holds apps/ and/or ostree_repo/)"`
	HardwareId string            `json:"hardware_id,omitempty" jsonschema:"required when the directory has no ostree_repo"`
	OstreeHash string            `json:"ostree_hash,omitempty" jsonschema:"required for an apps-only update; 64 hex chars"`
	Version    int               `json:"version,omitempty" jsonschema:"TUF target version; must be unique per upload"`
	Name       string            `json:"name,omitempty" jsonschema:"override the probed target name"`
	Apps       map[string]string `json:"apps,omitempty" jsonschema:"override discovered apps as name -> sha256"`
}

type uploadOut struct {
	Update     string `json:"update"`
	Tag        string `json:"tag"`
	Dir        string `json:"dir"`
	Files      int    `json:"files"`
	BytesSent  int64  `json:"bytes_sent" jsonschema:"compressed size streamed to the server"`
	AppsOnly   bool   `json:"apps_only"`
	HardwareId string `json:"hardware_id,omitempty"`
}

// upload tars and gzips the directory straight into the request body, so a
// multi-gigabyte payload never lands on disk twice.
func upload(ctx context.Context, a *api.Api, in uploadIn) (*mcp.CallToolResult, uploadOut, error) {
	var zero uploadOut

	dir, err := filepath.Abs(in.Dir)
	if err != nil {
		return nil, zero, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, zero, fmt.Errorf("cannot read %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, zero, fmt.Errorf("%s is not a directory", dir)
	}

	_, ostreeErr := os.Stat(filepath.Join(dir, "ostree_repo"))
	appsOnly := errors.Is(ostreeErr, fs.ErrNotExist)
	if appsOnly {
		// The server rejects these uploads rather than guessing; fail here
		// with the reason instead of after streaming the whole payload.
		if in.HardwareId == "" {
			return nil, zero, errors.New("hardware_id is required: the directory has no ostree_repo, so the server cannot probe it")
		}
		if in.OstreeHash == "" {
			return nil, zero, errors.New("ostree_hash is required for an apps-only update; use the same dummy value for every upload")
		}
	}
	if in.OstreeHash != "" && len(in.OstreeHash) != 64 {
		return nil, zero, fmt.Errorf("ostree_hash must be 64 hex characters, got %d", len(in.OstreeHash))
	}

	count, err := countFiles(dir)
	if err != nil {
		return nil, zero, err
	}
	if count == 0 {
		return nil, zero, fmt.Errorf("%s is empty", dir)
	}

	pr, pw := io.Pipe()
	counter := &countingReader{r: pr}
	go func() {
		pw.CloseWithError(writeTarGz(ctx, pw, dir))
	}()

	opts := api.CreateUpdateOptions{
		Version:    in.Version,
		HardwareId: in.HardwareId,
		Name:       in.Name,
		OstreeHash: in.OstreeHash,
		Apps:       in.Apps,
	}
	if err := a.Updates().CreateUpdate(in.Tag, in.Update, opts, counter); err != nil {
		pr.CloseWithError(err)
		return nil, zero, err
	}
	return nil, uploadOut{
		Update:     in.Update,
		Tag:        in.Tag,
		Dir:        dir,
		Files:      count,
		BytesSent:  counter.n,
		AppsOnly:   appsOnly,
		HardwareId: in.HardwareId,
	}, nil
}

func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}

// writeTarGz archives dir's contents with paths relative to dir, which is the
// layout the server expects (apps/, ostree_repo/ at the archive root).
func writeTarGz(ctx context.Context, w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Symlinks are followed by Walk only for the root; a payload should
		// not contain them, so refuse rather than ship something surprising.
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to upload symlink %s", rel)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
