// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

// Package build exposes the container-build half of the OTA workflow as MCP
// tools: build or mirror multi-arch images into a registry, publish a compose
// app pinned to their digests, and collect an offline-update payload.
//
// Every tool shells out to the same binaries a human would use (docker buildx,
// skopeo, composectl) rather than reimplementing them, and reports the command
// it ran so a failure can be reproduced by hand.
package build

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config is the environment the tools build in. It is set once, at server
// start, so a tool call cannot be pointed at a different registry.
type Config struct {
	Registry     string // registry as composectl and the device address it, e.g. registry.example.lan:5000
	PushRegistry string // address docker pushes to; a loopback alias is treated as insecure by dockerd
	RegistryCA   string // PEM file the registry's TLS cert chains to
	Builder      string // buildx builder name; must be a docker-container driver for multi-arch
	WorkDir      string // paths in tool arguments resolve against this
}

// FromEnv fills a Config from the environment, with the same variable names
// the demo workspaces use.
func FromEnv() Config {
	c := Config{
		Registry:     os.Getenv("REGISTRY"),
		PushRegistry: os.Getenv("PUSH_REGISTRY"),
		RegistryCA:   os.Getenv("REGISTRY_CA"),
		Builder:      os.Getenv("BUILDER"),
		WorkDir:      os.Getenv("BUILD_WORKDIR"),
	}
	if c.PushRegistry == "" {
		c.PushRegistry = c.Registry
	}
	if c.Builder == "" {
		c.Builder = "default"
	}
	if c.WorkDir == "" {
		c.WorkDir, _ = os.Getwd()
	}
	return c
}

func (c Config) validate() error {
	if c.Registry == "" {
		return errors.New("REGISTRY is not set: the tools need a registry to push to")
	}
	if c.RegistryCA != "" {
		if _, err := os.Stat(c.RegistryCA); err != nil {
			return fmt.Errorf("REGISTRY_CA %s: %w", c.RegistryCA, err)
		}
	}
	return nil
}

