// Package replay runs reversible source-edit experiments on pinned routine cases.
// Edited results never inherit captured qualification from the original source.
package replay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

// Config pins every input and the explicit bounded region. OutDir must not exist.
// InputsPath must contain an empty JSON array; device/input scheduling is unsupported.
type Config struct {
	ProjectDir     string            `json:"project_dir"`
	ROMPath        string            `json:"rom_path"`
	CasePath       string            `json:"case_path"`
	SourcePath     string            `json:"source_path"`
	PatchPath      string            `json:"patch_path"`
	InputsPath     string            `json:"inputs_path"`
	CorpusRoot     string            `json:"corpus_root"`
	OutDir         string            `json:"out_dir"`
	Revision       string            `json:"revision"`
	ROMSHA256      string            `json:"rom_sha256"`
	CaseSHA256     string            `json:"case_sha256"`
	SourceSHA256   string            `json:"source_sha256"`
	PatchSHA256    string            `json:"patch_sha256"`
	InputsSHA256   string            `json:"inputs_sha256"`
	Start          uint32            `json:"start"`
	End            uint32            `json:"end"`
	MaxSteps       int               `json:"max_steps"`
	RefusalTargets map[uint32]string `json:"refusal_targets,omitempty"`
}

// Patch replaces exactly one literal occurrence in the pinned original source.
type Patch struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// StateChange identifies one compared architectural field. Values omit cycles.
type StateChange struct {
	Field    string `json:"field"`
	Original uint32 `json:"original"`
	Edited   uint32 `json:"edited"`
}

// WriteChange records one changed position in the ordered write journal.
type WriteChange struct {
	Index    int                 `json:"index"`
	Original *decomp.MemoryWrite `json:"original"`
	Edited   *decomp.MemoryWrite `json:"edited"`
}

