package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

// ConnectedProfile selects code for one bounded replay. Admission remains in
// the separately pinned, singular-handler policy.
type ConnectedProfile struct {
	ID              string              `json:"id"`
	ROMSHA256       string              `json:"rom_sha256"`
	Entry           uint32              `json:"entry"`
	Context         recovery.Context    `json:"context"`
	Spans           []decomp.CodeSpan   `json:"spans"`
	IndirectTargets map[uint32][]uint32 `json:"indirect_targets"`
	RefusalTargets  map[uint32]string   `json:"refusal_targets"`
	MaxInstructions int                 `json:"max_instructions"`
	MaxSteps        int                 `json:"max_steps"`
}

// ConnectedReport records one policy's case census and one generated region.
type ConnectedReport struct {
	Schema             string   `json:"schema"`
	Revision           string   `json:"revision"`
	ROMSHA256          string   `json:"rom_sha256"`
	ProfileSHA256      string   `json:"profile_sha256"`
	PolicyFileSHA256   string   `json:"policy_file_sha256"`
	PolicySHA256       string   `json:"policy_sha256"`
	CasesSHA256        string   `json:"cases_sha256"`
	Entry              uint32   `json:"entry"`
	RoutineID          string   `json:"routine_id"`
	HandlerEntry       uint32   `json:"handler_entry"`
	Status             string   `json:"status"`
	Reason             string   `json:"reason,omitempty"`
	Cases              int      `json:"cases"`
	Admitted           int      `json:"admitted"`
	Matched            int      `json:"matched"`
	Refused            int      `json:"refused"`
	Mismatched         int      `json:"mismatched"`
	Unexecuted         int      `json:"unexecuted"`
	SourceSHA256       string   `json:"source_sha256,omitempty"`
	RegionSHA256       string   `json:"region_sha256,omitempty"`
	RunnerSourceSHA256 string   `json:"runner_source_sha256,omitempty"`
	RunnerHash         string   `json:"runner_hash,omitempty"`
	Compiler           string   `json:"compiler,omitempty"`
	CompilerFlags      string   `json:"compiler_flags,omitempty"`
	Limitations        []string `json:"limitations"`
}

