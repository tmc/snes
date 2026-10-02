package exploration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/recovery/candidates"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/workflow"
)

func TestROMSnapshotDispatchTransientSubstitution(t *testing.T) {
	root := testDir(t)
	rom := make([]byte, 1<<16)
	rom[0] = 0x80
	rom[1] = 0xfe
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	romPath := filepath.Join(root, "operator-rom.bin")
	if err := os.WriteFile(romPath, rom, 0600); err != nil {
		t.Fatal(err)
	}
	sha := digest(rom)
	sub := append([]byte(nil), rom...)
	sub[10] = 1
	subSHA := digest(sub)
	idx := coverage.NewIndex(sha)
	idx.Runs["r"] = coverage.RunInfo{ID: "r", ROM_SHA256: sha, IsComplete: true, Outcome: "complete", StreamSHA: "synthetic-stream"}
	idx.Sites = []coverage.Site{{RunID: "r", Address: 0x8000, Hits: 1}}
	cov := testPin(t, root, "coverage.json", idx)
	dis := testPin(t, root, "discovery.json", candidates.Report{ROMSHA256: sha, Sources: []candidates.Source{{Kind: "coverage_index", SHA256: cov.SHA256}}, Candidates: []candidates.Candidate{{ID: "target", Entry: 0x8000, InstructionCount: 2}}})
	old := testPin(t, root, "previous.json", workflow.BatchReport{})
	prev := testPin(t, root, "summary.json", map[string]any{"rom_hash": sha, "trace_hash": "old"})
	started := filepath.Join(root, "started")
	release := filepath.Join(root, "release")
	restore := filepath.Join(root, "restore")
	restored := filepath.Join(root, "restored")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
rom= out= receipt= summary=
shift
while [ "$#" -gt 0 ]; do
 key="$1"; val="$2"; shift 2
 case "$key" in -rom) rom="$val";; -out) out="$val";; -receipt) receipt="$val";; -summary) summary="$val";; esac
done
case "$out" in *fixture.jsonl) : > '%s'; while [ ! -e '%s' ]; do sleep 0.01; done;; esac
romsha="$(shasum -a 256 "$rom" | cut -d' ' -f1)"
printf '{"kind":"run","run":{"rom_sha256":"%%s","engine_revision":"synthetic-control","events":["bus"]}}\n' "$romsha" > "$out"
tracesha="$(shasum -a 256 "$out" | cut -d' ' -f1)"
printf '{"schema":2,"outcome":"complete","stream_sha256":"%%s","event_count":1}\n' "$tracesha" > "$receipt"
printf '{"rom_hash":"%%s","emulator":"synthetic-control","trace_hash":"%%s","event_kinds":["bus"],"op":"write"}\n' "$romsha" "$tracesha" > "$summary"
printf 'producer read ROM %%s from %%s\n' "$romsha" "$rom"
case "$out" in *history.jsonl) : > '%s'; while [ ! -e '%s' ]; do sleep 0.01; done;; esac
`, started, release, restore, restored)
	toolPath := filepath.Join(root, "synthetic-producer")
	if err := os.WriteFile(toolPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c := Config{Schema: "snes-exploration-config-v1", ROM: workflow.Input{Path: romPath, SHA256: sha}, Discovery: dis, Coverage: cov, Previous: old, PreviousCapture: prev, Sources: []workflow.Input{dis}, TraceTool: workflow.Input{Path: toolPath, SHA256: digest([]byte(script))}, Target: "target", CapturePC: "cpu:00:8000-cpu:00:8001", BaselineFrames: 1, MaxSites: 100, MaxInstructions: 10000000, MaxTraceEvents: 10, MaxTraceBytes: 10000, ProjectRevision: "independent-synthetic"}
	for i := 0; i < 8; i++ {
		a := make([]uint16, 30)
		a[0] = uint16(i)
		c.Schedules = append(c.Schedules, a)
	}
	pin := testPin(t, root, "config.json", c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		wait := func(path string) error {
			for {
				if _, err := os.Stat(path); err == nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
			}
		}
		if err := wait(started); err != nil {
			done <- err
			return
		}
		if err := os.WriteFile(romPath, sub, 0600); err != nil {
			done <- err
			return
		}
		if err := os.WriteFile(release, nil, 0600); err != nil {
			done <- err
			return
		}
		if err := wait(restore); err != nil {
			done <- err
			return
		}
		if err := os.WriteFile(romPath, rom, 0600); err != nil {
			done <- err
			return
		}
		done <- os.WriteFile(restored, nil, 0600)
	}()
	out := filepath.Join(root, "published")
	r, err := Run(ctx, out, pin, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.Stage != "complete_discovery" || r.Capture == nil || r.Capture.Queue != nil {
		t.Fatalf("unexpected result %+v capture %+v", r, r.Capture)
	}
	if _, err := load(pin); err != nil {
		t.Fatalf("restored operator pin should verify: %v", err)
	}
	if r.OperatorROM != c.ROM || r.RuntimeROMSHA256 != sha {
		t.Fatalf("ROM identities not retained: %+v", r)
	}
	var privatePath string
	for _, kind := range []string{"fixture", "capture", "history"} {
		command, err := os.ReadFile(filepath.Join(out, "capture", kind+".command.json"))
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		if err := json.Unmarshal(command, &args); err != nil {
			t.Fatal(err)
		}
		path := args[2]
		if path == romPath || (privatePath != "" && path != privatePath) {
			t.Fatalf("producer ROM paths differ: %q", args)
		}
		privatePath = path
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("private ROM retained: %q: %v", path, err)
		}
		b, err := os.ReadFile(filepath.Join(out, "capture", kind+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), sha) || strings.Contains(string(b), subSHA) {
			t.Fatalf("producer did not consume verified original ROM in %s stream: %s", kind, b)
		}
	}
	if err := filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if digest(b) == sha || digest(b) == subSHA {
			return fmt.Errorf("ROM retained in output: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(map[string]any{"status": "VERIFIED_PRIVATE_ROM_SNAPSHOT", "operator_rom_sha256": sha, "producer_rom_sha256": sha, "stage": r.Stage, "capture_reason": r.Capture.Reason, "qualified_cases": 0, "original_pin_reverified": true, "scope": "synthetic producer exercises mutable ROM dispatch path; no real emulator recapture or fabricated qualification"}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "verdict.json"), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("operator ROM %s; all three published streams ROM %s; restored pin accepted; capture refuses: %s", sha, sha, r.Capture.Reason)
}
