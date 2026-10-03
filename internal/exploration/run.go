package exploration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/tmc/snes/internal/trace"
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
	if r.Winner >= 0 {
		r.Stage = "capture_winner"
		r.WinnerCapture = captureWinner(ctx, dir, c, r, s, checkpoint, baseline, entry)
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
	if _, e := read(workflow.Input{Path: path, SHA256: pin.SHA256}, 128<<20); e == nil {
		return path, nil
	}
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

func captureWinner(ctx context.Context, dir string, c Config, r *Report, s *snes.System, checkpoint []byte, baseline map[uint64]uint64, entry uint32) *WinnerCapture {
	wc := &WinnerCapture{
		Status:          "unqualified",
		InitialStateSHA: digest(checkpoint),
	}
	if r.Winner < 0 || r.Winner >= len(c.Schedules) {
		wc.Reason = "invalid winner index"
		return wc
	}
	winner := r.Branches[r.Winner]
	windowFrames := len(c.Schedules[r.Winner])
	if winner.Frames > 0 {
		insnPerFrame := winner.Instructions / uint64(winner.Frames)
		if insnPerFrame == 0 {
			insnPerFrame = 1000
		}
		estEventsPerFrame := insnPerFrame * 3
		estBytesPerFrame := insnPerFrame * 1500
		if c.MaxTraceEvents > 0 && uint64(windowFrames)*estEventsPerFrame > uint64(c.MaxTraceEvents) {
			windowFrames = int(uint64(c.MaxTraceEvents) / estEventsPerFrame)
		}
		if c.MaxTraceBytes > 0 && int64(windowFrames)*int64(estBytesPerFrame) > c.MaxTraceBytes {
			maxF := int(c.MaxTraceBytes / int64(estBytesPerFrame))
			if maxF < windowFrames {
				windowFrames = maxF
			}
		}
	}
	if windowFrames < 1 {
		windowFrames = 1
	}
	if windowFrames > len(c.Schedules[r.Winner]) {
		windowFrames = len(c.Schedules[r.Winner])
	}
	wc.Frames = windowFrames

	schedule := c.Schedules[r.Winner][:windowFrames]
	if e := s.Unserialize(checkpoint); e != nil {
		wc.Reason = fmt.Sprintf("prefix restore: %v", e)
		return wc
	}
	prefixResult, e := measure(ctx, s, c, schedule, r.Winner, baseline, entry)
	if e != nil {
		wc.Reason = fmt.Sprintf("prefix measure: %v", e)
		return wc
	}
	wc.PrefixExpectedState = prefixResult.StateSHA256
	wc.PrefixExpectedSite = prefixResult.SiteSHA256

	ib, e := json.Marshal(schedule)
	if e != nil {
		wc.Reason = e.Error()
		return wc
	}
	wc.InputSHA256 = digest(ib)
	inputsPath := filepath.Join(dir, "winner-inputs.json")
	if e = os.WriteFile(inputsPath, ib, 0600); e != nil {
		wc.Reason = e.Error()
		return wc
	}

	tool, e := copyTool(dir, c.TraceTool)
	if e != nil {
		wc.Reason = e.Error()
		return wc
	}

	tracePath := filepath.Join(dir, "trace.jsonl")
	receiptPath := filepath.Join(dir, "trace.receipt.json")
	summaryPath := filepath.Join(dir, "summary.json")
	framesDir := filepath.Join(dir, "frames")
	chkPath := filepath.Join(dir, "checkpoint.state")

	args := []string{
		"run",
		"-rom", c.ROM.Path,
		"-state", chkPath,
		"-inputs", inputsPath,
		"-cadence", "frame",
		"-frames", fmt.Sprint(windowFrames),
		"-events", "cpu_insn,cpu_transition,bus,mmio,dma,ppu",
		"-frame-dir", framesDir,
		"-frame-png", "all",
		"-out", tracePath,
		"-receipt", receiptPath,
		"-summary", summaryPath,
		"-max-events", fmt.Sprint(c.MaxTraceEvents),
		"-max-bytes", fmt.Sprint(c.MaxTraceBytes),
	}
	if e := write(dir, "winner-capture.command.json", args); e != nil {
		wc.Reason = e.Error()
		return wc
	}

	log, e := os.Create(filepath.Join(dir, "winner-capture.log"))
	if e != nil {
		wc.Reason = e.Error()
		return wc
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmdErr := cmd.Run()
	ce := log.Close()
	if cmdErr == nil {
		cmdErr = ce
	}
	if cmdErr != nil {
		wc.Reason = fmt.Sprintf("winner producer: %v", cmdErr)
		return wc
	}

	if err := validateWinnerArtifacts(dir, c.MaxTraceEvents, c.MaxTraceBytes, c.ROM.SHA256, wc.InitialStateSHA, wc.PrefixExpectedState, wc.InputSHA256, wc.PrefixExpectedSite, windowFrames, wc); err != nil {
		wc.Reason = err.Error()
		return wc
	}

	wc.Status = "complete"
	wc.Reason = ""
	return wc
}

func validateWinnerArtifacts(dir string, maxTraceEvents int, maxTraceBytes int64, expectedROM, expectedInitialState, expectedFinalState, expectedInput, expectedSite string, windowFrames int, wc *WinnerCapture) error {
	receiptPath := filepath.Join(dir, "trace.receipt.json")
	tracePath := filepath.Join(dir, "trace.jsonl")
	summaryPath := filepath.Join(dir, "summary.json")
	framesDir := filepath.Join(dir, "frames")

	tb, err := os.ReadFile(receiptPath)
	if err != nil {
		return errors.New("trace receipt missing")
	}
	var tr struct {
		Schema       int    `json:"schema"`
		Outcome      string `json:"outcome"`
		StreamSHA256 string `json:"stream_sha256"`
		EventCount   int    `json:"event_count"`
	}
	if err := json.Unmarshal(tb, &tr); err != nil {
		return fmt.Errorf("parse trace receipt: %w", err)
	}
	if tr.Schema != 2 {
		return fmt.Errorf("unsupported trace receipt schema %d", tr.Schema)
	}
	if tr.Outcome != "complete" {
		return fmt.Errorf("trace outcome %q", tr.Outcome)
	}
	if tr.EventCount <= 0 || (maxTraceEvents > 0 && tr.EventCount > maxTraceEvents) {
		return fmt.Errorf("invalid trace event count %d", tr.EventCount)
	}
	wc.TraceEvents = tr.EventCount

	fi, err := os.Stat(tracePath)
	if err != nil {
		return errors.New("trace file missing")
	}
	wc.TraceBytes = fi.Size()
	if maxTraceBytes > 0 && wc.TraceBytes > maxTraceBytes {
		return fmt.Errorf("trace bytes %d exceeds limit %d", wc.TraceBytes, maxTraceBytes)
	}

	tf, err := os.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace file: %w", err)
	}
	defer tf.Close()

	hasher := sha256.New()
	trReader := bufio.NewReaderSize(io.TeeReader(tf, hasher), 64*1024)

	headerLine, err := trReader.ReadBytes('\n')
	if err != nil && len(headerLine) == 0 {
		return fmt.Errorf("read trace header: %w", err)
	}
	var headerRec struct {
		Kind string         `json:"kind"`
		Run  *trace.RunInfo `json:"run"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(headerLine), &headerRec); err != nil {
		return fmt.Errorf("parse trace header: %w", err)
	}
	if headerRec.Kind != "run" || headerRec.Run == nil {
		return errors.New("trace run header missing RunInfo")
	}
	if expectedROM != "" && headerRec.Run.ROMSHA256 != expectedROM {
		return fmt.Errorf("run identity mismatch: trace ROM %s, expected %s", headerRec.Run.ROMSHA256, expectedROM)
	}
	if expectedInitialState != "" && headerRec.Run.InitialStateSHA256 != "" && headerRec.Run.InitialStateSHA256 != expectedInitialState {
		return fmt.Errorf("run identity mismatch: trace initial state %s, expected %s", headerRec.Run.InitialStateSHA256, expectedInitialState)
	}
	if expectedInput != "" && headerRec.Run.ReplayInputSHA256 != "" && headerRec.Run.ReplayInputSHA256 != expectedInput {
		return fmt.Errorf("run identity mismatch: trace replay input %s, expected %s", headerRec.Run.ReplayInputSHA256, expectedInput)
	}

	actualEvents := 1
	siteMap := make(map[uint64]uint64)
	for {
		line, err := trReader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			actualEvents++
			if bytes.Contains(trimmed, []byte(`"kind":"cpu_insn"`)) {
				var insnRec struct {
					Insn *struct {
						Entry struct {
							PB uint8  `json:"pb"`
							PC uint16 `json:"pc"`
							P  uint8  `json:"p"`
							E  bool   `json:"e"`
						} `json:"entry"`
					} `json:"insn"`
				}
				if err := json.Unmarshal(trimmed, &insnRec); err == nil && insnRec.Insn != nil {
					entry := insnRec.Insn.Entry
					a := uint32(entry.PB)<<16 | uint32(entry.PC)
					var c uint8
					if entry.E {
						c |= 8
					}
					if entry.P&0x20 != 0 {
						c |= 4
					}
					if entry.P&0x10 != 0 {
						c |= 2
					}
					if entry.P&1 != 0 {
						c |= 1
					}
					key := uint64(a)<<4 | uint64(c)
					siteMap[key]++
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("read trace stream: %w", err)
		}
	}

	actualTraceSHA := hex.EncodeToString(hasher.Sum(nil))
	if actualTraceSHA != tr.StreamSHA256 {
		return fmt.Errorf("trace stream hash mismatch: computed %s, receipt has %s", actualTraceSHA, tr.StreamSHA256)
	}
	wc.TraceSHA256 = actualTraceSHA

	if actualEvents != tr.EventCount {
		return fmt.Errorf("trace event count mismatch: stream has %d, receipt has %d", actualEvents, tr.EventCount)
	}

	if expectedSite != "" {
		var sites []site
		for k, h := range siteMap {
			a := uint32(k >> 4)
			sites = append(sites, site{Address: a, Context: uint8(k & 15), Hits: h})
		}
		sort.Slice(sites, func(i, j int) bool {
			if sites[i].Address != sites[j].Address {
				return sites[i].Address < sites[j].Address
			}
			return sites[i].Context < sites[j].Context
		})
		sb, _ := json.Marshal(sites)
		reconstructedSiteSHA := digest(sb)
		if reconstructedSiteSHA != expectedSite {
			return fmt.Errorf("site census mismatch: reconstructed %s, measured prefix has %s", reconstructedSiteSHA, expectedSite)
		}
	}

	fb, err := os.ReadFile(filepath.Join(framesDir, "frames.receipt.json"))
	if err != nil {
		return errors.New("frame receipt missing")
	}
	var fr struct {
		Schema         int    `json:"schema"`
		Outcome        string `json:"outcome"`
		ManifestSHA256 string `json:"manifest_sha256"`
		Frames         int    `json:"frames"`
		Stored         int    `json:"stored"`
	}
	if err := json.Unmarshal(fb, &fr); err != nil {
		return fmt.Errorf("parse frame receipt: %w", err)
	}
	if fr.Schema != 1 {
		return fmt.Errorf("unsupported frame receipt schema %d", fr.Schema)
	}
	if fr.Outcome != "complete" {
		return fmt.Errorf("frame capture outcome %q", fr.Outcome)
	}
	if fr.Frames != windowFrames || fr.Stored != windowFrames {
		return fmt.Errorf("frame count mismatch: got frames=%d stored=%d, want %d", fr.Frames, fr.Stored, windowFrames)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(framesDir, "frames.jsonl"))
	if err != nil {
		return errors.New("frames manifest missing")
	}
	actualManifestSHA := digest(manifestBytes)
	if actualManifestSHA != fr.ManifestSHA256 {
		return fmt.Errorf("frame manifest hash mismatch: computed %s, receipt has %s", actualManifestSHA, fr.ManifestSHA256)
	}
	wc.ManifestSHA256 = actualManifestSHA

	mfScanner := bufio.NewScanner(bytes.NewReader(manifestBytes))
	if !mfScanner.Scan() {
		return errors.New("empty frames manifest")
	}
	var frameRun struct {
		Schema int            `json:"schema"`
		Kind   string         `json:"kind"`
		Run    *trace.RunInfo `json:"run"`
	}
	if err := json.Unmarshal(mfScanner.Bytes(), &frameRun); err != nil {
		return fmt.Errorf("parse frame run header: %w", err)
	}
	if frameRun.Kind != "frame_run" || frameRun.Schema != 1 {
		return fmt.Errorf("invalid frame header kind %q schema %d", frameRun.Kind, frameRun.Schema)
	}
	if frameRun.Run == nil {
		return errors.New("frame run header missing RunInfo")
	}
	if expectedROM != "" && frameRun.Run.ROMSHA256 != expectedROM {
		return fmt.Errorf("run identity mismatch: frame ROM %s, expected %s", frameRun.Run.ROMSHA256, expectedROM)
	}
	if expectedInitialState != "" && frameRun.Run.InitialStateSHA256 != "" && frameRun.Run.InitialStateSHA256 != expectedInitialState {
		return fmt.Errorf("run identity mismatch: frame initial state %s, expected %s", frameRun.Run.InitialStateSHA256, expectedInitialState)
	}
	if expectedInput != "" && frameRun.Run.ReplayInputSHA256 != "" && frameRun.Run.ReplayInputSHA256 != expectedInput {
		return fmt.Errorf("run identity mismatch: frame replay input %s, expected %s", frameRun.Run.ReplayInputSHA256, expectedInput)
	}
	if headerRec.Run != nil && frameRun.Run.ROMSHA256 != headerRec.Run.ROMSHA256 {
		return fmt.Errorf("run identity mismatch: frame ROM %s, trace ROM %s", frameRun.Run.ROMSHA256, headerRec.Run.ROMSHA256)
	}

	actualFrames := 0
	for mfScanner.Scan() {
		if len(bytes.TrimSpace(mfScanner.Bytes())) > 0 {
			actualFrames++
		}
	}
	if actualFrames != windowFrames {
		return fmt.Errorf("frame count mismatch: manifest records=%d, want %d", actualFrames, windowFrames)
	}

	sb, err := os.ReadFile(summaryPath)
	if err != nil {
		return errors.New("summary missing")
	}
	var sm struct {
		FrameSummary []struct {
			Frame     int    `json:"frame"`
			StateHash string `json:"state_hash"`
		} `json:"frame_summary"`
	}
	if err := json.Unmarshal(sb, &sm); err != nil {
		return fmt.Errorf("parse summary: %w", err)
	}
	foundFinal := false
	for _, fs := range sm.FrameSummary {
		if fs.Frame == windowFrames-1 {
			wc.FinalStateSHA = fs.StateHash
			foundFinal = true
			break
		}
	}
	if !foundFinal {
		return fmt.Errorf("summary missing final frame %d state hash", windowFrames-1)
	}
	if expectedFinalState != "" && wc.FinalStateSHA != expectedFinalState {
		return fmt.Errorf("final state mismatch: summary has %s, measured prefix has %s", wc.FinalStateSHA, expectedFinalState)
	}

	return nil
}
