# update-server MCP servers

> [!IMPORTANT]
> **Unofficial, and not for production.**
>
> This is a personal project. It is not an official Qualcomm or Foundries.io
> product, it is not supported by either, and it has no relation to any
> official Qualcomm MCP server at this time.
>
> It is built against the open source implementation of `update-server` and is
> intended for experimentation, demos and local fleets. Do not use it to
> operate a production fleet: it can create rollouts, replace a device's app
> selection, upload updates and delete device records, and nothing here has
> been reviewed or hardened for that. No warranty — see [LICENSE](LICENSE).

Two MCP servers for the self-hosted Foundries `update-server`, in Go:

| binary | what it does | needs |
|---|---|---|
| `update-server-mcp` | the fleet: devices, updates, rollouts, configs, app selection, uploads | a `fiocli` login |
| `update-server-build-mcp` | the container side: build or mirror multi-arch images, publish a compose app, collect a payload | docker, skopeo, composectl, a registry |

They are deliberately separate binaries. The fleet server only talks HTTP to
the update-server API; the build server runs local commands. Keeping them apart
means you can hand out the fleet server — or run it `-read-only` — without also
handing out local command execution.

## Build

```
make all          # bin/update-server-mcp, bin/update-server-build-mcp
make test vet
```

The module uses a `replace` onto the `update-server` checkout next door, so it
reuses that project's own API client (`cli/api`, `cli/config`). Auth, contexts
and response types therefore stay in lockstep with the server, and nothing
re-implements the HTTP surface.

## Fleet server

Credentials come from one of two places, chosen with `-auth`:

| `-auth` | where credentials come from |
|---|---|
| `auto` (default) | token auth if a URL and token are both set, else the `fiocli` config |
| `fiocli` | the `fiocli` config only — fails rather than falling back |
| `token` | the supplied URL and token only; never reads the `fiocli` config |

**fiocli auth** reuses the file `fiocli` writes (`$HOME/.config/satcli.yaml`),
read at call time — so a `fiocli login` in another terminal takes effect
without restarting the server. Run `fiocli login` first.

```
bin/update-server-mcp                 # active context
bin/update-server-mcp -context prod
bin/update-server-mcp -read-only      # drops every state-changing tool
```

**token auth** needs no `fiocli` at all, which is what you want for a
container, a CI runner, or a server someone else operates:

```
UPDATE_SERVER_URL=http://update.example.lan:8080 \
UPDATE_SERVER_TOKEN_FILE=/run/secrets/update-server-token \
bin/update-server-mcp -auth token
```

| flag | env | mode |
|---|---|---|
| `-auth` | `AUTH` | both |
| `-config` | `FIOCLI_CONFIG` | fiocli |
| `-context` | `FIOCLI_CONTEXT` | fiocli |
| `-url` | `UPDATE_SERVER_URL` | token |
| — | `UPDATE_SERVER_TOKEN` | token |
| `-token-file` | `UPDATE_SERVER_TOKEN_FILE` | token |

There is deliberately **no `-token` flag**: anything in argv is readable by
every process on the host through `/proc`. Pass the token by environment, or
better by `-token-file`, which keeps it out of the environment a child process
inherits. A trailing newline in the file is trimmed — otherwise it would be
sent as part of the bearer value and rejected.

`auto` will not pick token mode from a half-configured environment: with a URL
but no token it falls back to `fiocli` rather than building an unauthenticated
client.

Register it with Claude Code:

```
claude mcp add update-server -- /path/to/bin/update-server-mcp
claude mcp add update-server -e AUTH=token -e UPDATE_SERVER_URL=http://update.example.lan:8080 \
  -e UPDATE_SERVER_TOKEN_FILE=/run/secrets/update-server-token -- /path/to/bin/update-server-mcp
```

