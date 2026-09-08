package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/snes/internal/snesprobe"
)

func TestServeSocketCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- serveSocket(ctx, filepath.Join(t.TempDir(), "probe.sock"), snesprobe.New()) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serve = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled listener did not stop")
	}
}
