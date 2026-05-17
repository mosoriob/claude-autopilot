# Watcher Mode for `claude-autopilot run` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `--watch` mode so `claude-autopilot run` keeps polling for new tasks instead of exiting when the queue drains, exiting only on `SIGINT`/`SIGTERM`.

**Architecture:** Surgical inline change to the existing `Runner.Run()` loop. The two loop-terminating points (empty queue at `runner.go:176`, "Step 11" idle `break` at `runner.go:290`) become a conditional idle-wait when watcher mode is active. A new `idleWait()` helper sleeps the poll interval in ≤1s ticks (honoring shutdown) and gates a quiet heartbeat behind a loop-scoped `drainedOnce` flag so the summary/notification print once per drain, not per idle tick. Two new CLI flags (`--watch`, `--watch-interval`) feed two new `Runner` fields.

**Tech Stack:** Go 1.24, cobra CLI, standard library (`time`, `os/signal`, `sync/atomic`). Module path: `github.com/hseinmoussa/claude-autopilot`.

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `internal/runner/runner.go` | Core execution loop | Add `Watch`/`WatchInterval` fields, `idleWait()` helper, wire two terminal points, reset `drainedOnce` after a task executes |
| `cmd/root.go` | CLI surface | Add `--watch`/`--watch-interval` flags, validate interval, pass into `Runner` |
| `internal/runner/watch_test.go` | Watcher behavior tests + shared test harness (NEW) | Fake `claude` binary, temp HOME isolation, task/state helpers, three behavior tests |
| `cmd/root_test.go` | CLI validation test (NEW) | `--watch-interval` validation |
| `README.md` | User docs | Document `--watch`/`--watch-interval` + a "Watcher Mode" subsection |

Test isolation strategy (used by `watch_test.go`): `config.BaseDir()` resolves to `os.UserHomeDir()` → `$HOME` on this platform, so `t.Setenv("HOME", tmp)` redirects all autopilot state into a temp dir. `executeTask` runs `exec.Command("claude", …)`, so a fake `claude` script is placed on a temp `PATH` entry. The fake exits 0, which `detector.Detect(0, …)` classifies as `Completed` regardless of stdout. Shutdown is triggered by `r.ShuttingDown.Store(true)` rather than a real signal (the spec explicitly allows this).

---

## Task 1: CLI flags and `--watch-interval` validation

This task is independent of the runner changes and carries the lowest risk, so it goes first. It adds the two flags, validates `--watch-interval` at the very top of `runRun` (before the expensive `compat.DetectVersion()` call so the validation path is unit-testable without a real `claude`), and passes the values into the `Runner` struct.

**Files:**
- Create: `cmd/root_test.go`
- Modify: `cmd/root.go` (flag vars near `runYes` at `cmd/root.go:138`; `runRun` body start at `cmd/root.go:140`; `Runner` literal at `cmd/root.go:170-178`; `init()` flag registration at `cmd/root.go:709`)

- [ ] **Step 1: Write the failing test**

Create `cmd/root_test.go`:

```go
package cmd

import (
	"strings"
	"testing"
	"time"
)

// setWatchFlags sets the package-level run flags and returns a restore func.
func setWatchFlags(watch bool, interval time.Duration) func() {
	prevWatch, prevInterval := runWatch, runWatchInterval
	runWatch, runWatchInterval = watch, interval
	return func() { runWatch, runWatchInterval = prevWatch, prevInterval }
}

func TestRunRun_ZeroWatchIntervalReturnsError(t *testing.T) {
	restore := setWatchFlags(true, 0)
	defer restore()

	err := runRun(nil, nil)
	if err == nil {
		t.Fatal("expected error for --watch-interval=0, got nil")
	}
	if !strings.Contains(err.Error(), "watch-interval") {
		t.Fatalf("error should mention watch-interval, got: %v", err)
	}
}

func TestRunRun_NegativeWatchIntervalReturnsError(t *testing.T) {
	restore := setWatchFlags(true, -5*time.Second)
	defer restore()

	err := runRun(nil, nil)
	if err == nil {
		t.Fatal("expected error for negative --watch-interval, got nil")
	}
	if !strings.Contains(err.Error(), "watch-interval") {
		t.Fatalf("error should mention watch-interval, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ -run TestRunRun_ -v`