// Register adds the build tools to the server.
func Register(s *mcp.Server, c Config) error {
	if err := c.validate(); err != nil {
		return err
	}

	type ensureBuilderIn struct{}
	type ensureBuilderOut struct {
		Builder   string `json:"builder"`
		Created   bool   `json:"created"`
		Platforms string `json:"platforms"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "ensure_builder",
		Description: "Make sure the configured buildx builder exists and is booted. Multi-platform " +
			"builds need the docker-container driver; the default docker driver cannot export " +
			"several platforms at once.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ensureBuilderIn) (*mcp.CallToolResult, ensureBuilderOut, error) {
		out := ensureBuilderOut{Builder: c.Builder}
		if _, err := c.run(ctx, "docker", "buildx", "inspect", c.Builder); err != nil {
			// network=host so the builder container can reach a registry
			// running on this host.
			if _, err := c.run(ctx, "docker", "buildx", "create",
				"--name", c.Builder, "--driver", "docker-container",
				"--driver-opt", "network=host"); err != nil {
				return nil, out, err
			}
			out.Created = true
		}
		res, err := c.run(ctx, "docker", "buildx", "inspect", "--bootstrap", c.Builder)
		if err != nil {
			return nil, out, err
		}
		for _, line := range strings.Split(res, "\n") {
			if strings.HasPrefix(line, "Platforms:") {
				out.Platforms = strings.TrimSpace(strings.TrimPrefix(line, "Platforms:"))
				break
			}
		}
		return nil, out, nil
	})

	type buildIn struct {
		ContextDir string            `json:"context_dir" jsonschema:"directory holding the Dockerfile"`
		Image      string            `json:"image" jsonschema:"image name without registry or tag, e.g. shellhttpd"`
		Tags       []string          `json:"tags" jsonschema:"tags to push; include a version AND latest if the compose file references :latest"`
		Platforms  string            `json:"platforms,omitempty" jsonschema:"comma-separated, e.g. linux/amd64,linux/arm64 (default both)"`
		BuildArgs  map[string]string `json:"build_args,omitempty" jsonschema:"--build-arg values"`
		File       string            `json:"file,omitempty" jsonschema:"Dockerfile path relative to context_dir"`
	}
	type buildOut struct {
		Image       string   `json:"image"`
		Tags        []string `json:"tags"`
		Platforms   string   `json:"platforms"`
		IndexDigest string   `json:"index_digest" jsonschema:"manifest index digest; pin this, not a tag"`
		PinnedRef   string   `json:"pinned_ref" jsonschema:"registry/image@sha256:... ready for publish_app"`
		Command     string   `json:"command"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "build_image",
		Description: "Build a multi-arch image and push it to the registry, then read back its manifest " +
			"index digest. Do not pass --provenance=false/--sbom=false here: without attestations " +
			"buildx pushes a bare manifest and composectl rejects it as an unexpected manifest.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in buildIn) (*mcp.CallToolResult, buildOut, error) {
		var zero buildOut
		if in.Image == "" || len(in.Tags) == 0 {
			return nil, zero, errors.New("image and at least one tag are required")
		}
		dir, err := c.resolve(in.ContextDir)
		if err != nil {
			return nil, zero, err
		}
		platforms := in.Platforms
		if platforms == "" {
			platforms = "linux/amd64,linux/arm64"
		}

		args := []string{"buildx", "build", "--builder", c.Builder, "--platform", platforms}
		for _, t := range in.Tags {
			args = append(args, "-t", c.PushRegistry+"/"+in.Image+":"+t)
		}
		for k, v := range in.BuildArgs {
			args = append(args, "--build-arg", k+"="+v)
		}
		if in.File != "" {
			args = append(args, "-f", filepath.Join(dir, in.File))
		}
		// registry.insecure covers the loopback push alias; a TLS registry
		// addressed by name ignores it.
		args = append(args, "--output", "type=registry,registry.insecure=true", dir)

		if _, err := c.run(ctx, "docker", args...); err != nil {
			return nil, zero, err
		}
		digest, err := c.indexDigest(in.Image, in.Tags[0])
		if err != nil {
			return nil, zero, err
		}
		return nil, buildOut{
			Image:       in.Image,
			Tags:        in.Tags,
			Platforms:   platforms,
			IndexDigest: digest,
			PinnedRef:   c.Registry + "/" + in.Image + "@" + digest,
			Command:     "docker " + strings.Join(args, " "),
		}, nil
	})

	type mirrorIn struct {
		Upstream  string   `json:"upstream" jsonschema:"upstream reference, e.g. openremote/manager:latest"`
		LocalName string   `json:"local_name" jsonschema:"name to give it in the local registry"`
		Tags      []string `json:"tags" jsonschema:"tags to write locally"`
	}
	type mirrorOut struct {
		Upstream    string   `json:"upstream"`
		LocalName   string   `json:"local_name"`
		Tags        []string `json:"tags"`
		IndexDigest string   `json:"index_digest"`
		PinnedRef   string   `json:"pinned_ref"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "mirror_image",
		Description: "Copy an already-published multi-arch image into the local registry with its " +
			"manifest index intact, for apps assembled from upstream images rather than built here. " +
			"Uses skopeo: `docker buildx imagetools create` segfaults on some indexes (buildx 0.17.1, " +
			"otelhttptrace nil deref over HTTP/2). A per-platform pull+push would flatten the index.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in mirrorIn) (*mcp.CallToolResult, mirrorOut, error) {
		var zero mirrorOut
		if in.Upstream == "" || in.LocalName == "" || len(in.Tags) == 0 {
			return nil, zero, errors.New("upstream, local_name and at least one tag are required")
		}
		for _, t := range in.Tags {
			args := []string{"copy", "--all"}
			if c.RegistryCA != "" {
				// skopeo wants a directory of CAs, not a file.
				args = append(args, "--dest-cert-dir", filepath.Dir(c.RegistryCA))
			}
			args = append(args, "docker://"+in.Upstream, "docker://"+c.Registry+"/"+in.LocalName+":"+t)
			if _, err := c.run(ctx, "skopeo", args...); err != nil {
				return nil, zero, err
			}
		}
		digest, err := c.indexDigest(in.LocalName, in.Tags[0])
		if err != nil {
			return nil, zero, err
		}
		return nil, mirrorOut{
			Upstream:    in.Upstream,
			LocalName:   in.LocalName,
			Tags:        in.Tags,
			IndexDigest: digest,
			PinnedRef:   c.Registry + "/" + in.LocalName + "@" + digest,
		}, nil
	})

	type publishIn struct {
		AppDir       string   `json:"app_dir" jsonschema:"directory holding the compose file"`
		ComposeFile  string   `json:"compose_file,omitempty" jsonschema:"compose file name (default docker-compose.yml)"`
		AppName      string   `json:"app_name" jsonschema:"app name; published as <app_name> in the registry"`
		Tag          string   `json:"tag" jsonschema:"tag for the published app, usually the version"`
		Arches       string   `json:"arches,omitempty" jsonschema:"comma-separated arches, e.g. amd64,arm64 (default both)"`
		PinnedImages []string `json:"pinned_images,omitempty" jsonschema:"image digest refs from build_image/mirror_image"`
	}
	type publishOut struct {
		AppUri  string `json:"app_uri" jsonschema:"registry/app@sha256:... — pass this to pull_payload"`
		AppHash string `json:"app_hash"`
		Command string `json:"command"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "publish_app",
		Description: "Publish a compose app, pinning its images to the digests given. Any ${VAR} left " +
			"in the compose file is interpolated on the DEVICE, where nothing sets it — render such " +
			"values into the file before publishing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in publishIn) (*mcp.CallToolResult, publishOut, error) {
		var zero publishOut
		if in.AppName == "" || in.Tag == "" {
			return nil, zero, errors.New("app_name and tag are required")
		}
		dir, err := c.resolve(in.AppDir)
		if err != nil {
			return nil, zero, err
		}
		composeFile := in.ComposeFile
		if composeFile == "" {
			composeFile = "docker-compose.yml"
		}
		if !filepath.IsAbs(composeFile) {
			composeFile = filepath.Join(dir, composeFile)
		}
		if _, err := os.Stat(composeFile); err != nil {
			return nil, zero, fmt.Errorf("compose file %s: %w", composeFile, err)
		}
		arches := in.Arches
		if arches == "" {
			arches = "amd64,arm64"
		}

		hashFile, err := os.CreateTemp("", "app-hash-*")
		if err != nil {
			return nil, zero, err
		}
		hashPath := hashFile.Name()
		hashFile.Close()
		defer os.Remove(hashPath)

		args := []string{"publish", "-d", hashPath, "-f", composeFile}
		if len(in.PinnedImages) > 0 {
			args = append(args, "--pinned-images", strings.Join(in.PinnedImages, ","))
		}
		args = append(args, c.Registry+"/"+in.AppName+":"+in.Tag, arches)

		if _, err := c.runIn(ctx, dir, "composectl", args...); err != nil {
			return nil, zero, err
		}
		raw, err := os.ReadFile(hashPath)
		if err != nil {
			return nil, zero, err
		}
		// composectl writes the digest already prefixed with "sha256:";
		// strip it so the URI never ends up with two prefixes.
		hash := strings.TrimPrefix(strings.TrimSpace(string(raw)), "sha256:")
		if hash == "" {
			return nil, zero, errors.New("composectl wrote no digest")
		}
		return nil, publishOut{
			AppUri:  c.Registry + "/" + in.AppName + "@sha256:" + hash,
			AppHash: hash,
			Command: "composectl " + strings.Join(args, " "),
		}, nil
	})

	type pullIn struct {
		AppUris []string `json:"app_uris" jsonschema:"app URIs from publish_app; several apps share blobs in one payload"`
		Arch    string   `json:"arch" jsonschema:"target device arch, e.g. arm64 — not the host's"`
		OutDir  string   `json:"out_dir" jsonschema:"payload directory; becomes the dir passed to update_upload"`
	}
	type pullOut struct {
		OutDir string   `json:"out_dir"`
		Arch   string   `json:"arch"`
		Apps   []string `json:"apps" jsonschema:"app directories found in the payload"`
		Bytes  int64    `json:"bytes"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "pull_payload",
		Description: "Pull published apps into an offline-update payload for one architecture. " +
			"The result is what update_upload takes. Build one payload per architecture unless the " +
			"apps are small: a combined payload makes every device fetch content it cannot run.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pullIn) (*mcp.CallToolResult, pullOut, error) {
		var zero pullOut
		if len(in.AppUris) == 0 || in.Arch == "" || in.OutDir == "" {
			return nil, zero, errors.New("app_uris, arch and out_dir are required")
		}
		out, err := c.resolveAllowMissing(in.OutDir)
		if err != nil {
			return nil, zero, err
		}
		appsDir := filepath.Join(out, "apps")
		if err := os.MkdirAll(appsDir, 0o755); err != nil {
			return nil, zero, err
		}
		args := append([]string{"pull", "--arch", in.Arch, "-i", appsDir, "-s", appsDir}, in.AppUris...)
		if _, err := c.run(ctx, "composectl", args...); err != nil {
			return nil, zero, err
		}

		res := pullOut{OutDir: out, Arch: in.Arch}
		entries, err := os.ReadDir(filepath.Join(appsDir, "apps"))
		if err == nil {
			for _, e := range entries {
				res.Apps = append(res.Apps, e.Name())
			}
		}
		res.Bytes = dirSize(out)
		return nil, res, nil
	})

	type catalogOut struct {
		Registry     string   `json:"registry"`
		Repositories []string `json:"repositories"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "registry_catalog",
		Description: "List repositories in the configured registry — what is available to pin or publish.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, catalogOut, error) {
		var body struct {
			Repositories []string `json:"repositories"`
		}
		if err := c.registryGet("/v2/_catalog", &body); err != nil {
			return nil, catalogOut{}, err
		}
		return nil, catalogOut{Registry: c.Registry, Repositories: body.Repositories}, nil
	})

	return nil
}

