package exploration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/extractor"
	"github.com/tmc/snes/internal/recovery/queue"
	"github.com/tmc/snes/internal/recovery/workflow"
)

type site struct {
	Address uint32 `json:"address"`
	Context uint8  `json:"emxc"`
	Hits    uint64 `json:"hits"`
}
type branch struct {
	ControllerNewSites int    `json:"controller_new_sites"`
	Index              int    `json:"index"`
	Frames             int    `json:"frames"`
	Instructions       uint64 `json:"instructions"`
	TargetHits         uint64 `json:"target_hits"`
	NewSites           int    `json:"new_sites"`
	InputSHA256        string `json:"input_sha256"`
	SiteSHA256         string `json:"site_sha256"`
	StateSHA256        string `json:"state_sha256"`
	Sites              []site `json:"sites"`
}
type captureResult struct {
	Stage   string                       `json:"stage"`
	Reason  string                       `json:"reason,omitempty"`
	Receipt *extractor.ExtractionReceipt `json:"receipt,omitempty"`
	Queue   *queue.Report                `json:"queue,omitempty"`
}
type counter struct {
	sites    map[uint64]uint64
	n        uint64
	max      uint64
	maxSites int
	fault    error
}

func (o *counter) ObserveTransition(cpu.Transition) {}
func (o *counter) ObserveInstruction(in cpu.Observation) {
	if o.fault != nil {
		return
	}
	if in.Fault != nil || in.Overflow {
		o.fault = fmt.Errorf("incomplete cpu observation")
		return
	}
	o.n++
	if o.n > o.max {
		o.fault = fmt.Errorf("instruction budget exhausted")
		return
	}
	a := uint32(in.Entry.PB)<<16 | uint32(in.Entry.PC)
	var c uint8
	if in.Entry.E {
		c |= 8
	}
	if in.Entry.P&0x20 != 0 {
		c |= 4
	}
	if in.Entry.P&0x10 != 0 {
		c |= 2
	}
	if in.Entry.P&1 != 0 {
		c |= 1
	}
	key := uint64(a)<<4 | uint64(c)
	if _, ok := o.sites[key]; !ok && len(o.sites) >= o.maxSites {
		o.fault = fmt.Errorf("site budget exhausted")
		return
	}
	o.sites[key]++
}
func measure(ctx context.Context, s *snes.System, c Config, inputs []uint16, index int, baseline map[uint64]uint64, entry uint32) (branch, error) {
	o := &counter{sites: map[uint64]uint64{}, max: c.MaxInstructions, maxSites: c.MaxSites}
	detach, e := s.CPU.Observe(o)
	if e != nil {
		return branch{}, e
	}
	defer detach()
	for _, in := range inputs {
		if e = ctx.Err(); e != nil {
			return branch{}, e
		}
		if e = s.SetInputState(0, in); e != nil {
			return branch{}, e
		}
		if e = s.RunFrame(); e != nil {
			return branch{}, e
		}
		if o.fault != nil {
			return branch{}, o.fault
		}
	}
	r := branch{Index: index, Frames: len(inputs), Instructions: o.n}
	ib, _ := json.Marshal(inputs)
	r.InputSHA256 = digest(ib)
	for k, h := range o.sites {
		a := uint32(k >> 4)
		r.Sites = append(r.Sites, site{a, uint8(k & 15), h})
		if a == entry {
			r.TargetHits += h
		}
		if _, ok := baseline[k]; !ok {
			r.NewSites++
		}
	}
	sort.Slice(r.Sites, func(i, j int) bool {
		a, b := r.Sites[i], r.Sites[j]
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		return a.Context < b.Context
	})
	b, _ := json.Marshal(r.Sites)
	r.SiteSHA256 = digest(b)
	b, e = s.Serialize()
	if e != nil {
		return branch{}, e
	}
	r.StateSHA256 = digest(b)
	return r, nil
}
func run(ctx context.Context, dir string, c Config, r *Report) error {
	r.Stage = "baseline"
	rom, e := read(c.ROM, 64<<20)
	if e != nil {
		return e
	}
	// Keep runtime consumers on one owned snapshot outside published artifacts.
	private, e := os.MkdirTemp(filepath.Dir(dir), ".exploration-rom-")
	if e != nil {
		return fmt.Errorf("create private ROM directory: %w", e)
	}
	defer os.RemoveAll(private)
	path := filepath.Join(private, "rom.sfc")
	if e = os.WriteFile(path, rom, 0600); e != nil {
		return fmt.Errorf("write private ROM: %w", e)
	}
	r.OperatorROM = c.ROM
	c.ROM.Path = path
	if _, e = read(c.ROM, 64<<20); e != nil {
		return fmt.Errorf("verify private ROM: %w", e)
	}
	r.RuntimeROMSHA256 = c.ROM.SHA256
	s := snes.NewSystem(nil)
	if e = s.LoadROM(rom); e != nil {
		return e
	}
	s.Power()
	var entry uint32
	for _, t := range r.Targets {
		if t.ID == c.Target {
			entry = t.Entry
		}
	}
	b, e := measure(ctx, s, c, make([]uint16, c.BaselineFrames), -1, nil, entry)
	if e != nil {
		return e
	}
	r.Baseline = &b
	checkpoint, e := s.Serialize()
	if e != nil {
		return e
	}
	if len(checkpoint) > 64<<20 {
		return fmt.Errorf("checkpoint exceeds budget")
	}
	if e = os.WriteFile(filepath.Join(dir, "checkpoint.state"), checkpoint, 0600); e != nil {
		return e
	}
	r.Capture = recapture(ctx, dir, c)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.Stage = "search"
	baseline := map[uint64]uint64{}
	for _, site := range b.Sites {
		baseline[uint64(site.Address)<<4|uint64(site.Context)] = site.Hits
	}
	for i, inputs := range c.Schedules {
		if e = s.Unserialize(checkpoint); e != nil {
			return e
		}
		result, e := measure(ctx, s, c, inputs, i, baseline, entry)
		if e != nil {
			return e
		}
		if i > 0 {
			neutral := map[uint64]bool{}
			for _, site := range r.Branches[0].Sites {
				neutral[uint64(site.Address)<<4|uint64(site.Context)] = true
			}
			for _, site := range result.Sites {
				key := uint64(site.Address)<<4 | uint64(site.Context)
				if _, ok := baseline[key]; !ok && !neutral[key] {
					result.ControllerNewSites++
				}
			}
		}
		r.Branches = append(r.Branches, result)
		if r.Winner < 0 || result.ControllerNewSites > r.Branches[r.Winner].ControllerNewSites {
			r.Winner = i
		}
	}
	r.Stage = "repeat"
	winner := r.Branches[r.Winner]
	r.Repeatable = true
	for range 2 {
		if e = s.Unserialize(checkpoint); e != nil {
			return e
		}
		result, e := measure(ctx, s, c, c.Schedules[r.Winner], r.Winner, baseline, entry)
		if e != nil {
			return e
		}
		r.Repeats = append(r.Repeats, result)
		if result.SiteSHA256 != winner.SiteSHA256 || result.StateSHA256 != winner.StateSHA256 || result.InputSHA256 != winner.InputSHA256 {
			r.Repeatable = false
		}
	}
	if !r.Repeatable {
		return fmt.Errorf("winner repeat identities differ")
	}
	r.Stage = "complete_discovery"
	return nil
}
func recapture(ctx context.Context, dir string, c Config) *captureResult {
	r := &captureResult{Stage: "capture"}
	root := filepath.Join(dir, "capture")
	if e := os.Mkdir(root, 0700); e != nil {
		r.Reason = e.Error()
		return r
	}
	tool, e := copyTool(dir, c.TraceTool)
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	for _, kind := range []string{"fixture", "capture", "history"} {
		events := "cpu_insn,cpu_transition"
		if kind == "capture" {
			events = "cpu_insn,cpu_transition,bus,mmio,dma,hdma"
		}
		if kind == "history" {
			events = "bus,mmio,dma,hdma"
		}
		args := []string{"run", "-rom", c.ROM.Path, "-frames", fmt.Sprint(c.BaselineFrames), "-events", events, "-out", filepath.Join(root, kind+".jsonl"), "-receipt", filepath.Join(root, kind+".receipt.json"), "-summary", filepath.Join(root, kind+".summary.json"), "-max-events", fmt.Sprint(c.MaxTraceEvents), "-max-bytes", fmt.Sprint(c.MaxTraceBytes)}
		if kind == "capture" || kind == "fixture" {
			args = append(args, "-pc", c.CapturePC)
		}
		if kind == "history" {
			args = append(args, "-op", "write")
		}
		if e := write(root, kind+".command.json", args); e != nil {
			r.Reason = e.Error()
			return r
		}
		log, e := os.Create(filepath.Join(root, kind+".log"))
		if e != nil {
			r.Reason = e.Error()
			return r
		}
		cmd := exec.CommandContext(ctx, tool, args...)
		cmd.Stdout = log
		cmd.Stderr = log
		e = cmd.Run()
		ce := log.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			r.Reason = fmt.Sprintf("%s producer: %v", kind, e)
			return r
		}
	}
	r.Stage = "extraction"
	d, e := read(c.Discovery, 32<<20)
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	cand, e := extractor.LoadCandidate(d, c.Target)
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	x, e := extractor.Extract(extractor.Config{Candidate: cand, ExpectedROMSHA256: c.ROM.SHA256, ROMPath: c.ROM.Path, FixturePath: filepath.Join(root, "fixture.jsonl"), FixtureReceiptPath: filepath.Join(root, "fixture.receipt.json"), FixtureSummaryPath: filepath.Join(root, "fixture.summary.json"), CapturePath: filepath.Join(root, "capture.jsonl"), CaptureReceiptPath: filepath.Join(root, "capture.receipt.json"), CaptureSummaryPath: filepath.Join(root, "capture.summary.json"), HistoryPath: filepath.Join(root, "history.jsonl"), HistoryReceiptPath: filepath.Join(root, "history.receipt.json"), HistorySummaryPath: filepath.Join(root, "history.summary.json"), StartBoundary: "power_on", CorpusLabel: "exploration", CorpusName: "exploration-" + c.Target, CasePrefix: "exploration_"})
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	// Store relative artifact paths so publication does not leave references to
	// the private staging directory. Hashes and captured values are unchanged.
	rebaseRoot(&x.TrustRoot, dir)
	for i := range x.Cases {
		ev := &x.Cases[i].Evidence
		ev.Fixture.Path = relative(dir, ev.Fixture.Path)
		rebaseFile(dir, ev.Fixture.Receipt)
		rebaseFile(dir, ev.Fixture.Summary)
		rebaseStream(dir, &ev.Capture)
		rebaseStream(dir, &ev.History)
	}
	r.Receipt = &x.Receipt
	if e = write(root, "extraction-receipt.json", x.Receipt); e != nil {
		r.Reason = e.Error()
		return r
	}
	if e = write(root, "proposed-trust-root.json", x.TrustRoot); e != nil {
		r.Reason = e.Error()
		return r
	}
	f, e := os.OpenFile(filepath.Join(root, "cases.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	enc := json.NewEncoder(f)
	for _, cas := range x.Cases {
		if e = enc.Encode(cas); e != nil {
			break
		}
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	if len(x.Cases) == 0 {
		r.Reason = "no complete extracted executions"
		return r
	}
	if c.Policy.Path == "" {
		r.Stage = "await_policy"
		r.Reason = "extracted proposed evidence requires explicit reviewed policy; no qualification executed"
		return r
	}
	r.Stage = "qualification"
	q, e := queue.Run(ctx, queue.Config{ProjectDir: c.ProjectDir, ROMPath: c.ROM.Path, CasesPath: filepath.Join(root, "cases.jsonl"), CorpusRoot: dir, OutDir: filepath.Join(root, "queue"), Revision: c.ProjectRevision, PolicyPath: c.Policy.Path, PolicySHA256: c.Policy.SHA256, Entry: cand.Entry, Limit: 1, MaxCases: 1, MaxSteps: 10000})
	if e != nil {
		r.Reason = e.Error()
		return r
	}
	r.Queue = q
	return r
}

func relative(dir, path string) string {
	r, e := filepath.Rel(dir, path)
	if e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return r
	}
	return path
}
func rebaseFile(dir string, p *extractor.FileRef) {
	if p != nil {
		p.Path = relative(dir, p.Path)
	}
}
func rebaseStream(dir string, p *extractor.StreamRef) {
	p.Path = relative(dir, p.Path)
	rebaseFile(dir, p.Receipt)
	rebaseFile(dir, p.Summary)
}
func rebaseRoot(p *extractor.ProposedTrustRoot, dir string) {
	p.Fixture.Path = relative(dir, p.Fixture.Path)
	rebaseFile(dir, p.Fixture.Receipt)
	rebaseFile(dir, p.Fixture.Summary)
	rebaseStream(dir, &p.Capture)
	rebaseStream(dir, &p.History)
}

// copyTool executes only bytes measured against the configured tool pin. The
// containing private staging directory is mode0700; mutating the source path
// after this copy cannot substitute a different executable at dispatch.
func copyTool(dir string, pin workflow.Input) (string, error) {
	b, e := read(pin, 128<<20)
	if e != nil {
		return "", e
	}
	path := filepath.Join(dir, "producer-tool")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if e != nil {
		return "", e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return "", e
	}
	if _, e = read(workflow.Input{Path: path, SHA256: pin.SHA256}, 128<<20); e != nil {
		return "", fmt.Errorf("verify private producer: %w", e)
	}
	return path, nil
}
