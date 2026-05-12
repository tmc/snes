package snesprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/emulator"
)

func TestServiceCheckpointForkAndRun(t *testing.T) {
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, testROM(), 0o666); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	svc := New()
	if _, err := svc.LoadROM(romPath); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	cp, err := svc.Snapshot("frame0")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if cp.Hash == "" || cp.Bytes == 0 {
		t.Fatalf("checkpoint = %+v, want hash and bytes", cp)
	}
	fork, err := svc.Fork("frame0", "branch")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if fork.Hash != cp.Hash {
		t.Fatalf("fork hash = %s, want %s", fork.Hash, cp.Hash)
	}

	watches := WatchRequest{Watches: []WatchField{{Name: "w0", Space: "wram", Addr: 0, Width: 1}}}
	right, err := svc.RunFromCheckpoint(context.Background(), RunFromCheckpointRequest{
		Checkpoint:    "frame0",
		InputSequence: []uint16{emulator.StandardButtonRight, emulator.StandardButtonRight},
		WatchSpec:     watches,
		EventFilter:   []string{"frame", "input", "watch"},
	})
	if err != nil {
		t.Fatalf("RunFromCheckpoint right: %v", err)
	}
	up, err := svc.RunFromCheckpoint(context.Background(), RunFromCheckpointRequest{
		Checkpoint:    "frame0",
		InputSequence: []uint16{emulator.StandardButtonUp, emulator.StandardButtonUp},
		WatchSpec:     watches,
	})
	if err != nil {
		t.Fatalf("RunFromCheckpoint up: %v", err)
	}
	if right.InputHash == up.InputHash {
		t.Fatal("input hashes matched for different input sequences")
	}
	if right.ComponentHashes == nil || right.FramebufferHash == "" || right.FinalStateHash == "" {
		t.Fatalf("run result missing hashes: %+v", right)
	}
	if _, ok := right.Watches["w0"]; !ok {
		t.Fatalf("run result missing watch: %+v", right.Watches)
	}
}

func TestServiceServeJSONL(t *testing.T) {
	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, testROM(), 0o666); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := Request{
		ID:     "1",
		Method: "load_rom",
		Params: mustJSON(t, map[string]string{"path": romPath}),
	}
	var in, out bytes.Buffer
	if err := json.NewEncoder(&in).Encode(req); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := New().Serve(context.Background(), &in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(out.String(), `"id":"1"`) || !strings.Contains(out.String(), `"status":"loaded"`) {
		t.Fatalf("response = %s", out.String())
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}

func testROM() []byte {
	rom := make([]byte, 0x8000)
	for i := 0; i < 0x100; i++ {
		rom[i] = 0xea
	}
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}
