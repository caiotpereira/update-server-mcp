# Contributing

## Setup

You need Go 1.27 or later, and the `update-server` checkout beside this one:

```
parent/
├── update-server/
└── update-server-mcp/
```

`go.mod` has `replace github.com/foundriesio/update-server => ../update-server`.
If that directory is missing, nothing builds. Point the `replace` elsewhere if
your layout differs — but do not commit that change.

```
make all        # bin/update-server-mcp, bin/update-server-build-mcp
make test vet
```

To exercise the servers you also want, for the fleet side, a `fiocli login`
against a running update-server; and for the build side `docker` with a
`docker-container` buildx builder, `skopeo`, `composectl`, and a registry.

## Before you open a PR

```
gofmt -l .      # must print nothing
make vet test
```

New behaviour needs a test. The ones in the tree are the model to follow: they
check the parts that are easy to get quietly wrong — tar paths stay relative,
symlinks are refused, config validation rejects an incomplete environment —
rather than restating the happy path.

If a change touches a tool's inputs, outputs or `Description`, update
`docs/TOOLS.md` in the same commit. That file is the human-readable mirror of
the registrations; a drift between them is a bug.

## Adding a tool

1. Decide which server it belongs to. The fleet server makes HTTP calls to the
   update-server API; the build server runs local commands. Nothing crosses.
   See AGENTS.md for why.
2. Define `fooIn` / `fooOut` structs with `json` and `jsonschema` tags. One
   clause per `jsonschema` tag.
3. Register with `mcp.AddTool[fooIn, fooOut]`. Write a `Description` that
   carries the operational trap, not a restatement of the name.
4. If it changes server state, register it in `registerWrites` so `-read-only`
   drops it. If it is destructive, add a `Confirm bool` field and return
   `confirmErr(...)` when it is false.
5. Validate cheaply and early — before uploading, building or pulling.
6. Add it to `docs/TOOLS.md` and to the counts in `README.md`.

## Commits

Describe the **intent** — why the change was made. Concise imperative subject
line, body hard-wrapped at ~72 columns and composed via `git commit -F <file>`;
a single-line `-m` body is left unwrapped and the shell eats backticks and
`$`. Reserve `-m` for the subject.

Each commit carries a `Signed-off-by: <Name> <email>` (DCO) trailer, then any
`Co-Authored-By:` trailers. Work done with an AI assistant also names the
model, so the history says which one:

```
Signed-off-by: Caio Pereira <caiopere@qti.qualcomm.com>
Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Assisted-by: Claude Code (model claude-opus-5)
```

The sign-off is the human author's either way — the assistant's attribution
does not substitute for it. Check `git log` and mirror the form it uses.

## License

This project is MIT (see LICENSE). New files carry:

```
// Copyright (c) <year> <you>
// SPDX-License-Identifier: MIT
```

By contributing you agree your contribution is licensed under those terms, and
your `Signed-off-by` line certifies the
[Developer Certificate of Origin](https://developercertificate.org/).

Note that the `update-server` project this one builds against is
BSD-3-Clause-Clear. It is consumed as a dependency, not vendored into this
tree; do not copy source from it into this repository without carrying its
headers.