**23 tools.** Reads: `devices_list`, `device_show`, `device_updates`,
`device_update_events`, `device_apps_states`, `device_applied_config`,
`config_show`, `groups_list`, `updates_list`, `update_summary`, `update_tuf`,
`rollouts_list`, `rollout_show`, `rollout_summary`, `devices_for_status`,
`denied_devices_list`, `pki_cert`. Writes: `rollout_create`, `select_apps`,
`update_upload`. Destructive, each requiring `confirm: true`:
`update_delete`, `device_delete`, `denied_device_delete`.

Three behaviours worth knowing, all encoded in the tool descriptions so the
model does not have to rediscover them:

* `rollout_create` is not optional. A device that is in no rollout gets the
  default TUF metadata, which on a fresh server does not exist — the device
  then reports a 404 on `/repo/targets.json` that reads like a DNS or TLS
  fault.
* `select_apps` **replaces** `pacman.compose_apps`, it does not merge
  (`sota.Set` in `cli/subcommands/configs/updates.go`). It edits only
  `z-50-fioctl.toml` and returns the resulting file, so you can see exactly
  what the device will receive. Selecting an app that is not in the device's
  target does nothing — there is nothing to fetch.
* `update_upload` refuses an apps-only payload without `hardware_id` and
  `ostree_hash` *before* streaming anything, since the server would reject it
  after the whole upload. It tars and gzips straight into the request body, so
  a multi-gigabyte payload is never written to disk twice.
* `config_show` withholds encrypted file contents and reports only their names.

## Build server

Configured by environment or flags; the names match the demo workspaces:

```
REGISTRY=registry.example.lan:5000 \
PUSH_REGISTRY=127.0.0.1:5000 \
REGISTRY_CA=/path/to/certs/ca.crt \
BUILDER=fio-demo \
BUILD_WORKDIR=/path/to/workspace \
bin/update-server-build-mcp
```

`REGISTRY` is the address composectl and the devices use; `PUSH_REGISTRY` is
the address docker pushes to. They differ on purpose: `composectl pull` always
speaks HTTPS with no insecure flag, while dockerd treats `127.0.0.0/8` as
insecure, so a loopback alias avoids installing a CA for the push. `REGISTRY_CA`
is passed to child processes as `SSL_CERT_FILE`, which is how composectl trusts
a private CA without touching the system store.

**6 tools:** `ensure_builder`, `build_image`, `mirror_image`, `publish_app`,
`pull_payload`, `registry_catalog`.

The hard-won bits, again encoded in the tools:

* `build_image` reads the manifest **index** digest back from the registry API.
  `docker inspect` cannot see it (a multi-arch build never enters the local
  image store) and `buildx imagetools --format` is silently ignored by some
  versions. It also does not pass `--provenance=false --sbom=false`: without
  attestations buildx pushes a bare manifest and composectl rejects it.
* `mirror_image` uses `skopeo copy --all` rather than
  `docker buildx imagetools create`, which segfaults on some indexes
  (buildx 0.17.1, `otelhttptrace` nil deref over HTTP/2). A per-platform
  pull+push would flatten the index and break the other architecture.
* `publish_app` strips composectl's own `sha256:` prefix from the digest file,
  so the app URI never carries it twice.
* `pull_payload` takes several app URIs at once — that is how one update ends
  up containing every app, which is what makes per-device app selection a
  config change rather than a new upload.

## Typical loop

```
ensure_builder → build_image (or mirror_image) → publish_app → pull_payload
  → update_upload → rollout_create → select_apps → device_show
```

The first four come from the build server, the rest from the fleet server.

## Documentation

* [docs/QUICKSTART.md](docs/QUICKSTART.md) — build, authenticate, register,
  and ship an app end to end.
* [docs/TOOLS.md](docs/TOOLS.md) — every tool, its parameters and what it
  returns.
* [CONTRIBUTING.md](CONTRIBUTING.md) — setup, the pre-PR checks, how to add a
  tool.
* [AGENTS.md](AGENTS.md) — conventions, including the ones that exist for a
  reason you cannot see from the code.

## License

MIT — see [LICENSE](LICENSE). The `update-server` project this builds against
is BSD-3-Clause-Clear and is consumed as a dependency, not vendored here.
