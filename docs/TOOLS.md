# Tool reference

29 tools across two servers. Required parameters are in **bold**; everything
else is optional. This file mirrors the registrations in
`internal/fleet/tools.go`, `internal/fleet/upload.go` and
`internal/build/tools.go` — if it disagrees with them, they win.

## Fleet server — `update-server-mcp`

Flags:

| flag | env | meaning |
|---|---|---|
| `-auth` | `AUTH` | `auto` (default), `fiocli` or `token` |
| `-config` | `FIOCLI_CONFIG` | fiocli config file (default `$HOME/.config/satcli.yaml`) |
| `-context` | `FIOCLI_CONTEXT` | context in that file (default: the active one) |
| `-url` | `UPDATE_SERVER_URL` | server base URL, token auth |
| — | `UPDATE_SERVER_TOKEN` | bearer token, token auth |
| `-token-file` | `UPDATE_SERVER_TOKEN_FILE` | file holding the token, token auth |
| `-read-only` | — | register only the 17 read tools |
| `-version` | — | print version and exit |

In `fiocli` mode the token is read at call time, so a `fiocli login` in another
terminal takes effect without restarting the server. In `token` mode the
`fiocli` config is never touched, which is what a container or CI runner wants.
`auto` uses token auth only when a URL *and* a token are both present, and
falls back to `fiocli` otherwise rather than building an unauthenticated
client. There is no `-token` flag: argv is world-readable through `/proc`.

### Reads (17)

| tool | parameters | returns |
|---|---|---|
| `devices_list` | `limit` (100), `page` (1), `sort_by` e.g. `-last-seen` | devices, `total_pages`, `more_pages` |
| `device_show` | **`uuid`** | labels, current target, apps, last seen |
| `device_updates` | **`uuid`** | update ids this device processed, newest first |
| `device_update_events` | **`uuid`**, **`update_id`** | per-step events — first place to look at a failure |
| `device_apps_states` | **`uuid`** | what the device reports per app: fetched, installed, running |
| `device_applied_config` | **`uuid`** | the config the device says it applied, with the server's view |
| `config_show` | **`scope`** (`factory`\|`group`\|`device`), `name` | file names, `on_changed`, `encrypted`, and `value` for unencrypted files only |
| `groups_list` | — | device group names |
| `updates_list` | — | every update with tag, upload time, device count |
| `update_summary` | **`update`** | queued / updating / succeeded / failed counts |
| `update_tuf` | **`update`** | the generated TUF metadata, keyed by role file |
| `rollouts_list` | **`update`** | rollout names for that update |
| `rollout_show` | **`update`**, **`rollout`** | targeted uuids and groups, and the devices actually affected |
| `rollout_summary` | **`update`**, **`rollout`** | counts for one rollout rather than the whole update |
| `devices_for_status` | **`update`**, **`status`**, `rollout` | the drill-down behind `update_summary` |
| `denied_devices_list` | — | devices refused at the gateway |
| `pki_cert` | `certs` (default `root.crt`) | PEM. The TLS cert's SAN is the gateway name devices must use |

`config_show` never returns the contents of an encrypted file, only its name.

### Writes (3)

**`rollout_create`** — **`update`**, **`rollout`**, `uuids[]`, `groups[]`.

Not optional in practice. A device that is in no rollout is served the default
TUF metadata, which on a fresh server does not exist; the device then reports a
404 on `/repo/targets.json`, which reads like a DNS or TLS fault.

**`select_apps`** — **`scope`** (`factory`\|`group`\|`device`), `name`,
`apps`, `tag`, `reason`.

Chooses which compose apps a device, group or the whole factory runs. `apps` is
a comma-separated list; `","` means no apps; `"-"` clears the override. Same
for `tag`.

It **replaces** `pacman.compose_apps` — it does not merge. (`sota.Set` in
`cli/subcommands/configs/updates.go`.) It edits only `z-50-fioctl.toml` and
returns `{changed, file, content}`, so you can see exactly what the device
will get. Selecting an app that is not in the device's current target does
nothing: what runs is the intersection of the target's apps and this list, so
there is nothing to fetch.

**`update_upload`** — **`tag`**, **`update`**, **`dir`**, `hardware_id`,
`ostree_hash`, `version`, `name`, `apps{name: sha256}`.

`dir` is a local offline-update directory holding `apps/` and/or
`ostree_repo/`. It is tarred, gzipped and streamed straight into the request
body; nothing is staged on disk. Returns
`{update, tag, dir, files, bytes_sent, apps_only, hardware_id}`.

