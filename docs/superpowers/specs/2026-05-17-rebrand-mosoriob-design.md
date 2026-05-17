# Rebrand module identity to mosoriob

Date: 2026-05-17
Status: Approved (design)

## Problem

`go.mod` declares module `github.com/hseinmoussa/claude-autopilot` (upstream).
This repo is `github.com/mosoriob/claude-autopilot` (a fork). Consequences:

- `go install github.com/hseinmoussa/claude-autopilot@latest` installs the
  upstream repo, not this fork.
- README CI badge, Go Report Card badge, Releases link, and clone URL all
  point at the upstream repo.
- Internal Go imports use the `github.com/hseinmoussa/...` prefix, so a
  consumer importing this fork by its real path would not resolve.

## Goal

Make module identity match the actual repository: every occurrence of
`github.com/hseinmoussa/claude-autopilot` becomes
`github.com/mosoriob/claude-autopilot`. Build and tests stay green.

## Scope

In scope — exhaustive list of files and lines:

| File | What changes |
|------|--------------|
| `go.mod` (line 1) | `module` path prefix |
| `main.go` | import path prefix |
| `cmd/root.go` | import path prefixes |
| `internal/notifier/notifier.go` | import path prefix |
| `internal/config/config.go` | import path prefix |
| `internal/runner/runner.go` | import path prefixes |
| `internal/detector/detector.go` | import path prefix |
| `internal/queue/queue.go` | import path prefix |
| `README.md` (lines 3, 4, 18, 21, 26) | CI badge URL, Go Report Card URL, `go install` line, Releases link, clone URL |
| `llms.txt` (line 15) | `go install` line |

Out of scope:

- `.goreleaser.yml` — verified to contain no `hseinmoussa` reference.
- `.github/workflows/ci.yml` — verified to contain no repo-path reference.
- GitHub-side settings (repo description, topics, Actions secrets) — not
  source-controlled; cannot be changed from the codebase.

## Method

1. Replace every literal `hseinmoussa` with `mosoriob` across the in-scope
   files (the string is the GitHub org segment of the module path; it does
   not appear anywhere it should be preserved).
2. `gofmt -w` the changed `.go` files.
3. Verify: `go build ./...` then `go test ./...` — both must pass with no
   new failures relative to the pre-change baseline (all unit packages
   currently green).

## Acceptance criteria

- `grep -rn hseinmoussa .` returns zero matches.
- `go.mod` line 1 reads `module github.com/mosoriob/claude-autopilot`.
- `go build ./...` succeeds.
- `go test ./...` passes for every package that passed before the change
  (compat, config, detector, fileutil, lock, queue, runner, timeparse).
- README `go install` line and badges resolve to
  `github.com/mosoriob/claude-autopilot`.

## Risks

- Low. The change is a single mechanical string substitution on a token that
  has exactly one meaning (the module org). No logic, control flow, or data
  format is touched. The build + full test suite is the safety net.

## Non-goals

- No code behavior change.
- Issue #2 (NDJSON `type:"result"` idempotency) is explicitly excluded from
  this spec and tracked separately.
