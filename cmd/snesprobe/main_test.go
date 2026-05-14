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

func TestSmokeOutDirWritesManifestAndStates(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "cwd")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Chdir(cwd)

	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, smokeTestROM(), 0o666); err != nil {
		t.Fatalf("WriteFile rom: %v", err)
	}
	svc := snesprobe.New()
	if _, err := svc.LoadROM(romPath); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	cp, err := svc.Snapshot("seed")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	statePath := filepath.Join(dir, "seed.state")
	if _, err := svc.ExportCheckpoint(snesprobe.ExportCheckpointRequest{Checkpoint: cp.Name, Path: statePath}); err != nil {
		t.Fatalf("ExportCheckpoint seed: %v", err)
	}

	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"smoke",
		"--rom", romPath,
		"--state", statePath,
		"--frames", "2",
		"--watch", "w0=wram:0x0:1",
		"--out-dir", outDir,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run smoke code = %d, stderr = %s", code, stderr.String())
	}

	var summary struct {
		ArtifactDir string `json:"artifact_dir"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("Unmarshal stdout: %v\n%s", err, stdout.String())
	}
	if summary.ArtifactDir != outDir {
		t.Fatalf("artifact_dir = %q, want %q", summary.ArtifactDir, outDir)
	}

	var manifest smokeArtifactManifest
	data, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal manifest: %v", err)
	}
	if manifest.SchemaVersion != "snesprobe-branch-checkpoint-v1" {
		t.Fatalf("schema_version = %q", manifest.SchemaVersion)
	}
	if manifest.Provenance.State != "derived" || manifest.SourceCheckpoint.Path != filepath.Join(outDir, "frame0.state") {
		t.Fatalf("manifest provenance/source = %+v / %+v", manifest.Provenance, manifest.SourceCheckpoint)
	}
	if len(manifest.Branches) != 2 {
		t.Fatalf("branches = %d, want 2", len(manifest.Branches))
	}
	for _, branch := range manifest.Branches {
		if branch.ExportedStatePath != filepath.Join(outDir, branch.Checkpoint+".state") {
			t.Fatalf("%s exported path = %q", branch.Name, branch.ExportedStatePath)
		}
		if branch.ExportedStateSHA256 == "" || branch.ExportedStateSHA256 != branch.FinalStateHash {
			t.Fatalf("%s hashes = exported %q final %q", branch.Name, branch.ExportedStateSHA256, branch.FinalStateHash)
		}
		if branch.ExportedStateBytes == 0 || branch.CheckpointStateBytes != branch.ExportedStateBytes {
			t.Fatalf("%s sizes = exported %d checkpoint %d", branch.Name, branch.ExportedStateBytes, branch.CheckpointStateBytes)
		}

		fresh := snesprobe.New()
		if _, err := fresh.LoadROM(romPath); err != nil {
			t.Fatalf("fresh LoadROM: %v", err)
		}
		if _, err := fresh.LoadState(branch.ExportedStatePath, false); err != nil {
			t.Fatalf("fresh LoadState %s: %v", branch.Name, err)
		}
	}
	for _, name := range []string{"frame0.state", "right-final.state", "up-final.state"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(cwd, name)); !os.IsNotExist(err) {
			t.Fatalf("state blob leaked into cwd %s: %v", name, err)
		}
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

func smokeTestROM() []byte {
	rom := make([]byte, 0x8000)
	for i := 0; i < 0x100; i++ {
		rom[i] = 0xea
	}
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}