For an apps-only payload (no `ostree_repo/`) both `hardware_id` and
`ostree_hash` are required — `ostree_hash` must be the 64-hex commit already
installed on the target devices, and the tool checks the shape before sending
anything, because the server would otherwise reject the upload only after
receiving all of it.

`update` and `version` must each be unique across the fleet: the TUF target id
is `<factory>-<version>`, so reusing either collides. One upload carries one
`hardware_id`, which is why a mixed-architecture fleet needs two uploads of the
same content.

### Destructive (3)

Each requires `confirm: true` and refuses otherwise.

| tool | parameters | effect |
|---|---|---|
| `update_delete` | **`update`**, **`confirm`** | removes the update and its content; devices already on it keep running but lose the source |
| `device_delete` | **`uuid`**, **`confirm`** | removes the device record; the device keeps its client cert but must re-register |
| `denied_device_delete` | **`uuid`**, **`confirm`** | takes a device off the denied list so it can reach the gateway again |

## Build server — `update-server-build-mcp`

Flags, each with an environment equivalent:

| flag | env | meaning |
|---|---|---|
| `-registry` | `REGISTRY` | address composectl and the devices use (TLS) |
| `-push-registry` | `PUSH_REGISTRY` | address docker pushes to |
| `-registry-ca` | `REGISTRY_CA` | PEM file the registry cert chains to |
| `-builder` | `BUILDER` | buildx builder name |
| `-workdir` | `BUILD_WORKDIR` | relative tool paths resolve against this |

`REGISTRY` and `PUSH_REGISTRY` differ on purpose. `composectl pull` always
speaks HTTPS and has no insecure flag, so it needs the TLS address and a
trusted CA; dockerd treats `127.0.0.0/8` as insecure automatically, so pushing
to a loopback alias avoids installing a CA for the push. `REGISTRY_CA` is
passed to child processes as `SSL_CERT_FILE`, which is how composectl trusts a
private CA without touching the system trust store.

### Tools (6)

**`ensure_builder`** — no parameters. Creates and boots the configured buildx
builder if absent. Returns `{builder, created, platforms}`. Multi-platform
builds need the `docker-container` driver; the default `docker` driver cannot
do them.

**`build_image`** — **`context_dir`**, **`image`**, **`tags[]`**, `platforms`
(default `linux/amd64,linux/arm64`), `build_args{}`, `file`.

Returns `{image, tags, platforms, index_digest, pinned_ref, command}`.
`pinned_ref` is `registry/image@sha256:…`, ready for `publish_app`.

Include `latest` in `tags` as well as the version if the compose file
references `:latest`. The index digest is read back from the registry API,
because `docker inspect` cannot see it — a multi-arch build never enters the
local image store — and `buildx imagetools --format` is silently ignored by
some versions. The tool deliberately does *not* pass
`--provenance=false --sbom=false`: without attestations buildx pushes a bare
manifest instead of an index, and composectl then rejects it.

**`mirror_image`** — **`upstream`**, **`local_name`**, **`tags[]`**. Copies an
already-published multi-arch image into the local registry with its index
intact. Returns the same digest/ref pair as `build_image`.

It uses `skopeo copy --all`, not `docker buildx imagetools create`: that
segfaults on some indexes (buildx 0.17.1, `otelhttptrace` nil deref over
HTTP/2). A per-platform pull-and-push would flatten the index and lose the
other architecture.

**`publish_app`** — **`app_dir`**, **`app_name`**, **`tag`**, `compose_file`
(default `docker-compose.yml`), `arches` (default `amd64,arm64`),
`pinned_images[]`.

Returns `{app_uri, app_hash, command}`. `app_uri` goes to `pull_payload`. Any
`${VAR}` left in the compose file must already be resolved — the device has no
`.env`. The tool strips composectl's own `sha256:` prefix from the digest file
so the URI never carries it twice.

**`pull_payload`** — **`app_uris[]`**, **`arch`**, **`out_dir`**. Returns
`{out_dir, arch, apps, bytes}`. `out_dir` is what you pass to `update_upload`
as `dir`.

`arch` is the *target device's* architecture, not the host's, and must be one
of the arches the app was published for. Pass several `app_uris` at once: apps
in one payload share blobs, and an update containing every app is what makes
per-device app choice a `select_apps` config change rather than a new upload.

**`registry_catalog`** — no parameters. Lists repositories in the registry.

## The loop

```
ensure_builder → build_image | mirror_image → publish_app → pull_payload
  → update_upload → rollout_create → select_apps → device_show
```

The first four are the build server, the rest the fleet server. For a
mixed-architecture fleet, run `pull_payload` + `update_upload` once per
architecture with distinct update names and versions, then roll out the
matching one to each device.
