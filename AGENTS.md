# AGENTS.md

Conventions for working in this repository. They apply to every task unless
the user overrides them.

## What this project is

Two MCP (Model Context Protocol) servers, in Go, over the self-hosted
Foundries `update-server`:

* `cmd/update-server-mcp` — the fleet: devices, updates, rollouts, config
  files, per-device app selection, update upload. Talks HTTP to the user API
  and nothing else.
* `cmd/update-server-build-mcp` — the container side: multi-arch image build
  or mirror, compose-app publish, offline-update payload collection. Runs
  local commands (`docker`, `skopeo`, `composectl`) and nothing else.

The split is a security boundary, not packaging taste. The fleet server can be
handed to someone — or run `-read-only` — without also handing over local
command execution. Do not add a tool that shells out to the fleet server, and
do not give the build server an API client.

## Ground every claim

* Every statement about this project traces to a file you read, a command you
  ran, or something the user said. Otherwise it is a guess — label it or drop
  it.
* Never invent flags, config keys or API paths. The CLI surfaces change: run
  `fioup <cmd> --help`, `fiocli <cmd> --help`, `composectl <cmd> --help`
  rather than recalling them. Several claims in this project's history were
  wrong exactly this way (`fioup register --hardware-id` does not exist).
* Mark claims **verified** (read or ran it), **inferred** (state the
  reasoning), or **unknown**. Never present untested code as working.

## Reuse the server's client

`go.mod` carries `replace github.com/foundriesio/update-server => ../update-server`.
Auth, contexts and response types come from that checkout's `cli/api` and
`cli/config`. When the API changes, the compiler says so.

Do not re-implement an endpoint with a hand-rolled request. If `cli/api` lacks
a method you need, add it there, in the update-server repo, under its own
conventions — not here.

## Tool design

A tool's `Description` is the only documentation the model gets. Put the
non-obvious operational knowledge in it, in one or two sentences: the trap, not
the mechanism. Examples already in the tree:

* `rollout_create` explains that a device in no rollout 404s on
  `/repo/targets.json`.
* `update_upload` states that an apps-only payload needs `hardware_id` and
  `ostree_hash`.
* `build_image` says to pin the index digest, not a tag.

Rules that hold across tools:

* **Validate before the expensive part.** `update_upload` refuses a bad
  apps-only payload before streaming a byte, because the server would reject
  it only after the whole multi-gigabyte upload.
* **Stream, don't stage.** Payloads go through `io.Pipe` into the request body.
  Nothing multi-gigabyte gets written to disk twice.
* **Destructive tools take `confirm: true`** and return `confirmErr` otherwise.
  That is a tool error, not a protocol error, so the model can see it and ask
  the operator.
* **Return what the change produced.** `select_apps` returns the resulting
  config file so the caller can see what the device will receive, rather than
  just "ok".
* Every state-changing tool must be registered in `registerWrites`, so
  `-read-only` actually drops it.

## Secrets

The API token is read from the `fiocli` config at call time and never logged,
never echoed in a tool result, and never written to a scratch file.
`config_show` withholds encrypted config contents and reports only names. Keep
it that way: do not add a tool that mints, reads or prints a credential.

## Go conventions

* Input and output types live next to the tool that uses them. A type shared
  by two tools goes at package scope — not inside a `register*` function.
* `jsonschema` struct tags are the per-field docs; keep them to one clause.
* Errors carry the tail of the failing command's output
  (`tail(out, 40)` in `internal/build`), because a bare exit status is useless
  to the model.
* Comments explain **why**. No narration of what the code does.

## Verification

`make all test vet` before you claim anything works. Report what you ran, what
passed, what failed, and what you did not test. A smoke test against a live
server is worth more than another unit test here: the servers speak stdio
JSON-RPC, so

```
scripts/tools-list.py bin/update-server-mcp
```

lists what a client would see. A bare `printf | bin/...` pipe does **not**
work: stdin closes before the server answers and you get nothing. The helper
keeps the pipe open and reads the reply.

Note that stdout is the protocol — anything diagnostic goes to stderr, or it
corrupts the stream. The build server validates its environment at startup and
exits if `REGISTRY`, `PUSH_REGISTRY`, `REGISTRY_CA`, `BUILDER` or
`BUILD_WORKDIR` is unset, so a tool list with no env returns nothing rather
than an empty list.

## Scope

Do what was asked. Note adjacent problems in one line; do not fix them unasked.
No unrequested refactors, renames or dependency additions. Prefer the smallest
change that addresses the cause.
