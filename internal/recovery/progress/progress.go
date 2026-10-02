package progress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/coverage"
	"github.com/tmc/snes/internal/recovery/workflow"
)

// Config selects externally pinned inputs. Batch is optional.
type Config struct {
	Schema   string         `json:"schema"`
	Recovery workflow.Input `json:"recovery"`
	Coverage workflow.Input `json:"coverage"`
	Batch    *Batch         `json:"batch,omitempty"`
}

// Batch identifies a retained batch and its original completion pin.
type Batch struct {
	Directory      string         `json:"directory"`
	Config         workflow.Input `json:"config"`
	ManifestSHA256 string         `json:"manifest_sha256"`
}

// Metric reports one unit and scope. Nil Count means unavailable, never zero.
type Metric struct {
	Name  string  `json:"name"`
	Count *string `json:"count"`
	Unit  string  `json:"unit"`
	Scope string  `json:"scope"`
}

// Bank describes a physical 32 KiB ROM segment, not a CPU bank or code denominator.
type Bank struct {
	Index         int  `json:"index"`
	Offset        int  `json:"offset"`
	Bytes         int  `json:"bytes"`
	DecodedBytes  int  `json:"decoded_bytes"`
	ReachedStarts *int `json:"reached_starts"`
}

// Candidate preserves a selected batch outcome and its next inspection step.
type Candidate struct {
	ID        string `json:"id"`
	Entry     uint32 `json:"entry"`
	Stage     string `json:"stage"`
	Status    string `json:"status"`
	Captured  *int   `json:"captured"`
	Qualified int    `json:"qualified"`
	Emitted   bool   `json:"emitted"`
	Reason    string `json:"reason"`
	Action    string `json:"action"`
}

// Source identifies immutable bytes served as evidence, not authority.
type Source struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// Report has distinct units; its metrics must not be combined into a percent done.
type Report struct {
	Schema             string      `json:"schema"`
	ROMSHA256          string      `json:"rom_sha256"`
	ROMBytes           int         `json:"rom_bytes"`
	ConfigSHA256       string      `json:"config_sha256"`
	Metrics            []Metric    `json:"metrics"`
	Banks              []Bank      `json:"banks"`
	Candidates         []Candidate `json:"candidates"`
	Sources            []Source    `json:"sources"`
	BatchStatus        string      `json:"batch_status"`
	BatchRuntimeSHA256 string      `json:"batch_runtime_sha256,omitempty"`
	Limitations        []string    `json:"limitations"`
	evidence           [][]byte
}

