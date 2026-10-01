package candidates

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/tmc/snes/internal/recovery"
	"io"
)

// Occurrence is a reported execution interval. Complete describes the input
// record's bounds, not independent verification of its source capture.
type Occurrence struct {
	CaseID       string           `json:"case_id"`
	SourceID     string           `json:"source_id"`
	RunID        string           `json:"run_id"`
	StreamSHA256 string           `json:"stream_sha256"`
	ROMSHA256    string           `json:"rom_sha256"`
	EntryPC      uint32           `json:"entry_pc"`
	ReturnInsnPC uint32           `json:"return_insn_pc"`
	EntrySeq     uint64           `json:"entry_seq"`
	ExitSeq      uint64           `json:"exit_seq"`
	Complete     bool             `json:"complete"`
	Interrupted  bool             `json:"interrupted"`
	EntryContext recovery.Context `json:"reported_entry_context"`
	ExitContext  recovery.Context `json:"reported_exit_context"`
}

// ReadInventory reads routine-case JSONL without trusting admission or eligibility
// fields. It hashes the input bytes and preserves run and occurrence identities.
// Each line is bounded to 4 MiB. Duplicate occurrences are rejected.
func ReadInventory(r io.Reader, sourceID string) ([]Occurrence, Source, error) {
	if r == nil || sourceID == "" {
		return nil, Source{}, fmt.Errorf("read inventory: reader and source id are required")
	}
	h := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(r, h))
	scanner.Buffer(make([]byte, 65536), 4<<20)
	var out []Occurrence
	seen := map[string]bool{}
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var record struct {
			Schema string `json:"schema_version"`
			Occurrence
			ReturnSeq        uint64          `json:"return_seq"`
			InstructionCount uint64          `json:"instruction_count"`
			Initial          *statusSnapshot `json:"initial_state"`
			Exit             *statusSnapshot `json:"observed_exit_state"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, Source{}, fmt.Errorf("read inventory line %d: %w", line, err)
		}
		if record.Schema != "snes-routine-case-v1" {
			return nil, Source{}, fmt.Errorf("read inventory line %d: unsupported schema %q", line, record.Schema)
		}
		o := record.Occurrence
		o.SourceID = sourceID
		o.Complete = false
		o.EntryContext = statusContext(record.Initial)
		o.ExitContext = statusContext(record.Exit)
		if o.CaseID == "" || o.RunID == "" || o.StreamSHA256 == "" || o.ROMSHA256 == "" || o.EntryPC > 0xFFFFFF || o.ReturnInsnPC > 0xFFFFFF {
			return nil, Source{}, fmt.Errorf("read inventory line %d: missing or invalid identity", line)
		}
		key := fmt.Sprintf("%s:%d:%d", o.RunID, o.EntryPC, o.EntrySeq)
		if seen[key] {
			return nil, Source{}, fmt.Errorf("read inventory line %d: duplicate occurrence", line)
		}
		seen[key] = true
		if !o.Interrupted && o.ExitSeq >= o.EntrySeq && o.ExitSeq != ^uint64(0) && record.ReturnSeq == o.ExitSeq+1 && record.InstructionCount == o.ExitSeq-o.EntrySeq+1 {
			o.Complete = true
		}
		out = append(out, o)
	}
	if err := scanner.Err(); err != nil {
		return nil, Source{}, fmt.Errorf("read inventory: %w", err)
	}
	return out, Source{ID: sourceID, SHA256: hex.EncodeToString(h.Sum(nil)), Kind: "reported_routine_case_inventory"}, nil
}

type statusSnapshot struct {
	P *uint8 `json:"p"`
	E *bool  `json:"e"`
}

func statusContext(s *statusSnapshot) recovery.Context {
	c := recovery.Context{E: "unknown", M: "unknown", X: "unknown", C: "unknown"}
	if s == nil {
		return c
	}
	flag := func(v bool) string {
		if v {
			return "set"
		}
		return "clear"
	}
	if s.E != nil {
		c.E = flag(*s.E)
	}
	if s.P != nil {
		c.M = flag(*s.P&0x20 != 0)
		c.X = flag(*s.P&0x10 != 0)
		c.C = flag(*s.P&1 != 0)
	}
	return c
}