Expected: FAIL — compile error `undefined: runWatch` / `undefined: runWatchInterval`.

- [ ] **Step 3: Add flag vars and validation**

In `cmd/root.go`, replace the single `runYes` declaration:

```go
var runYes bool
```

with:

```go
var (
	runYes           bool
	runWatch         bool
	runWatchInterval time.Duration
)
```

Then, in `runRun`, insert the validation as the **first statements** of the function body (immediately after `func runRun(cmd *cobra.Command, args []string) error {`, before `// Detect Claude Code version.`):

```go
	if runWatch && runWatchInterval <= 0 {
		return fmt.Errorf("--watch-interval must be greater than zero (got %s)", runWatchInterval)
	}

```

(`fmt` and `time` are already imported in `cmd/root.go`.)

- [ ] **Step 4: Pass the new fields into the Runner**

In `cmd/root.go`, change the `Runner` struct literal in `runRun` from:

```go
	r := &runner.Runner{
		Config:         &cfg,
		Adapter:        adapter,
		Detector:       det,
		Notifier:       nfy,
		ProjectDir:     resolveProjectDir(),
		YesFlag:        runYes,
		PromptPatterns: matchers.PromptPatterns,
	}
```

to:

```go
	r := &runner.Runner{
		Config:         &cfg,
		Adapter:        adapter,
		Detector:       det,
		Notifier:       nfy,
		ProjectDir:     resolveProjectDir(),
		YesFlag:        runYes,
		PromptPatterns: matchers.PromptPatterns,
		Watch:          runWatch,
		WatchInterval:  runWatchInterval,
	}
```

- [ ] **Step 5: Register the flags**

In `cmd/root.go` `init()`, find:

```go
	// run command flags.
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip first-run safety prompt")
```

and add the two flags directly below the existing `runYes` line:

```go
	runCmd.Flags().BoolVar(&runWatch, "watch", false, "Keep running after the queue drains; poll for new tasks instead of exiting.")
	runCmd.Flags().DurationVar(&runWatchInterval, "watch-interval", 10*time.Second, "Poll interval used while idle in watcher mode. Ignored unless --watch is set.")
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go build ./... && go test ./cmd/ -run TestRunRun_ -v`
Expected: PASS — both `TestRunRun_ZeroWatchIntervalReturnsError` and `TestRunRun_NegativeWatchIntervalReturnsError` pass. (Build must succeed first; `runner.Runner` does not yet have `Watch`/`WatchInterval` fields, so **this step will fail to build until Task 2 adds them**. To keep Task 1 self-contained, add the two fields now as part of Step 4 — see note below.)

> **Important ordering note:** The `Runner` struct literal in Step 4 references `Watch:` and `WatchInterval:`, which do not exist on `runner.Runner` until Task 2. To keep `go build ./...` green at the end of Task 1, also apply the struct-field addition from Task 2 Step 3 (the two-field block) now. The rest of Task 2 (idleWait + loop wiring) still follows in Task 2. Concretely: before running Step 6, open `internal/runner/runner.go` and add to the `Runner` struct, immediately after the `PromptPatterns []string` field:
>
> ```go
> 	// Watch keeps Run() polling for new tasks instead of exiting when the
> 	// queue drains. WatchInterval is the idle poll interval.
> 	Watch         bool
> 	WatchInterval time.Duration
> ```
>
> (`time` is already imported in `runner.go`.) Adding only the fields (not yet wired) is inert and keeps every commit buildable.

- [ ] **Step 7: Commit**

```bash
git add cmd/root.go cmd/root_test.go internal/runner/runner.go
git commit -m "feat: add --watch and --watch-interval flags with interval validation"
```

---

## Task 2: Runner watcher loop (`idleWait`, terminal-point wiring) + late-task test

Adds the always-printing `idleWait()` helper and converts the two loop-terminating points into a conditional idle-wait. The quiet-heartbeat gating is deliberately **deferred to Task 3** so Task 3 has a genuine failing test to drive it (textbook TDD split). This task's test only asserts that a late-added task is picked up and that the watcher exits on shutdown.

