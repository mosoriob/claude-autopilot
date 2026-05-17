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
	t        *testing.T
	home     string
	tasksDir string
	stateDir string
	workDir  string
	mu       sync.Mutex
	outBuf   *bytes.Buffer
	stdoutW  *os.File
	prevOut  *os.File
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