// Evidence returns a copy of one measured source; index corresponds to Sources.
func (r *Report) Evidence(index int) ([]byte, bool) {
	if r == nil || index < 0 || index >= len(r.evidence) {
		return nil, false
	}
	return append([]byte(nil), r.evidence[index]...), true
}

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func read(in workflow.Input) ([]byte, error) {
	if !filepath.IsAbs(in.Path) || !validHash(in.SHA256) {
		return nil, fmt.Errorf("absolute input and SHA-256 required")
	}
	f, err := os.Open(in.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 64<<20 {
		return nil, fmt.Errorf("input exceeds 64 MiB")
	}
	if digest(b) != in.SHA256 {
		return nil, fmt.Errorf("input digest differs: %s", in.Path)
	}
	return b, nil
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == fmt.Sprintf("%x", b)
}
func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func metric(name string, n *string, unit, scope string) Metric { return Metric{name, n, unit, scope} }
func count(n int) *string                                      { s := fmt.Sprint(n); return &s }
func (r *Report) source(name string, b []byte) {
	r.Sources = append(r.Sources, Source{name, digest(b)})
	r.evidence = append(r.evidence, append([]byte(nil), b...))
}

// Load verifies input bytes and revalidates optional recorded batch qualification.
// It never executes C or writes to the project. Invalid batch evidence refuses
// the load rather than publishing its claimed qualification as progress.
func Load(ctx context.Context, path, pin string) (*Report, error) {
	b, e := read(workflow.Input{Path: path, SHA256: pin})
	if e != nil {
		return nil, e
	}
	var c Config
	if e = strict(b, &c); e != nil {
		return nil, e
	}
	if c.Schema != "snes-progress-config-v1" {
		return nil, fmt.Errorf("unsupported progress config")
	}
	db, e := read(c.Recovery)
	if e != nil {
		return nil, e
	}
	doc, e := recovery.Decode(bytes.NewReader(db))
	if e != nil {
		return nil, e
	}
	r, e := summarize(doc, nil)
	if e != nil {
		return nil, e
	}
	r.ConfigSHA256 = pin
	r.source("config.json", b)
	r.source("recovery.json", db)
	if c.Coverage.Path != "" || c.Coverage.SHA256 != "" {
		cb, e := read(c.Coverage)
		if e != nil {
			return nil, e
		}
		var idx coverage.Index
		if e = strict(cb, &idx); e != nil {
			return nil, e
		}
		r, e = summarize(doc, &idx)
		if e != nil {
			return nil, e
		}
		r.ConfigSHA256 = pin
		r.source("config.json", b)
		r.source("recovery.json", db)
		r.source("coverage.json", cb)
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if c.Batch != nil {
		batch, e := workflow.ResumeBatch(ctx, c.Batch.Directory, c.Batch.Config, c.Batch.ManifestSHA256)
		if e != nil {
			return nil, fmt.Errorf("recorded batch: %w", e)
		}
		bb, e := read(workflow.Input{Path: filepath.Join(c.Batch.Directory, "manifest.json"), SHA256: c.Batch.ManifestSHA256})
		if e != nil {
			return nil, e
		}
		if e = r.addBatch(c.Batch.Directory, batch); e != nil {
			return nil, e
		}
		r.source("batch-manifest.json", bb)
	}
	return r, nil
}

func summarize(doc *recovery.Document, idx *coverage.Index) (*Report, error) {
	size := doc.ROM.NormalizedSize
	if size < 1 || size > 16<<20 || !validHash(doc.ROM.NormalizedSHA256) {
		return nil, fmt.Errorf("invalid normalized ROM identity")
	}
	r := &Report{Schema: "snes-progress-v1", ROMSHA256: doc.ROM.NormalizedSHA256, ROMBytes: int(size), BatchStatus: "unavailable", Candidates: []Candidate{}, Limitations: []string{"Recorded evidence only; no whole-game completion percentage.", "ROM includes data and padding; decoded-byte coverage is not percent of game logic recovered.", "Unobserved code is not proven unreachable. Reached and decoded count different units.", "Qualification is sampled CPU and ordered WRAM agreement under recorded runtime, not timing, frames or hardware equivalence.", "Batch metrics cover only selected entries; they do not inventory every generated artifact or qualified routine.", "Workflow checkpoints do not establish full call/data closure or behavioral milestones."}}
	decoded := make([]bool, int(size))
	starts := map[uint32]bool{}
	byteValues := map[uint32]byte{}
	for _, in := range doc.Instructions {
		raw, e := hex.DecodeString(in.Bytes)
		if e != nil || len(raw) < 1 || len(raw) > 4 || in.Address > 0xffffff || uint64(in.Offset)+uint64(len(raw)) > uint64(size) {
			return nil, fmt.Errorf("instruction outside normalized ROM")
		}
		if raw[0] != in.Opcode {
			return nil, fmt.Errorf("instruction opcode differs from bytes")
		}
		starts[in.Offset] = true
		for i, v := range raw {
			off := in.Offset + uint32(i)
			if old, ok := byteValues[off]; ok && old != v {
				return nil, fmt.Errorf("conflicting decoded bytes")
			}
			byteValues[off] = v
			decoded[off] = true
		}
	}
	n := 0
	for _, v := range decoded {
		if v {
			n++
		}
	}
	r.Metrics = []Metric{metric("Reached", nil, "physical ROM instruction starts", "Coverage index unavailable"), metric("Decoded", count(n), "unique ROM bytes", fmt.Sprintf("%d physical starts; %d context encodings in recovery document", len(starts), len(doc.Instructions))), metric("Captured", nil, "complete executions", "Batch evidence unavailable"), metric("Emitted", nil, "selected C candidates", "Batch evidence unavailable"), metric("Qualified", nil, "sampled replay cases", "Batch evidence unavailable")}
	reached := map[uint32]bool{}
	if idx != nil {
		if idx.Format != coverage.Format || idx.Schema != coverage.SchemaVersion || idx.ROMHash != r.ROMSHA256 {
			return nil, fmt.Errorf("coverage format or ROM identity differs")
		}
		if err := validateCoverage(idx); err != nil {
			return nil, err
		}
		hits := new(big.Int)
		unmapped := 0
		for id, run := range idx.Runs {
			if run.ID != id || run.ROM_SHA256 != r.ROMSHA256 || !validHash(run.StreamSHA) {
				return nil, fmt.Errorf("invalid coverage run identity")
			}
		}
		for _, s := range idx.Sites {
			if _, ok := idx.Runs[s.RunID]; !ok || s.Address > 0xffffff || s.Hits == 0 {
				return nil, fmt.Errorf("invalid coverage site")
			}
			hits.Add(hits, new(big.Int).SetUint64(s.Hits))
			if !s.HasROMOffset {
				unmapped++
				continue
			}
			if s.Offset >= uint32(size) {
				return nil, fmt.Errorf("coverage offset outside ROM")
			}
			reached[s.Offset] = true
		}
		r.Metrics[0] = metric("Reached", count(len(reached)), "physical ROM instruction starts", fmt.Sprintf("%s recorded hits across %d runs; %d unmapped records excluded; no unobserved-zero inference", hits.String(), len(idx.Runs), unmapped))
	}
	for off := 0; off < int(size); off += 32768 {
		bank := Bank{Index: off / 32768, Offset: off, Bytes: min(32768, int(size)-off)}
		for j := off; j < off+bank.Bytes; j++ {
			if decoded[j] {
				bank.DecodedBytes++
			}
		}
		if idx != nil {
			bank.ReachedStarts = new(int)
			for start := range reached {
				if int(start) >= off && int(start) < off+bank.Bytes {
					*bank.ReachedStarts++
				}
			}
		}
		r.Banks = append(r.Banks, bank)
	}
	return r, nil
}

func (r *Report) addBatch(dir string, b *workflow.BatchReport) error {
	captured, emitted, qualified, measured := 0, 0, 0, 0
	for _, row := range b.Rows {
		if row.ROMSHA256 != r.ROMSHA256 {
			return fmt.Errorf("batch and project ROM identities differ")
		}
		cand := Candidate{ID: row.CandidateID, Entry: row.Entry, Stage: row.Stage, Status: row.Status, Reason: row.Reason, Qualified: row.State.QualifiedCases}
		if row.Extraction != nil {
			cand.Captured = new(int)
			*cand.Captured = row.Extraction.CompleteExecutions
			captured += *cand.Captured
			measured++
		}
		for _, result := range row.Results {
			if result.SourceSHA256 == "" {
				continue
			}
			// Resolve the generated file from the verified journal inventory.
			// ValidationDir is absent for emitted-but-refused candidates.
			if !localPath(result.Directory) {
				return fmt.Errorf("invalid generated source directory")
			}
			suffix := filepath.Join("queue", result.Directory, "generated.c")
			var artifact string
			for name, pin := range row.State.Artifacts {
				if localPath(name) && strings.HasSuffix(name, string(filepath.Separator)+suffix) && pin == result.SourceSHA256 {
					if artifact == "" || name < artifact {
						artifact = name
					}
				}
			}
			if artifact == "" {
				return fmt.Errorf("generated source absent from journal inventory")
			}
			base := filepath.Join(row.Directory, artifact)
			if b.Artifacts[base] != result.SourceSHA256 {
				return fmt.Errorf("generated source differs from batch inventory")
			}
			raw, e := read(workflow.Input{Path: filepath.Join(dir, base), SHA256: result.SourceSHA256})
			if e != nil {
				return e
			}
			if len(raw) == 0 {
				return fmt.Errorf("empty generated source")
			}
			cand.Emitted = true
		}
		if cand.Emitted {
			emitted++
		}
		qualified += cand.Qualified
		switch {
		case row.Status == "accepted":
			cand.Action = "Inspect sampled receipts and untested paths before expanding the entry contract."
		case cand.Captured == nil:
			cand.Action = "Deliver an explicit complete capture interval; no extraction measurement is available."
		case *cand.Captured == 0:
			cand.Action = "Compare capture PC/frame filters with observed coverage; recapture the entry and complete return interval."
		case !cand.Emitted:
			cand.Action = "Inspect decoder, entry-context and unsupported-opcode refusals before emission."
		default:
			cand.Action = "Inspect admission and replay discrepancies; keep emitted C separate from qualification."
		}
		r.Candidates = append(r.Candidates, cand)
	}
	sort.Slice(r.Candidates, func(i, j int) bool { return r.Candidates[i].Entry < r.Candidates[j].Entry })
	scope := fmt.Sprintf("%d selected entries in revalidated recorded batch; not all ROM routines", len(b.Rows))
	r.Metrics[2] = metric("Captured", count(captured), "known complete executions", fmt.Sprintf("%d of %d selected entries have extraction measurements", measured, len(b.Rows)))
	if measured == 0 {
		r.Metrics[2].Count = nil
	}
	r.Metrics[3] = metric("Emitted", count(emitted), "selected C candidates", scope)
	r.Metrics[4] = metric("Qualified", count(qualified), "sampled replay cases", fmt.Sprintf("%d accepted candidates; %s", b.Accepted, scope))
	r.BatchStatus = "revalidated recorded qualification"
	r.BatchRuntimeSHA256 = b.RuntimeSHA256
	return nil
}
func localPath(p string) bool {
	return p != "." && p != ".." && p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.HasPrefix(p, ".."+string(filepath.Separator))
}

func validateCoverage(idx *coverage.Index) error {
	seen := map[string]bool{}
	totals := map[string]*big.Int{}
	for _, s := range idx.Sites {
		if _, ok := idx.Runs[s.RunID]; !ok {
			return fmt.Errorf("coverage site has no run")
		}
		key := fmt.Sprintf("%s|%s|%d|%d|%t|%v", s.RunID, s.InstructionID, s.Address, s.Offset, s.HasROMOffset, s.Context)
		if seen[key] {
			return fmt.Errorf("duplicate coverage site")
		}
		seen[key] = true
		if len(s.Frames) == 0 || len(s.Frames) != len(s.FrameHits) || s.FirstFrame != s.Frames[0] || s.LastFrame != s.Frames[len(s.Frames)-1] || s.FirstSeq > s.LastSeq {
			return fmt.Errorf("invalid coverage frame bounds")
		}
		sum := new(big.Int)
		for i, frame := range s.Frames {
			if s.FrameHits[i] == 0 || (i > 0 && frame <= s.Frames[i-1]) {
				return fmt.Errorf("invalid coverage frame counts")
			}
			sum.Add(sum, new(big.Int).SetUint64(s.FrameHits[i]))
		}
		if sum.Cmp(new(big.Int).SetUint64(s.Hits)) != 0 {
			return fmt.Errorf("coverage hit/frame totals differ")
		}
		if totals[s.RunID] == nil {
			totals[s.RunID] = new(big.Int)
		}
		totals[s.RunID].Add(totals[s.RunID], sum)
	}
	for id, run := range idx.Runs {
		sum := totals[id]
		if sum == nil {
			sum = new(big.Int)
		}
		if sum.Cmp(new(big.Int).SetUint64(run.EventCount)) != 0 {
			return fmt.Errorf("coverage run event total differs")
		}
	}
	return nil
}