**Files:**
- Create: `internal/runner/watch_test.go`
- Modify: `internal/runner/runner.go` (loop body in `Run()`; new helper method)

- [ ] **Step 1: Write the failing test (with the shared harness)**

Create `internal/runner/watch_test.go`:

```go
package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/compat"
	"github.com/hseinmoussa/claude-autopilot/internal/config"
	"github.com/hseinmoussa/claude-autopilot/internal/detector"
	"github.com/hseinmoussa/claude-autopilot/internal/fileutil"
	"github.com/hseinmoussa/claude-autopilot/internal/queue"
)

// watchHarness isolates a Runner test: a temp HOME so config.BaseDir() points
// into TempDir, a fake `claude` on PATH that exits 0 (→ detector Completed),
// and a captured os.Stdout drained concurrently so the runner never blocks on
// a full pipe.
type watchHarness struct {
	t         *testing.T
	home      string
	tasksDir  string
	stateDir  string
	workDir   string
	mu        sync.Mutex
	outBuf    *bytes.Buffer
	stdoutW   *os.File
	prevOut   *os.File
}

func newWatchHarness(t *testing.T) *watchHarness {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Fake claude on PATH: exits 0 so the detector returns Completed.
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	base := config.BaseDir()

	h := &watchHarness{
		t:        t,
		home:     home,
		tasksDir: filepath.Join(base, "tasks"),
		stateDir: filepath.Join(base, "state"),
		workDir:  t.TempDir(),
		outBuf:   &bytes.Buffer{},
	}

	// Redirect os.Stdout into a pipe drained concurrently into outBuf.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	h.prevOut = os.Stdout
	h.stdoutW = w
	os.Stdout = w

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				h.mu.Lock()
				h.outBuf.Write(buf[:n])
				h.mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		os.Stdout = h.prevOut
		_ = w.Close()
		<-drained
		_ = r.Close()
	})

	return h
}

// newRunner builds a Runner wired for tests (safe-mode adapter, nil Notifier,
// first-run prompt skipped). Caller sets Watch / WatchInterval.
func (h *watchHarness) newRunner() *Runner {
	adapter := compat.NewAdapter(nil)
	return &Runner{
		Config:   &config.Config{},
		Adapter:  adapter,
		Detector: detector.NewDetector(nil, adapter.RateLimitExitCode()),
		Notifier: nil,
		YesFlag:  true,
	}
}

// writeTask atomically writes a minimal valid task YAML into the queue.
func (h *watchHarness) writeTask(id string) {
	h.t.Helper()
	yaml := "id: " + id + "\nprompt: \"noop\"\nworking_dir: " + h.workDir + "\n"
	path := filepath.Join(h.tasksDir, id+".yaml")
	if err := fileutil.AtomicWrite(path, []byte(yaml), 0644); err != nil {
		h.t.Fatal(err)
	}
}

// waitStatus polls task state until it reaches want or the timeout elapses.
func (h *watchHarness) waitStatus(id, want string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, _ := queue.LoadState(h.stateDir, id)
		if st != nil && st.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := queue.LoadState(h.stateDir, id)
	got := "<nil>"
	if st != nil {
		got = st.Status
	}
	h.t.Fatalf("task %s did not reach status %q within %s (got %q)", id, want, timeout, got)
}

// output returns a snapshot of captured stdout.
func (h *watchHarness) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.outBuf.String()
}

func TestRun_WatchPicksUpLateTask(t *testing.T) {
	h := newWatchHarness(t)
	r := h.newRunner()
	r.Watch = true
	r.WatchInterval = 50 * time.Millisecond

	done := make(chan int, 1)
	go func() { done <- r.Run() }()

	// Let the watcher complete at least one idle cycle on the empty queue.
	time.Sleep(150 * time.Millisecond)
	select {
	case ec := <-done:
		t.Fatalf("Run() exited early with code %d before any task was added", ec)
	default:
	}

	// Add a task after Run() has started — the watcher must pick it up.
	h.writeTask("late-task")
	h.waitStatus("late-task", queue.StatusDone, 5*time.Second)

	// Trigger shutdown and assert ExitSignal.
	r.ShuttingDown.Store(true)
	select {
	case ec := <-done:
		if ec != ExitSignal {
			t.Fatalf("Run() returned %d; want ExitSignal (%d)", ec, ExitSignal)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after shutdown")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runner/ -run TestRun_WatchPicksUpLateTask -v`