func decodeStrict(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func validConnectedProfile(p ConnectedProfile, romSHA string) error {
	if p.ID == "" || strings.ContainsAny(p.ID, "/\\.") || p.ROMSHA256 != romSHA || p.Entry >= 1<<24 || len(p.Spans) == 0 || len(p.Spans) > 64 || p.MaxInstructions < 1 || p.MaxInstructions > 1<<16 || p.MaxSteps < 1 || p.MaxSteps > 1<<20 {
		return fmt.Errorf("queue: invalid connected profile or ROM binding")
	}
	if p.Context.E != "clear" || (p.Context.M != "set" && p.Context.M != "clear") || (p.Context.X != "set" && p.Context.X != "clear") || (p.Context.C != "unknown" && p.Context.C != "set" && p.Context.C != "clear") {
		return fmt.Errorf("queue: unresolved connected entry context")
	}
	entryInSpan := false
	for _, s := range p.Spans {
		if s.Start <= p.Entry && p.Entry < s.End {
			entryInSpan = true
		}
	}
	if !entryInSpan || len(p.IndirectTargets) == 0 {
		return fmt.Errorf("queue: connected entry or indirect targets missing")
	}
	targetCount := 0
	for site, targets := range p.IndirectTargets {
		if !insideSpan(p.Spans, site, site+1) || len(targets) == 0 {
			return fmt.Errorf("queue: empty indirect target set at $%06X", site)
		}
		for _, target := range targets {
			targetCount++
			if p.RefusalTargets[target] != "" || !insideSpan(p.Spans, target, target+1) {
				return fmt.Errorf("queue: allowed target is refused or outside spans")
			}
		}
	}
	if targetCount > 2 {
		return fmt.Errorf("queue: connected profile permits more than two targets")
	}
	return nil
}

func connectedPolicy(p ConnectedProfile, policy decomp.AdmissionPolicy) (decomp.RoutineContract, error) {
	if len(policy.Routines) != 1 {
		return decomp.RoutineContract{}, fmt.Errorf("queue: connected replay requires one singular routine policy")
	}
	r := policy.Routines[0]
	if r.ID != p.ID || r.Entry != p.Entry || r.ROMSHA256 != p.ROMSHA256 || r.Connected == nil || r.Kind != "connected_routine" {
		return r, fmt.Errorf("queue: connected policy identity mismatch")
	}
	c := r.Connected
	if c.HandlerEntryPC == 0 || len(c.AllowedIndirectTargets) != 1 {
		return r, fmt.Errorf("queue: connected policy must select one handler")
	}
	for site, targets := range c.AllowedIndirectTargets {
		allowed := p.IndirectTargets[site]
		if len(targets) != 1 || targets[0] != c.HandlerEntryPC || !containsAddress(allowed, targets[0]) {
			return r, fmt.Errorf("queue: connected policy target differs from profile")
		}
	}
	for _, s := range c.Spans {
		if !insideSpan(p.Spans, s.Start, s.End) {
			return r, fmt.Errorf("queue: connected policy span outside profile")
		}
	}
	return r, nil
}

func containsAddress(addrs []uint32, target uint32) bool {
	for _, a := range addrs {
		if a == target {
			return true
		}
	}
	return false
}

func insideSpan(spans []decomp.CodeSpan, start, end uint32) bool {
	for _, s := range spans {
		if s.Start <= start && end <= s.End && start < end {
			return true
		}
	}
	return false
}

func connectedContextMatches(got decomp.CPUState, want recovery.Context) bool {
	actual := entryContext(got)
	if actual.E != want.E || actual.M != want.M || actual.X != want.X {
		return false
	}
	if want.C == "unknown" {
		return true
	}
	return (got.P&1 != 0) == (want.C == "set")
}

// RunConnected replays one singular-handler policy against a connected region.
// OutDir must not exist; report.json is the publication marker.
func RunConnected(ctx context.Context, cfg Config) (*ConnectedReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.ProjectDir == "" || cfg.ROMPath == "" || cfg.CasesPath == "" || cfg.CorpusRoot == "" || cfg.OutDir == "" || cfg.Revision == "" || cfg.ConnectedProfilePath == "" || cfg.PolicyPath == "" || cfg.PolicySHA256 == "" {
		return nil, fmt.Errorf("queue: incomplete connected replay inputs")
	}
	if cfg.MaxCases < 1 || cfg.MaxCases > 10000 || cfg.NamedSymbolsPath != "" || cfg.CandidatePath != "" || cfg.Entry != 0 {
		return nil, fmt.Errorf("queue: invalid connected replay options")
	}
	if _, err := os.Lstat(cfg.OutDir); err == nil {
		return nil, fmt.Errorf("queue: output already exists")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var pins []pinnedInput
	docBytes, err := readInput(filepath.Join(cfg.ProjectDir, "recovery.json"), &pins)
	if err != nil {
		return nil, err
	}
	doc, err := recovery.Decode(bytes.NewReader(docBytes))
	if err != nil {
		return nil, err
	}
	if recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != cfg.Revision {
		return nil, fmt.Errorf("queue: project revision mismatch")
	}
	romBytes, err := readInput(cfg.ROMPath, &pins)
	if err != nil {
		return nil, err
	}
	rom, err := recovery.AdmitROM(bytes.NewReader(romBytes), recovery.AdmissionOptions{})
	if err != nil {
		return nil, err
	}
	if doc.ROM.Mapper != "lorom" || rom.Identity.NormalizedSHA256 != doc.ROM.NormalizedSHA256 {
		return nil, fmt.Errorf("queue: ROM identity or mapper differs from recovery document")
	}
	profileBytes, err := readInput(cfg.ConnectedProfilePath, &pins)
	if err != nil {
		return nil, err
	}
	if len(profileBytes) > 1<<20 {
		return nil, fmt.Errorf("queue: connected profile exceeds 1 MiB")
	}
	var profile ConnectedProfile
	if err := decodeStrict(profileBytes, &profile); err != nil {
		return nil, fmt.Errorf("queue: connected profile: %w", err)
	}
	if err := validConnectedProfile(profile, rom.Identity.NormalizedSHA256); err != nil {
		return nil, err
	}
	policyBytes, err := readInput(cfg.PolicyPath, &pins)
	if err != nil {
		return nil, err
	}
	if len(policyBytes) > 1<<20 || hash(policyBytes) != cfg.PolicySHA256 {
		return nil, fmt.Errorf("queue: policy SHA-256 mismatch or oversize")
	}
	var policy decomp.AdmissionPolicy
	if err := decodeStrict(policyBytes, &policy); err != nil {
		return nil, fmt.Errorf("queue: policy: %w", err)
	}
	contract, err := connectedPolicy(profile, policy)
	if err != nil {
		return nil, err
	}
	v, err := decomp.NewEvidenceVerifierWithPolicy(cfg.CorpusRoot, policy, rom.NormalizedROM)
	if err != nil {
		return nil, fmt.Errorf("queue: policy: %w", err)
	}
	caseBytes, err := readInput(cfg.CasesPath, &pins)
	if err != nil {
		return nil, err
	}
	cases, err := readCases(caseBytes)
	if err != nil {
		return nil, err
	}
	r := &ConnectedReport{Schema: "snes-connected-queue-v1", Revision: cfg.Revision, ROMSHA256: rom.Identity.NormalizedSHA256, ProfileSHA256: hash(profileBytes), PolicyFileSHA256: hash(policyBytes), PolicySHA256: v.PolicySHA256(), CasesSHA256: hash(caseBytes), Entry: profile.Entry, RoutineID: profile.ID, HandlerEntry: contract.Connected.HandlerEntryPC, Status: "blocked", Limitations: []string{"one singular-handler admission policy per invocation", "qualified scope is captured CPU and ordered writes, not timing or whole-game equivalence", "maxcases may omit later occurrences; qualification applies only to persisted receipts"}}
	if err := os.MkdirAll(filepath.Dir(cfg.OutDir), 0755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(cfg.OutDir), ".connected-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "admission-policy.json"), policyBytes, 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, "connected-profile.json"), profileBytes, 0600); err != nil {
		return nil, err
	}
	var selected []decomp.ReplayCase
	var admissions []decomp.AdmissionRecord
	for _, original := range cases {
		if uint32(original.InitialState.PB)<<16|uint32(original.InitialState.PC) != profile.Entry {
			continue
		}
		if r.Cases == cfg.MaxCases {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.Cases++
		c := original
		if c.RoutineID != profile.ID || c.ROMSHA256 != r.ROMSHA256 || !connectedContextMatches(c.InitialState, profile.Context) {
			r.Refused++
			admissions = append(admissions, decomp.AdmissionRecord{CaseID: c.CaseID, RoutineID: c.RoutineID, Reason: "connected profile identity or entry context mismatch"})
			continue
		}
		a, err := v.Admit(&c, nil, nil)
		admissions = append(admissions, a)
		if err != nil {
			r.Refused++
			continue
		}
		selected = append(selected, c)
		r.Admitted++
		r.Unexecuted++
	}
	if err := writeJSON(filepath.Join(stage, "admissions.json"), admissions); err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		r.Reason = "no admitted cases"
		if r.Cases > 0 {
			r.Reason = "all selected cases refused"
		}
	} else {
		if err := v.PrefetchFixtures(selected); err != nil {
			return nil, fmt.Errorf("queue: connected fixture prefetch: %w", err)
		}
		region, err := decomp.DecodeConnected(decomp.ConnectedConfig{ROM: rom.NormalizedROM, Spans: profile.Spans, Entry: profile.Entry, Context: profile.Context, MaxInstructions: profile.MaxInstructions, MaxSteps: profile.MaxSteps, IndirectTargets: profile.IndirectTargets, RefusalTargets: profile.RefusalTargets})
		if err != nil {
			return nil, fmt.Errorf("queue: decode connected: %w", err)
		}
		region.ROMBytes = nil
		code, err := decomp.GenerateRegionC(region)
		if err != nil {
			return nil, fmt.Errorf("queue: generate connected: %w", err)
		}
		r.SourceSHA256 = hash([]byte(code))
		r.RunnerSourceSHA256 = hash([]byte(decomp.GenerateRegionMultiCaseRunnerC(code, "execute_"+region.Name)))
		if err := writeJSON(filepath.Join(stage, "region.json"), region); err != nil {
			return nil, err
		}
		regionBytes, err := os.ReadFile(filepath.Join(stage, "region.json"))
		if err != nil {
			return nil, err
		}
		r.RegionSHA256 = hash(regionBytes)
		sourcePath := filepath.Join(stage, "generated.c")
		if err := os.WriteFile(sourcePath, []byte(code), 0600); err != nil {
			return nil, err
		}
		runner, err := decomp.NewCompiledRegionRunnerWithROM(ctx, sourcePath, "execute_"+region.Name, rom.NormalizedROM)
		if err != nil {
			return nil, fmt.Errorf("queue: compile connected: %w", err)
		}
		defer runner.Close()
		if err := runner.BindRegion(region, cfg.Revision); err != nil {
			return nil, fmt.Errorf("queue: bind connected: %w", err)
		}
		var receipts []decomp.ReplayReceipt
		for _, c := range selected {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			receipt, err := v.ExecuteThreeWayRoutineReplay(ctx, runner, c)
			if err != nil {
				return nil, err
			}
			v.ValidateRoutineReplayReceiptFreshness(&receipt, &c, region, code, hash(rom.NormalizedROM), cfg.Revision, runner)
			if r.RunnerHash == "" {
				r.RunnerHash = receipt.Metadata.RunnerHash
				r.Compiler = receipt.Metadata.Compiler
				r.CompilerFlags = receipt.Metadata.CompilerFlags
			}
			receipts = append(receipts, receipt)
			r.Unexecuted--
			if receipt.CapturedProofEligible && receipt.Eligible && receipt.Matched && receipt.EffectsMatch && !receipt.Metadata.IsStale {
				r.Matched++
			} else if strings.Contains(strings.ToLower(receipt.Discrepancy), "mismatch") {
				r.Mismatched++
			} else {
				r.Refused++
			}
		}
		if err := writeJSON(filepath.Join(stage, "cases.json"), selected); err != nil {
			return nil, err
		}
		if err := writeJSON(filepath.Join(stage, "receipts.json"), receipts); err != nil {
			return nil, err
		}
		if r.Mismatched > 0 {
			r.Status = "mismatch"
		} else if r.Matched == r.Admitted && r.Admitted > 0 {
			r.Status = "qualified"
		} else {
			r.Status = "blocked"
		}
	}
	if err := checkInputs(pins); err != nil {
		return nil, err
	}
	if recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != cfg.Revision {
		return nil, fmt.Errorf("queue: project changed during execution")
	}
	if err := checkConnectedArtifacts(stage, r); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(stage, "report.json"), r); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(cfg.OutDir); err == nil {
		return nil, fmt.Errorf("queue: output appeared during execution")
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Rename(stage, cfg.OutDir); err != nil {
		return nil, fmt.Errorf("queue: publish connected: %w", err)
	}
	return r, nil
}

func checkConnectedArtifacts(stage string, r *ConnectedReport) error {
	for _, item := range []struct{ name, digest string }{{"admission-policy.json", r.PolicyFileSHA256}, {"connected-profile.json", r.ProfileSHA256}, {"generated.c", r.SourceSHA256}, {"region.json", r.RegionSHA256}} {
		if item.digest == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(stage, item.name))
		if err != nil || hash(b) != item.digest {
			return fmt.Errorf("queue: connected artifact changed: %s", item.name)
		}
	}
	return nil
}