// Result preserves original qualification separately from intentional edit effects.
type Result struct {
	ConsumerExecutableSHA256    string                 `json:"consumer_executable_sha256"`
	GoVersion                   string                 `json:"go_version"`
	Schema                      string                 `json:"schema"`
	Config                      Config                 `json:"config"`
	Admission                   decomp.AdmissionRecord `json:"admission"`
	Baseline                    decomp.ReplayReceipt   `json:"baseline"`
	Edited                      decomp.ExecResult      `json:"edited"`
	EditedCapturedProofEligible bool                   `json:"edited_captured_proof_eligible"`
	EditedStatus                string                 `json:"edited_status"`
	CPUChanges                  []StateChange          `json:"cpu_changes,omitempty"`
	WriteChanges                []WriteChange          `json:"write_changes,omitempty"`
	Difference                  string                 `json:"difference,omitempty"`
	EditedSHA256                string                 `json:"edited_sha256"`
	EditedRunnerSHA256          string                 `json:"edited_runner_sha256"`
	Compiler                    string                 `json:"compiler"`
	CompilerFlags               string                 `json:"compiler_flags"`
	Limitations                 []string               `json:"limitations"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func readPinned(path, expected string) ([]byte, error) {
	if path == "" || len(expected) != 64 {
		return nil, fmt.Errorf("missing path or SHA-256 identity")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return nil, fmt.Errorf("invalid SHA-256 identity")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 32<<20 {
		return nil, fmt.Errorf("input exceeds 32 MiB")
	}
	if digest(b) != expected {
		return nil, fmt.Errorf("input digest mismatch: %s", path)
	}
	return b, nil
}
func decodeStrict(b []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
func applyPatch(original []byte, p Patch) ([]byte, error) {
	if p.Find == "" || p.Find == p.Replace {
		return nil, fmt.Errorf("patch must make an explicit change")
	}
	if bytes.Count(original, []byte(p.Find)) != 1 {
		return nil, fmt.Errorf("patch must match exactly once")
	}
	return bytes.Replace(original, []byte(p.Find), []byte(p.Replace), 1), nil
}
func entryContext(s decomp.CPUState) recovery.Context {
	bit := func(set bool) string {
		if set {
			return "set"
		}
		return "clear"
	}
	return recovery.Context{E: bit(s.E), M: bit(s.P&0x20 != 0), X: bit(s.P&0x10 != 0), C: "unknown"}
}
func compare(original, edited decomp.ExecResult) (string, string) {
	if edited.MissingRead || edited.MMIOAccess || edited.WriteOverflow {
		return "refused", "edited execution reported unsupported access or bounds"
	}
	a, b := original.State, edited.State
	a.Cycles = 0
	b.Cycles = 0
	if a != b || original.NextPC != edited.NextPC {
		return "diverged", "exit CPU state or next PC changed"
	}
	if original.TotalWrites != edited.TotalWrites || !slices.Equal(original.Writes, edited.Writes) {
		return "diverged", "ordered memory writes changed"
	}
	return "same_sample", "no compared effect changed in this one sample"
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

// Run qualifies the original source and compares an edited copy from the same
// pinned initial state. Only pure captured CPU/RAM cases are supported.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	cfg.RefusalTargets = maps.Clone(cfg.RefusalTargets)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.ProjectDir == "" || cfg.CorpusRoot == "" || cfg.OutDir == "" || cfg.Revision == "" {
		return nil, fmt.Errorf("missing project, corpus, output, or revision")
	}
	if cfg.MaxSteps < 1 || cfg.MaxSteps > 1000000 || cfg.Start >= 1<<24 || cfg.End > 1<<24 || cfg.Start >= cfg.End || cfg.End-cfg.Start > 32768 || cfg.Start&0xffff < 0x8000 || cfg.Start&0xff0000 != (cfg.End-1)&0xff0000 {
		return nil, fmt.Errorf("unsupported region or instruction budget")
	}
	if _, err := os.Lstat(cfg.OutDir); err == nil {
		return nil, fmt.Errorf("output already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	pins := []struct{ path, sha string }{{cfg.ROMPath, cfg.ROMSHA256}, {cfg.CasePath, cfg.CaseSHA256}, {cfg.SourcePath, cfg.SourceSHA256}, {cfg.PatchPath, cfg.PatchSHA256}, {cfg.InputsPath, cfg.InputsSHA256}}
	data := make([][]byte, len(pins))
	for i, p := range pins {
		b, err := readPinned(p.path, p.sha)
		if err != nil {
			return nil, err
		}
		data[i] = b
	}
	var schedule []json.RawMessage
	if err := decodeStrict(data[4], &schedule); err != nil {
		return nil, fmt.Errorf("input schedule: %w", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data[4]), []byte("[")) || len(schedule) != 0 {
		return nil, fmt.Errorf("external input schedule unsupported; require explicit []")
	}
	var patch Patch
	if err := decodeStrict(data[3], &patch); err != nil {
		return nil, err
	}
	edited, err := applyPatch(data[2], patch)
	if err != nil {
		return nil, err
	}
	var c decomp.ReplayCase
	if err := decodeStrict(data[1], &c); err != nil {
		return nil, fmt.Errorf("case: %w", err)
	}
	if c.ROMSHA256 != cfg.ROMSHA256 || uint32(c.InitialState.PB)<<16|uint32(c.InitialState.PC) != cfg.Start {
		return nil, fmt.Errorf("case ROM or entry differs from pinned region")
	}
	admitted, err := decomp.NewEvidenceVerifier(cfg.CorpusRoot).Admit(&c, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("admit original case: %w", err)
	}
	docRaw, err := os.ReadFile(filepath.Join(cfg.ProjectDir, "recovery.json"))
	if err != nil {
		return nil, err
	}
	doc, err := recovery.Decode(bytes.NewReader(docRaw))
	if err != nil {
		return nil, err
	}
	if doc.ROM.NormalizedSHA256 != cfg.ROMSHA256 || doc.ROM.Mapper != "lorom" || recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != cfg.Revision {
		return nil, fmt.Errorf("project ROM, mapper, or content revision differs")
	}
	off := int((cfg.Start>>16&0x7f)*0x8000 + (cfg.Start & 0x7fff))
	length := int(cfg.End - cfg.Start)
	if off+length > len(data[0]) {
		return nil, fmt.Errorf("region exceeds ROM")
	}
	region, err := decomp.DecodeRegionWithConfig(decomp.DecodeRegionConfig{CodeBytes: data[0][off : off+length], EntryAddr: cfg.Start, EntryCtx: entryContext(c.InitialState), PinnedROM: data[0], ROMBaseAddr: cfg.Start, MaxSteps: cfg.MaxSteps, AllowInternalJSR: true, RefusalTargets: cfg.RefusalTargets})
	if err != nil {
		return nil, err
	}
	region.ROMBytes = nil
	generated, err := decomp.GenerateRegionC(region)
	if err != nil {
		return nil, err
	}
	if generated != string(data[2]) {
		return nil, fmt.Errorf("original source does not match bound region generation")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.OutDir), 0700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(cfg.OutDir), ".experiment-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	originalPath := filepath.Join(stage, "original.c")
	editedPath := filepath.Join(stage, "edited.c")
	if err := os.WriteFile(originalPath, data[2], 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(editedPath, edited, 0600); err != nil {
		return nil, err
	}
	runner, err := decomp.NewCompiledRegionRunnerWithROM(ctx, originalPath, "execute_"+region.Name, data[0])
	if err != nil {
		return nil, err
	}
	defer runner.Close()
	if err := runner.BindRegion(region, cfg.Revision); err != nil {
		return nil, err
	}
	baseline, err := decomp.ExecuteThreeWayRoutineReplay(ctx, runner, c)
	if err != nil {
		return nil, err
	}
	decomp.ValidateRoutineReplayReceiptFreshness(&baseline, &c, region, generated, cfg.ROMSHA256, cfg.Revision, runner)
	if !baseline.CapturedProofEligible || !baseline.Eligible || !baseline.Matched || !baseline.EffectsMatch || baseline.Metadata.IsStale {
		return nil, fmt.Errorf("original baseline not freshly qualified: %s", baseline.Discrepancy)
	}
	changed, err := decomp.NewCompiledRegionRunnerWithROM(ctx, editedPath, "execute_"+region.Name, data[0])
	if err != nil {
		return nil, fmt.Errorf("compile edited source: %w", err)
	}
	defer changed.Close()
	// RunBatch returns execution data only. Never BindRegion or issue captured proof for edited code.
	output, err := changed.RunBatch(ctx, []decomp.ReplayCase{c})
	if err != nil {
		return nil, err
	}
	if len(output) != 1 {
		return nil, fmt.Errorf("edited runner returned unexpected case count")
	}
	status, difference := compare(baseline.CompiledC, output[0])
	binary, err := os.ReadFile(changed.BinPath)
	if err != nil {
		return nil, err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executableBytes, err := os.ReadFile(executablePath)
	if err != nil {
		return nil, err
	}
	result := &Result{ConsumerExecutableSHA256: digest(executableBytes), GoVersion: runtime.Version(), Schema: "snes-source-edit-experiment-v1", Config: cfg, Admission: admitted, Baseline: baseline, Edited: output[0], EditedStatus: status, Difference: difference, EditedSHA256: digest(edited), EditedRunnerSHA256: digest(binary), Compiler: changed.Compiler, CompilerFlags: changed.CompilerFlags, Limitations: []string{"edited C is experimental and never captured-proof eligible", "same pinned initial CPU/memory and empty external input schedule", "one captured CPU/RAM sample; cycles and hardware scheduling not compared", "reference emulator shares Go CPU ancestry", "no rendered-frame or visible movement attribution; no live replacement"}}
	if status != "refused" {
		result.CPUChanges, result.WriteChanges = changes(baseline.CompiledC, output[0])
	}
	for _, item := range []struct {
		name string
		v    any
	}{{"case.json", c}, {"patch.json", patch}, {"inputs.json", schedule}, {"baseline.json", baseline}, {"result.json", result}} {
		if err := writeJSON(filepath.Join(stage, item.name), item.v); err != nil {
			return nil, err
		}
	}
	for _, p := range pins {
		if _, err := readPinned(p.path, p.sha); err != nil {
			return nil, fmt.Errorf("inputs changed: %w", err)
		}
	}
	if recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != cfg.Revision {
		return nil, fmt.Errorf("project changed during experiment")
	}
	for _, p := range []struct {
		path string
		sha  string
	}{{originalPath, cfg.SourceSHA256}, {editedPath, result.EditedSHA256}} {
		if _, err := readPinned(p.path, p.sha); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Mkdir(cfg.OutDir, 0700); err != nil {
		return nil, fmt.Errorf("reserve output: %w", err)
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(cfg.OutDir)
		}
	}()
	if err := os.Rename(stage, filepath.Join(cfg.OutDir, "artifacts")); err != nil {
		return nil, err
	}
	if err := os.Rename(filepath.Join(cfg.OutDir, "artifacts", "result.json"), filepath.Join(cfg.OutDir, "result.json")); err != nil {
		return nil, err
	}
	published = true
	return result, nil
}

// LoadConfig reads a strictly decoded experiment configuration.
func LoadConfig(r io.Reader) (Config, error) {
	var cfg Config
	b, err := io.ReadAll(io.LimitReader(r, 1<<20+1))
	if err != nil {
		return cfg, err
	}
	if len(b) > 1<<20 {
		return cfg, fmt.Errorf("configuration exceeds 1 MiB")
	}
	if err := decodeStrict(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func changes(a, b decomp.ExecResult) ([]StateChange, []WriteChange) {
	bit := func(v bool) uint32 {
		if v {
			return 1
		}
		return 0
	}
	fields := []struct {
		name          string
		before, after uint32
	}{
		{"a", uint32(a.State.A), uint32(b.State.A)}, {"x", uint32(a.State.X), uint32(b.State.X)},
		{"y", uint32(a.State.Y), uint32(b.State.Y)}, {"s", uint32(a.State.S), uint32(b.State.S)},
		{"d", uint32(a.State.D), uint32(b.State.D)}, {"db", uint32(a.State.DB), uint32(b.State.DB)},
		{"pb", uint32(a.State.PB), uint32(b.State.PB)}, {"pc", uint32(a.State.PC), uint32(b.State.PC)},
		{"p", uint32(a.State.P), uint32(b.State.P)}, {"e", bit(a.State.E), bit(b.State.E)},
		{"next_pc", a.NextPC, b.NextPC},
	}
	var cpu []StateChange
	var writes []WriteChange
	for _, f := range fields {
		if f.before != f.after {
			cpu = append(cpu, StateChange{f.name, f.before, f.after})
		}
	}
	for i := 0; i < max(len(a.Writes), len(b.Writes)); i++ {
		var before, after *decomp.MemoryWrite
		if i < len(a.Writes) {
			v := a.Writes[i]
			before = &v
		}
		if i < len(b.Writes) {
			v := b.Writes[i]
			after = &v
		}
		if before != nil && after != nil && *before == *after {
			continue
		}
		writes = append(writes, WriteChange{i, before, after})
	}
	return cpu, writes
}