Expected: FAIL — the test times out / `Run()` exits early. With watcher wiring absent, `Run()` hits the empty-queue `return ExitOK` at `runner.go:176-179` and the test fails at the "exited early" check.

- [ ] **Step 3: Confirm the Runner fields exist**

The `Watch`/`WatchInterval` fields were added at the end of Task 1 (Step 6 note). Verify the `Runner` struct in `internal/runner/runner.go` contains, immediately after `PromptPatterns []string`:

```go
	// Watch keeps Run() polling for new tasks instead of exiting when the
	// queue drains. WatchInterval is the idle poll interval.
	Watch         bool
	WatchInterval time.Duration
```

If missing (e.g., Task 1 skipped the note), add it now.

- [ ] **Step 4: Add the `idleWait` helper (always-print version)**

In `internal/runner/runner.go`, add this method immediately after the `Run()` method (after its closing `}` near `runner.go:304`, before `func (r *Runner) executeTask`):

```go
// idleWait is invoked from Run() when watcher mode is active and there is no
// actionable work. It prints the per-batch summary and notification, prints a
// heartbeat line, then sleeps r.WatchInterval in ticks of at most one second,
// returning early if a shutdown signal arrives so SIGINT/SIGTERM is honored
// within ~1s (or within the interval when it is shorter).
func (r *Runner) idleWait(stateDir string, runStarted time.Time) {
	r.printSummary(stateDir, runStarted)
	if r.Notifier != nil {
		r.Notifier.NotifyComplete("claude-autopilot run completed")
	}
	fmt.Printf("Watching for new tasks (every %s)... Ctrl+C to stop\n", r.WatchInterval)

	deadline := time.Now().Add(r.WatchInterval)
	for {
		if r.ShuttingDown.Load() {
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		tick := time.Second
		if remaining < tick {
			tick = remaining
		}
		time.Sleep(tick)
	}
}
```

- [ ] **Step 5: Wire terminal point 1 (empty queue)**

In `internal/runner/runner.go` `Run()`, replace:

```go
		if len(tasks) == 0 {
			fmt.Println("No tasks found. Add tasks with 'claude-autopilot add' or create YAML files in ~/.claude-autopilot/tasks/")
			return ExitOK
		}
```

with:

```go
		if len(tasks) == 0 {
			if r.Watch {
				r.idleWait(stateDir, runStarted)
				continue
			}
			fmt.Println("No tasks found. Add tasks with 'claude-autopilot add' or create YAML files in ~/.claude-autopilot/tasks/")
			return ExitOK
		}
```

- [ ] **Step 6: Wire terminal point 2 (Step 11 idle break)**

In `internal/runner/runner.go` `Run()`, replace:

```go
		// Step 11: No actionable and no waiting tasks.
		break
	}
```

with:

```go
		// Step 11: No actionable and no waiting tasks.
		if r.Watch {
			r.idleWait(stateDir, runStarted)
			continue
		}
		break
	}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/runner/ -run TestRun_WatchPicksUpLateTask -v`
Expected: PASS — the late task reaches `done` and `Run()` returns `ExitSignal` after shutdown.

- [ ] **Step 8: Commit**

```bash
git add internal/runner/runner.go internal/runner/watch_test.go
git commit -m "feat: keep run loop alive in watcher mode, polling for new tasks"
```

---

## Task 3: Quiet heartbeat (drain-once gating)

The Task 2 `idleWait` prints the summary and heartbeat line on **every** idle tick. The spec requires it printed **once per drain**: once when the queue first goes idle, again only after a task actually runs. This task adds a loop-scoped `drainedOnce` flag, gates the heartbeat behind it, and resets it after any task executes.

**Files:**
- Modify: `internal/runner/watch_test.go` (add test)
- Modify: `internal/runner/runner.go` (`idleWait` signature + gating; `drainedOnce` declaration and reset in `Run()`; both `idleWait` call sites)

