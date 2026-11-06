# Quick start

From nothing to an app running on a device, driven through the two MCP
servers. Fifteen minutes if the registry and the device already exist.

## 1. Build

You need Go 1.27+ and the `update-server` checkout beside this one — `go.mod`
has `replace github.com/foundriesio/update-server => ../update-server`, so
without it nothing compiles:

```
parent/
├── update-server/
└── update-server-mcp/
```

```
cd update-server-mcp
make all
```

That writes `bin/update-server-mcp` and `bin/update-server-build-mcp`.

## 2. Authenticate the fleet server

Pick one. If you already use `fiocli`, the first is a no-op.

**fiocli login** — the server reads the token from `$HOME/.config/satcli.yaml`
at call time, so a later `fiocli login` takes effect without a restart:

```
fiocli login
```

**bearer token** — no `fiocli` needed, which is what a container or CI runner
wants:

```
install -m 600 /dev/stdin /run/secrets/update-server-token <<< '<token>'
export UPDATE_SERVER_URL=http://update.example.lan:8080
export UPDATE_SERVER_TOKEN_FILE=/run/secrets/update-server-token
```

Add `-auth token` to the command when you run it.

Either way, check it resolves credentials and comes up. This is the real test —
`-version` prints and exits before any auth happens, so it tells you nothing
about your login:

```
scripts/tools-list.py bin/update-server-mcp
# bin/update-server-mcp 23 config_show denied_device_delete ...
```

23 tools, or 17 with `-read-only`. If you get nothing, the server exited at
startup — run it without the pipe and read stderr. A plain
`printf | bin/...` pipe also returns nothing, because stdin closes before the
reply arrives; that is why the helper script exists.

## 3. Configure the build server

Only needed if you want to build or mirror containers through MCP. All five
are required and validated at startup:

```
export REGISTRY=registry.example.lan:5000       # composectl and the devices
export PUSH_REGISTRY=127.0.0.1:5000             # docker push
export REGISTRY_CA=/path/to/certs/ca.crt
export BUILDER=fio-demo
export BUILD_WORKDIR=/path/to/workspaces
scripts/tools-list.py bin/update-server-build-mcp
# bin/update-server-build-mcp 6 build_image ensure_builder ...
```

`REGISTRY` and `PUSH_REGISTRY` differ deliberately: `composectl pull` always
speaks HTTPS and has no insecure flag, so it needs the TLS name and a trusted
CA, while dockerd treats `127.0.0.0/8` as insecure automatically — pushing to
a loopback alias avoids installing a CA just for the push.

## 4. Register with Claude Code

```
claude mcp add update-server -- $PWD/bin/update-server-mcp

claude mcp add update-server-build \
  -e REGISTRY=$REGISTRY -e PUSH_REGISTRY=$PUSH_REGISTRY \
  -e REGISTRY_CA=$REGISTRY_CA -e BUILDER=$BUILDER \
  -e BUILD_WORKDIR=$BUILD_WORKDIR -- $PWD/bin/update-server-build-mcp
```

Use absolute paths — the client does not run these from your shell's cwd.
`.mcp.json.example` shows the same thing as a project file, including a
read-only token-auth entry. Confirm with `claude mcp list`.

## 5. See the fleet

Ask for `devices_list`. Each row carries the target the device reports and when
it was last seen. Then `device_show` on one uuid for its apps and labels, and
`updates_list` for what has been uploaded. Nothing here changes state, so this
is the safe place to confirm the plumbing works.

If `devices_list` is empty, no device has registered yet — that is the device
side of the setup (`fioup register`), not this server's.

## 6. Ship an app

The whole loop, build server first:

```
ensure_builder
build_image      context_dir=./myapp image=myapp tags=[1,latest]
publish_app      app_dir=./myapp app_name=myapp-app tag=1 pinned_images=[<pinned_ref>]
pull_payload     app_uris=[<app_uri>] arch=arm64 out_dir=./payload-arm64
```

then the fleet server:

```
update_upload    tag=main update=1001 dir=./payload-arm64 \
                 hardware_id=<hw-id> ostree_hash=<64 hex>
rollout_create   update=1001 rollout=first uuids=[<uuid>]
select_apps      scope=device name=<uuid> apps=myapp-app
device_show      uuid=<uuid>
```

Four things that will otherwise cost you an afternoon:

* **`rollout_create` is not optional.** A device in no rollout gets the default
  TUF metadata, which on a fresh server does not exist. The device then reports
  a 404 on `/repo/targets.json`, which reads like a DNS or TLS fault.
* **An apps-only payload needs `hardware_id` and `ostree_hash`**, and the hash
  must be the commit already installed on the device. `update_upload` checks
  the shape before streaming, because the server would otherwise reject the
  upload only after receiving all of it.
* **`update` and `version` must be unique across the fleet.** The TUF target id
  is `<factory>-<version>`, so reusing either collides.
* **One upload carries one `hardware_id`.** A mixed-architecture fleet needs
  `pull_payload` + `update_upload` run once per arch, with distinct update
  names, then the matching update rolled out to each device.

## 7. Change what a device runs

Put every app in one update — `pull_payload` takes several `app_uris` at once
and they share blobs — then choose per device with `select_apps`. That makes
app choice a config change rather than a new upload.

`select_apps` **replaces** `pacman.compose_apps`; it does not merge. It returns
the resulting config file so you can see exactly what the device will get.
`apps=","` means no apps, `apps="-"` clears the override. What actually runs is
the intersection of the target's apps and this list, so selecting an app that
is not in the device's target does nothing.

## Troubleshooting

| symptom | look at |
|---|---|
| server exits immediately | run without a pipe; stderr carries the reason, stdout is the JSON-RPC stream |
| `no usable fiocli config` | `fiocli login`, or switch to `-auth token` |
| `token auth needs a server URL` | `UPDATE_SERVER_URL` is unset; `auto` falls back to fiocli rather than sending an empty bearer |
| 401 on every tool | a trailing newline in an inline token; use `-token-file`, which is trimmed |
| build server lists no tools | one of the five env vars is unset — it validates at startup |
| device never updates | `device_update_events`, then confirm a rollout exists with `rollouts_list` |
| app uploaded but not running | `device_apps_states`, then `config_show scope=device name=<uuid>` |

## Next

* [TOOLS.md](TOOLS.md) — every tool, its parameters and what it returns.
* [../CONTRIBUTING.md](../CONTRIBUTING.md) — adding a tool.
