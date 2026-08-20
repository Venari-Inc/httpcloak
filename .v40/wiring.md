# v40 wiring record — httpcloak

The per-project facts a run and its sub-agents would otherwise re-derive: what
bootstraps a checkout, what the suite is, what CI exists, and which machine-global
resources two parallel runs can collide on.

Written once by `/v40:setup`. Re-run that skill when you decide this is stale;
nothing refreshes it automatically, because a discovery that re-ran itself would
overwrite answers a person verified with answers nobody read.

Machine-readable knobs live beside this file in [`.v40/env`](env), which
`shipctl.sh` sources for itself. **Precedence:** an exported environment variable
beats `.v40/env`, and `.v40/env` beats shipctl's built-in defaults — so nothing
recorded here takes a per-run decision away from the operator.

## Bootstrap

```
go mod download
```

From the repository root. Go toolchain 1.26.0 or later (`go.mod`). Nothing else
is needed before the suite runs — no code generation, no service, no fixture load.

## Suite

```
go test -short ./...
```

Recorded as `SHIPCTL_SUITE_CMD` in `.v40/env`.

`-short` is a correctness choice, not a speed one. Three tests in `client/` —
`TestTLSFingerprint_Httpcloak`, `TestTLSFingerprint_GoStdlib` and
`TestTLSFingerprint_Comparison` — make live TLS requests to the third-party
service `tls.peet.ws`, and they guard themselves with `testing.Short()`. Without
`-short` the suite turns red whenever that service is unreachable, blocking
merges for a reason that has nothing to do with the change under test.

Run the full `go test ./...` by hand, with the network up, when the fingerprint
behaviour itself is what changed. That is the only thing `-short` hides.

## CI

**None.** This was verified against the server, not inferred from
`.github/workflows/`:

- no check runs and no commit statuses on any pull-request head commit
- the same on recent base-branch commits
- the repository's Actions run history is empty — no workflow has ever run
- `main` carries no branch protection

The one workflow, `.github/workflows/bindings.yml`, triggers on `v*` tags and
`workflow_dispatch`. It builds and publishes the language bindings: a release
publisher, not a test gate. It never fires on the events the ship loop watches.

`SHIPCTL_CHECKS_REQUIRED` is therefore left unset. `checks-wait` goes on
reporting checks-not-observed and letting the merge proceed, and that outcome is
legitimate here because someone established there is nothing to observe.

## Shared resources

A worktree isolates files and nothing else, so anything below that is not
isolated is shared by every concurrent run by default. Each entry names the knob
that moves it and the probe that proves the responder belongs to *this* run.

| Resource | Isolation knob | Ownership probe |
| --- | --- | --- |
| Test TCP/UDP listeners | None needed. Every listener in the suite binds port `0` and takes an OS-assigned port, so parallel worktrees cannot collide. | n/a |
| Go build and module cache | Shared by design and safe under concurrent `go test`. Set `GOCACHE` / `GOMODCACHE` only to force a cold build. | `go env GOCACHE GOMODCACHE` |
| Docs dev server (`docs/`, Docusaurus) | `npm run start -- --port <n> --no-open`. The default port is 3000; without `--port`, two runs collide. | `curl -s http://localhost:<n>/ \| grep '<title>'` — the title comes from the tree that started the server, so a stale owner from a deleted checkout is visible rather than silently trusted. |

The docs dev server binds **IPv6 `[::1]` only**. A readiness probe against
`127.0.0.1` fails with connection refused while the server is perfectly up; use
`localhost`.

No phase of a run needs to be serialised: nothing in the suite reaches for a
fixed port, a database, a cache, a container, or a fixed temporary path.

## Beyond the suite

Two build targets in this repository are not covered by `go test -short ./...`.
Neither is part of the merge gate — build them by hand when a change touches them,
rather than discovering the break at release-tag time.

- **C bindings** — `bindings/clib` is a separate Go module built with CGO and
  `replace github.com/sardanioss/httpcloak => ../..`. Verify with `go build ./...`
  run from `bindings/clib`.
- **Docs site** — `docs/` is a Docusaurus site. Bootstrap with `npm ci`, verify
  with `npm run build`, both from `docs/`.

## Fork note

`origin` is `Venari-Inc/httpcloak`; `upstream` is `sardanioss/httpcloak`.

`CLAUDE.md` is listed in this repository's `.gitignore` upstream, so the tracked
agent entrypoint here is `AGENTS.md`. A local `CLAUDE.md` is read in the main
checkout but does not follow into a git worktree — anything a sub-agent must read
belongs in `AGENTS.md` or in this file.
