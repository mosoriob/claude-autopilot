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