- [ ] **Step 1: Write the failing test**

Append to `internal/runner/watch_test.go`:

```go
import "strings" // add to the existing import block if not already present

func TestRun_QuietHeartbeat(t *testing.T) {
	h := newWatchHarness(t)
	r := h.newRunner()
	r.Watch = true
	r.WatchInterval = 20 * time.Millisecond

	done := make(chan int, 1)
	go func() { done <- r.Run() }()

	// ~15 idle cycles on the empty queue.
	time.Sleep(300 * time.Millisecond)
	select {
	case ec := <-done:
		t.Fatalf("Run() exited early with code %d", ec)
	default:
	}

	out := h.output()
	if got := strings.Count(out, "Watching for new tasks"); got != 1 {
		t.Fatalf("heartbeat printed %d times during idle; want exactly 1", got)
	}
	if got := strings.Count(out, "=== Run Summary ==="); got != 1 {
		t.Fatalf("summary printed %d times during idle; want exactly 1", got)
	}

	// A task runs → next drain must reprint exactly once more.
	h.writeTask("t1")
	h.waitStatus("t1", queue.StatusDone, 5*time.Second)
	time.Sleep(200 * time.Millisecond) // several more idle cycles after the drain

	out = h.output()
	if got := strings.Count(out, "Watching for new tasks"); got != 2 {
		t.Fatalf("heartbeat printed %d times total; want exactly 2 (one per drain)", got)
	}
	if got := strings.Count(out, "=== Run Summary ==="); got != 2 {
		t.Fatalf("summary printed %d times total; want exactly 2 (one per drain)", got)
	}

	r.ShuttingDown.Store(true)
	select {
	case ec := <-done:
		if ec != ExitSignal {
			t.Fatalf("Run() returned %d; want ExitSignal (%d)", ec, ExitSignal)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after shutdown")
	}
}
```