// --- plumbing -------------------------------------------------------------

// run executes a command in the configured work directory and returns its
// combined output. On failure the output is part of the error, because the
// useful detail from docker and composectl is in the last few lines.
func (c Config) run(ctx context.Context, name string, args ...string) (string, error) {
	return c.runIn(ctx, c.WorkDir, name, args...)
}

func (c Config) runIn(ctx context.Context, dir, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s is not installed or not on PATH", name)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if c.RegistryCA != "" {
		// composectl and other Go tools trust the demo CA through this
		// rather than the system trust store.
		cmd.Env = append(cmd.Env, "SSL_CERT_FILE="+c.RegistryCA)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		return out, fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, tail(out, 40))
	}
	return out, nil
}

// indexDigest reads the manifest index digest from the registry API. Neither
// `docker inspect` (a multi-arch build never enters the local image store) nor
// `buildx imagetools --format` (silently ignored by some versions) is reliable.
func (c Config) indexDigest(image, tag string) (string, error) {
	client, err := c.httpClient()
	if err != nil {
		return "", err
	}
	url := "https://" + c.Registry + "/v2/" + image + "/manifests/" + tag
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	}, ", "))
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	digest := resp.Header.Get("Docker-Content-Digest")
	if !strings.HasPrefix(digest, "sha256:") {
		return "", fmt.Errorf("registry returned %s with no index digest for %s:%s", resp.Status, image, tag)
	}
	return digest, nil
}

func (c Config) registryGet(path string, out any) error {
	client, err := c.httpClient()
	if err != nil {
		return err
	}
	resp, err := client.Get("https://" + c.Registry + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry %s returned %s", path, resp.Status)
	}
	return decodeJSON(resp, out)
}

func (c Config) httpClient() (*http.Client, error) {
	tr := &http.Transport{}
	if c.RegistryCA != "" {
		pem, err := os.ReadFile(c.RegistryCA)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no usable certificate", c.RegistryCA)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, Timeout: 60 * time.Second}, nil
}

// resolve turns a tool-supplied path into an absolute one under WorkDir and
// requires it to exist.
func (c Config) resolve(p string) (string, error) {
	abs, err := c.resolveAllowMissing(p)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("%s: %w", abs, err)
	}
	return abs, nil
}

func (c Config) resolveAllowMissing(p string) (string, error) {
	if p == "" {
		return "", errors.New("a path is required")
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Join(c.WorkDir, p), nil
}

func tail(s string, lines int) string {
	parts := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

func dirSize(dir string) int64 {
	var total int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
