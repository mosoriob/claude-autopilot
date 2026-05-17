# Watcher mode for `claude-autopilot run`

Date: 2026-05-17
Status: Approved

## Problem

`claude-autopilot` runs the task queue once and exits. When the queue has no
actionable work it prints a run summary and terminates:

- `internal/runner/runner.go:176` — early return when `len(tasks) == 0`.
- `internal/runner/runner.go:290` — the `break` at "Step 11" (no actionable
  and no waiting tasks) falls through to summary + return.

This means there is no daemon/watcher behavior: tasks added after `run` starts
are not picked up, and the operator must re-invoke `run` each time. The desired
behavior is a watcher that, when there is nothing to do, sleeps for a short
interval and re-scans, staying alive until explicitly stopped.

## Goal

Add an opt-in watcher mode to the `run` command. When enabled, the runner does
not exit on an empty/idle queue: it sleeps for a configurable interval, re-scans
task files and control commands, and resumes work when new tasks appear. It
exits only on `SIGINT`/`SIGTERM`.

## Non-goals

- No background/detached daemonization (no `fork`, no PID-file service
  management). The watcher runs in the foreground for its whole lifetime.
- No change to rate-limit handling or the existing waiting-task sleep path.
- No retry of terminal `failed` tasks. Failed tasks stay terminal and simply
  remain in the queue while the watcher keeps polling for new work.

## Approach

**Surgical inline change to the existing `Run()` loop.** The current `for {}`
loop in `internal/runner/runner.go` already re-scans task files and processes
control commands on every iteration. The only change needed is to convert the
two loop-terminating points into a conditional idle-wait when watcher mode is
active. The single process lock and signal handler are acquired once and held
for the watcher's entire lifetime — a natural consequence of keeping the change
inside `Run()` rather than wrapping it.

Rejected alternatives:

- Extract `runOnce()` and wrap it in an outer watch loop — larger refactor of
  the critical execution path with real regression risk for the existing
  single-run behavior.
- Separate `Watcher` type driving a `Runner` — duplicates lock and signal
  handling for what is fundamentally "don't exit, sleep instead."

## CLI surface (`cmd/root.go`)

Two new flags on `runCmd`:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--watch` | bool | `false` | Keep running after the queue drains; poll for new tasks instead of exiting. |
| `--watch-interval` | duration | `10s` | Poll interval used while idle in watcher mode. Ignored unless `--watch` is set. |

Validation:

- If `--watch-interval <= 0`, `runRun` returns an error before the loop starts.
- If `--watch-interval` is passed without `--watch`, it is ignored. The flag
  help text documents this.

`runRun` passes `Watch` and `WatchInterval` into the `Runner` struct alongside
the existing fields.

## Runner changes (`internal/runner/runner.go`)

Two new fields on `Runner`:

```go
Watch         bool
WatchInterval time.Duration
```

The existing `for {}` loop is unchanged except at its two terminal points:

1. **`len(tasks) == 0`** (currently runner.go:176–179): today prints a message
   and returns `ExitOK`. New behavior: if `Watch`, invoke the idle-wait helper
   and `continue`; otherwise unchanged.
2. **"Step 11" `break`** (currently runner.go:290): today falls through to
   summary + return. New behavior: if `Watch`, invoke the idle-wait helper and
   `continue`; otherwise unchanged.

The waiting-task sleep path (Step 10 — rate-limited or future `resume_at`) is
untouched; it already loops back into the main loop.

## Idle-wait behavior (quiet heartbeat)

A helper method, `idleWait()`, plus a `drainedOnce` bool declared as a local
variable in `Run()` (loop-scoped state, not part of the `Runner` API) and
passed to / updated by the helper.

- **First drain after activity** (`drainedOnce == false`):
  1. Call `printSummary(stateDir, runStarted)`.
  2. Fire `r.Notifier.NotifyComplete(...)` if the notifier is non-nil
     (preserves today's per-batch "done" notification).
  3. Print exactly one line:
     `Watching for new tasks (every <interval>)... Ctrl+C to stop`.
  4. Set `drainedOnce = true`.
- **Subsequent consecutive idle cycles** (`drainedOnce == true`): print nothing.
- **When a task actually executes:** reset `drainedOnce = false` so the next
  drain reprints the summary/notification. Result: a fresh summary each time a
  batch of work completes, silence in between.
- The sleep is performed in 1-second ticks, checking `r.ShuttingDown` on each
  tick so that `SIGINT`/`SIGTERM` is honored within ~1s. Control-command
  processing and task reload occur at the top of the next loop iteration
  (latency ≤ interval), so a queued `retry`/`cancel` and newly added task files
  are picked up on the following cycle.

## Error handling & exit semantics

- In watcher mode the process exits only via the signal path → `ExitSignal`
  (130), handled by the existing signal handler goroutine.
- The `anyFailed → ExitFailed` return is unreachable in watcher mode by design:
  failed tasks are terminal, stay in the queue, and the watcher continues
  polling for new work.
- Transient/fatal `LoadTasksAndInit` errors keep today's behavior
  (`ExitFatal`). Watcher mode does not swallow fatal load errors.
- The runner lock and signal handler are acquired once and held for the
  watcher's whole lifetime; there is no per-cycle lock re-acquisition.

## Testing (`internal/runner/runner_test.go`)

1. **Watch picks up a late task.** Start `Run()` with `Watch=true`,
   `WatchInterval=50ms`, and an empty task directory, in a goroutine. Assert it
   has not returned. Write a task YAML into the directory. Assert the task is
   executed (using the existing mock-claude test harness). Trigger shutdown
   (signal or `ShuttingDown`). Assert `Run()` returns `ExitSignal`.
2. **Quiet heartbeat.** Assert the summary and the `Watching for new tasks…`
   line are emitted once per drain, not on every idle tick; and that a new
   drain after a task runs reprints them once.
3. **Interval validation.** `--watch-interval=0` (and a negative value) returns
   an error from `runRun` before the loop starts.
4. **Regression guard.** Existing non-watch runner tests pass unchanged,
   confirming the surgical change does not alter single-run behavior.

## Documentation

Update `README.md`:

- Add `--watch` / `--watch-interval` to the `run` command description and the
  commands table.
- A short "Watcher mode" subsection explaining the polling behavior and that it
  exits only on `Ctrl+C`/`SIGTERM`.