> Note: if `strings` is not yet imported in `watch_test.go`, add `"strings"` to its existing import block rather than adding a second `import` statement.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runner/ -run TestRun_QuietHeartbeat -v`
Expected: FAIL — `heartbeat printed N times during idle; want exactly 1` (Task 2's `idleWait` prints every ~20ms tick, so N is ~15).

- [ ] **Step 3: Change `idleWait` to gate on `drainedOnce`**

In `internal/runner/runner.go`, replace the entire `idleWait` method body with the gated version (signature gains a `drainedOnce *bool` parameter):

```go
// idleWait is invoked from Run() when watcher mode is active and there is no
// actionable work. On the first drain after activity (*drainedOnce == false)
// it prints the per-batch summary, fires the completion notification, prints a
// single heartbeat line, and sets *drainedOnce = true. On subsequent
// consecutive idle cycles it prints nothing. It then sleeps r.WatchInterval in
// ticks of at most one second, returning early if a shutdown signal arrives so
// SIGINT/SIGTERM is honored within ~1s (or within the interval when shorter).
func (r *Runner) idleWait(stateDir string, runStarted time.Time, drainedOnce *bool) {
	if !*drainedOnce {
		r.printSummary(stateDir, runStarted)
		if r.Notifier != nil {
			r.Notifier.NotifyComplete("claude-autopilot run completed")
		}
		fmt.Printf("Watching for new tasks (every %s)... Ctrl+C to stop\n", r.WatchInterval)
		*drainedOnce = true
	}

	deadline := time.Now().Add(r.WatchInterval)
	for {
		if r.ShuttingDown.Load() {
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		tick := time.Second
		if remaining < tick {
			tick = remaining
		}
		time.Sleep(tick)
	}
}
```

- [ ] **Step 4: Declare `drainedOnce` in `Run()`**

In `internal/runner/runner.go` `Run()`, find the line declaring `anyFailed`:

```go
	anyFailed := false
```

and add directly below it:

```go
	// drainedOnce gates the watcher heartbeat: summary + notification + the
	// "Watching for new tasks" line print once per drain, not per idle tick.
	// Reset to false whenever a task executes so the next drain reprints.
	drainedOnce := false
```

- [ ] **Step 5: Reset `drainedOnce` after a task executes**

In `internal/runner/runner.go` `Run()`, find the post-execution block:

```go
			if exitResult == ExitSignal {
				return ExitSignal
			}
			// Loop back to pick next task.
			continue
```

and change it to:

```go
			if exitResult == ExitSignal {
				return ExitSignal
			}
			drainedOnce = false
			// Loop back to pick next task.
			continue
```

- [ ] **Step 6: Update both `idleWait` call sites**

In `internal/runner/runner.go` `Run()`, the empty-queue call site changes from:

```go
		if len(tasks) == 0 {
			if r.Watch {
				r.idleWait(stateDir, runStarted)
				continue
			}
```

to:

```go
		if len(tasks) == 0 {
			if r.Watch {
				r.idleWait(stateDir, runStarted, &drainedOnce)
				continue
			}
```

And the Step 11 call site changes from:

```go
		// Step 11: No actionable and no waiting tasks.
		if r.Watch {
			r.idleWait(stateDir, runStarted)
			continue
		}
		break
```

to:

```go
		// Step 11: No actionable and no waiting tasks.
		if r.Watch {
			r.idleWait(stateDir, runStarted, &drainedOnce)
			continue
		}
		break
```

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./internal/runner/ -run TestRun_QuietHeartbeat -v`
Expected: PASS — exactly 1 heartbeat/summary during the first idle stretch, exactly 2 after a task drains again.

- [ ] **Step 8: Run the full watcher suite to confirm no regression in Task 2's test**

Run: `go test ./internal/runner/ -v`
Expected: PASS — `TestRun_WatchPicksUpLateTask`, `TestRun_QuietHeartbeat`, `TestRotateLogIfNeeded`, `TestFormatTaskDuration` all pass.

- [ ] **Step 9: Commit**

```bash
git add internal/runner/runner.go internal/runner/watch_test.go
git commit -m "feat: quiet watcher heartbeat — print summary once per drain"
```

---

## Task 4: README documentation + full regression guard

Documents the feature and runs the entire suite to satisfy the spec's "Regression guard" requirement (existing non-watch tests must pass unchanged).

**Files:**
- Modify: `README.md` (Commands table at `README.md:99`; new subsection before `### Adding Tasks` at `README.md:107`)

- [ ] **Step 1: Update the `run` row in the Commands table**

In `README.md`, replace:

```
| `run` | Start executing the task queue |
```

with:

```
| `run` [`--watch`] [`--watch-interval <dur>`] | Start executing the task queue; `--watch` keeps polling for new tasks instead of exiting |
```

- [ ] **Step 2: Add the "Watcher Mode" subsection**

In `README.md`, find:

```
| `config set\|get\|list\|path` | Manage configuration |

### Adding Tasks
```

and replace it with:

```
| `config set\|get\|list\|path` | Manage configuration |

### Watcher Mode

By default `claude-autopilot run` processes the queue once and exits when there
is nothing left to do. With `--watch` it stays alive instead: when the queue
drains it prints a run summary, then polls for new tasks on a fixed interval.

```bash
# Poll every 10s (default) for newly added tasks
claude-autopilot run --yes --watch

# Poll every 30s instead
claude-autopilot run --yes --watch --watch-interval 30s
```

- `--watch-interval` sets the idle poll interval (default `10s`). It must be
  greater than zero, and is ignored unless `--watch` is also set.
- While watching, tasks added with `claude-autopilot add` (or by dropping YAML
  files into the task directory) are picked up on the next poll. Queued
  `retry`/`cancel` control commands are honored on the next cycle too.
- The summary and `Watching for new tasks…` line are printed once each time
  the queue drains — not on every idle poll — and reprinted after a new batch
  of work completes.
- Failed tasks stay terminal and remain in the queue; the watcher keeps
  polling for new work rather than retrying them.
- Watcher mode runs in the foreground and exits **only** on `Ctrl+C`
  (`SIGINT`) or `SIGTERM`.

### Adding Tasks
```

- [ ] **Step 3: Build and run the full test suite (regression guard)**

Run: `go build ./... && go test ./...`
Expected: PASS — all packages, including the pre-existing `internal/runner` tests (`TestRotateLogIfNeeded`, `TestFormatTaskDuration`) and every other package's tests, pass unchanged, confirming the surgical change did not alter single-run behavior.

- [ ] **Step 4: Verify `go vet` is clean**

Run: `go vet ./...`
Expected: no findings.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document watcher mode (--watch / --watch-interval)"
```

---

## Self-Review

**1. Spec coverage**

| Spec section | Covered by |
|--------------|------------|
| CLI flags `--watch` / `--watch-interval` (defaults, types) | Task 1 Steps 3, 5 |
| `--watch-interval <= 0` returns an error before the loop | Task 1 Step 3 (validation is first statement in `runRun`); Task 1 Step 1 test |
| `--watch-interval` ignored without `--watch` | Task 1 Step 3 (`runWatch &&` guard); flag help text in Step 5 |
| `runRun` passes `Watch`/`WatchInterval` into `Runner` | Task 1 Step 4 |
| Two new `Runner` fields | Task 1 Step 6 note / Task 2 Step 3 |
| Terminal point 1 (`len(tasks)==0`) → idle-wait when `Watch` | Task 2 Step 5 |
| Terminal point 2 ("Step 11" `break`) → idle-wait when `Watch` | Task 2 Step 6 |
| Waiting-task sleep path untouched | No task modifies Step 10 (verified: edits are only at lines 176-179 and 289-290) |
| `idleWait()` helper + loop-scoped `drainedOnce` | Task 2 Step 4, Task 3 Steps 3-6 |
| First drain: summary, NotifyComplete (if non-nil), one heartbeat line, set flag | Task 3 Step 3 |
| Subsequent idle cycles: print nothing | Task 3 Step 3 (gated by `*drainedOnce`) |
| Reset `drainedOnce` when a task executes | Task 3 Step 5 |
| Sleep in ≤1s ticks, check `ShuttingDown` each tick | Task 2 Step 4 / Task 3 Step 3 (loop with `tick` capped at 1s, shorter when interval shorter) |
| Exit only via signal path → `ExitSignal` | Task 2 Step 7 / Task 3 assertions (`continue` keeps loop alive; only `ShuttingDown` returns `ExitSignal`) |
| Fatal `LoadTasksAndInit` errors keep `ExitFatal` | Unchanged code path at `runner.go:168-171` (not modified) |
| Lock + signal handler acquired once for lifetime | Unchanged — change stays inside `Run()`, no per-cycle re-acquisition |
| Test: watch picks up a late task | Task 2 (`TestRun_WatchPicksUpLateTask`) |
| Test: quiet heartbeat | Task 3 (`TestRun_QuietHeartbeat`) |
| Test: interval validation | Task 1 (`TestRunRun_ZeroWatchIntervalReturnsError`, `TestRunRun_NegativeWatchIntervalReturnsError`) |
| Test: regression guard | Task 4 Step 3 (`go test ./...`) |
| README: flags + commands table + Watcher mode subsection | Task 4 Steps 1-2 |

No gaps.

**2. Placeholder scan:** No `TBD`/`TODO`/"add error handling"/"similar to Task N". Every code step shows complete code. The only cross-reference ("the two-field block — see Task 2 Step 3") repeats the exact code inline in Task 1's note, so the engineer never needs to jump.

**3. Type consistency:** `idleWait` is introduced in Task 2 with signature `(stateDir string, runStarted time.Time)` and both call sites match; Task 3 changes the signature to add `drainedOnce *bool` **and** updates both call sites in the same task (Steps 3 + 6) so no call site is left stale. `Runner.Watch` (bool) / `Runner.WatchInterval` (time.Duration) match the `cmd` flag vars `runWatch` (bool, `BoolVar`) / `runWatchInterval` (time.Duration, `DurationVar`) and the struct-literal keys in `cmd/root.go`. `r.printSummary(stateDir, runStarted)` and `r.Notifier.NotifyComplete(string)` match the existing signatures in `runner.go`. `queue.StatusDone` constant used in tests matches `internal/queue/task.go`. `fileutil.AtomicWrite(path, []byte, os.FileMode)` matches `internal/fileutil/atomic.go:25`. Consistent throughout.
