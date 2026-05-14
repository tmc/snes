package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/sneslive/v1"
	"github.com/tmc/snes/internal/snesprobe"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"bogus"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHashJSONStable(t *testing.T) {
	a := hashJSON(map[string]uint64{"x": 1, "y": 2})
	b := hashJSON(map[string]uint64{"y": 2, "x": 1})
	if a != b {
		t.Fatalf("hashJSON order changed: %s != %s", a, b)
	}
}

func TestServeSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "probe.sock")
	svc := snesprobe.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- serveSocket(ctx, socketPath, svc)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(snesprobe.Request{ID: "1", Method: "reset"}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var resp snesprobe.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if resp.ID != "1" || !strings.Contains(resp.Error, "no rom loaded") {
		t.Fatalf("response = %+v", resp)
	}
	cancel()
}

func TestServeGRPCUnixSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "probe-grpc.sock")
	svc := snesprobe.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- serveGRPC(ctx, "unix", socketPath, svc)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grpc socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	conn, err := grpc.DialContext(ctx, "unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()
	client := sneslivev1.NewSNESLiveServiceClient(conn)
	if _, err := client.Reset(ctx, &sneslivev1.ResetRequest{}); err == nil || !strings.Contains(err.Error(), "no rom loaded") {
		t.Fatalf("Reset error = %v, want no rom loaded", err)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("serveGRPC: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveGRPC did not exit")
	}
}
