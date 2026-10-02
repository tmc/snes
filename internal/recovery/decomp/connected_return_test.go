package decomp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRetainedConnectedReturnMetadata(t *testing.T) {
	path := os.Getenv("SNES_CONNECTED_RETURN_CONFIG")
	if path == "" {
		t.Skip("set SNES_CONNECTED_RETURN_CONFIG for the retained connected case")
	}
	var cfg struct {
		ROM, ROMSHA, Policy, PolicySHA, Cases, CasesSHA, Root string
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	read := func(path, sha string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if sha == "" || fmt.Sprintf("%x", sha256.Sum256(b)) != sha {
			t.Fatalf("input hash mismatch: %s", path)
		}
		return b
	}
	rom := read(cfg.ROM, cfg.ROMSHA)
	var policy AdmissionPolicy
	if err := json.Unmarshal(read(cfg.Policy, cfg.PolicySHA), &policy); err != nil {
		t.Fatal(err)
	}
	var original ReplayCase
	if err := json.Unmarshal(bytes.SplitN(read(cfg.Cases, cfg.CasesSHA), []byte("\n"), 2)[0], &original); err != nil {
		t.Fatal(err)
	}
	v, err := NewEvidenceVerifierWithPolicy(cfg.Root, policy, rom)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.PrefetchFixtures([]ReplayCase{original}); err != nil {
		t.Fatal(err)
	}
	if rec, err := v.Admit(&original, nil, nil); err != nil || !rec.Admitted {
		t.Fatalf("genuine case refused: %+v: %v", rec, err)
	}
	t.Logf("genuine %s admitted: terminal=$%06x return_seq=%d call_seq=%d", original.CaseID, original.ReturnInsnPC, original.ReturnSeq, original.CallSeq)
	for _, tt := range []struct {
		name string
		edit func(*ReplayCase)
		want string
	}{
		{"return_address", func(c *ReplayCase) { c.ReturnInsnPC++ }, "connected routine declared return PC mismatch"},
		{"missing_return_address", func(c *ReplayCase) { c.ReturnInsnPC = 0 }, "connected routine declared return PC mismatch"},
		{"asserted_continuation", func(c *ReplayCase) { c.ReturnSeq = c.ExitSeq }, "connected routine continuation sequence is unsupported"},
		{"absent_continuation", func(c *ReplayCase) { c.ReturnSeq = c.ExitSeq + 1 }, "fixture lacks requested cpu_insn seq"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var c ReplayCase
			if err := json.Unmarshal(b, &c); err != nil {
				t.Fatal(err)
			}
			tt.edit(&c)
			c.CaseHash, c.AdmissionDigest = "", ""
			PopulateHashes(&c)
			rec, err := v.Admit(&c, nil, nil)
			if err == nil || rec.Admitted || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("rehashed return mutation: admitted=%v error=%v; want %q", rec.Admitted, err, tt.want)
			}
			t.Logf("semantic refusal: %v", err)
		})
	}
}

func TestConnectedReturnMetadata(t *testing.T) {
	last := captureCPUInsn{Seq: 100, Entry: cpuStateWithCycles{PB: 1, PC: 0x8101}}
	original := ReplayCase{ExitSeq: 100, ReturnInsnPC: 0x018101}
	for _, tt := range []struct {
		name string
		edit func(*ReplayCase)
		want string
	}{
		{"observed_exit", func(c *ReplayCase) {}, ""},
		{"wrong_PC", func(c *ReplayCase) { c.ReturnInsnPC++ }, "declared return PC mismatch"},
		{"missing_PC", func(c *ReplayCase) { c.ReturnInsnPC = 0 }, "declared return PC mismatch"},
		{"wrong_sequence", func(c *ReplayCase) { c.ExitSeq++ }, "terminal sequence mismatch"},
		{"asserted_continuation", func(c *ReplayCase) { c.ReturnSeq = 101 }, "continuation sequence is unsupported"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := original
			tt.edit(&c)
			err := verifyConnectedReturnMetadata(&c, last, 0x018101)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
	if err := verifyConnectedReturnMetadata(&original, last, 0x018102); err == nil {
		t.Fatal("different contract terminal accepted")
	}
}
