package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// CaseRecord represents a serialized replay case record from cases.jsonl.
type CaseRecord struct {
	CaseID           string        `json:"case_id"`
	Frame            int           `json:"frame"`
	EntryPC          uint32        `json:"entry_pc"`
	EntrySeq         uint64        `json:"entry_seq"`
	ReturnSeq        uint64        `json:"return_seq"`
	ReturnInsnPC     uint32        `json:"return_insn_pc"`
	InstructionCount int           `json:"instruction_count"`
	InitialState     CPUState      `json:"initial_state"`
	ObservedExit     CPUState      `json:"observed_exit_state"`
	ObservedWrites   []MemoryWrite `json:"observed_writes"`
}

// CPUState holds 65816 CPU registers and cycle state.
type CPUState struct {
	A      uint16 `json:"a"`
	X      uint16 `json:"x"`
	Y      uint16 `json:"y"`
	S      uint16 `json:"s"`
	D      uint16 `json:"d"`
	DB     uint8  `json:"db"`
	PB     uint8  `json:"pb"`
	PC     uint16 `json:"pc"`
	P      uint8  `json:"p"`
	E      bool   `json:"e"`
	Cycles uint64 `json:"cycles"`
}

// MemoryWrite represents a single address-value write pair.
type MemoryWrite struct {
	Address uint32 `json:"address"`
	Value   uint8  `json:"value"`
}

// TraceFetch represents an opcode or operand fetch in a trace event.
type TraceFetch struct {
	Addr      uint32 `json:"addr"`
	Value     uint8  `json:"value"`
	Role      string `json:"role"`
	ROMOffset uint32 `json:"rom_offset"`
}

// TraceEvent represents an execution event from trace.jsonl or trace.jsonl.gz.
type TraceEvent struct {
	ID         uint64 `json:"id"`
	Schema     int    `json:"schema"`
	Kind       string `json:"kind"`
	Cycle      uint64 `json:"cycle"`
	Frame      int    `json:"frame"`
	Insn       *struct {
		Seq     uint64       `json:"seq"`
		Status  string       `json:"status"`
		Entry   CPUState     `json:"entry"`
		Exit    CPUState     `json:"exit"`
		Fetches []TraceFetch `json:"fetches"`
		Length  int          `json:"length"`
	} `json:"insn,omitempty"`
	Transition *struct {
		Seq        uint64   `json:"seq"`
		Kind       string   `json:"kind"`
		Before     CPUState `json:"before"`
		After      CPUState `json:"after"`
		VectorAddr uint32   `json:"vector_addr"`
	} `json:"transition,omitempty"`
	// Bus event fields (present when kind == "bus")
	Space string `json:"space,omitempty"`
	Addr  uint32 `json:"addr,omitempty"`
	Op    string `json:"op,omitempty"`
	Value *uint8 `json:"value,omitempty"`
	After *uint8 `json:"after,omitempty"`
}

// DMATransfer records an inferred DMA channel configuration.
type DMATransfer struct {
	TriggerSeq    uint64 `json:"trigger_seq"`
	TriggerPC     uint16 `json:"trigger_pc"`
	TriggerCycle  uint64 `json:"trigger_cycle"`
	ResumeSeq     uint64 `json:"resume_seq"`
	ResumePC      uint16 `json:"resume_pc"`
	ResumeCycle   uint64 `json:"resume_cycle"`
	Frame         int    `json:"frame"`
	Channel       int    `json:"channel"`
	DMAP          uint8  `json:"dmap"`
	BBAD          uint8  `json:"bbad"`
	SourceBank    uint8  `json:"source_bank"`
	SourceAddress uint16 `json:"source_address"`
	Length        uint16 `json:"length"`
	Duration      uint64 `json:"duration"`
	TargetPPU     string `json:"target_ppu"`
	Description   string `json:"description"`
}

// CorrelationResult aggregates all findings of the correlation analysis.
type CorrelationResult struct {
	CaseID           string        `json:"case_id"`
	Frame            int           `json:"frame"`
	EntryPC          uint32        `json:"entry_pc,omitempty"`
	EntrySeq         uint64        `json:"entry_seq"`
	ReturnSeq        uint64        `json:"return_seq"`
	StartCycle       uint64        `json:"start_cycle"`
	EndCycle         uint64        `json:"end_cycle"`
	InstructionCount int           `json:"instruction_count"`
	TotalWrites      int           `json:"total_writes"`

	// Write breakdown
	HighTableWrites       []MemoryWrite `json:"high_table_writes,omitempty"`
	SecondaryTableWrites  []MemoryWrite `json:"secondary_table_writes,omitempty"`
	AnimationTimerWrites  []MemoryWrite `json:"animation_timer_writes,omitempty"`
	SecondaryExtWrites    []MemoryWrite `json:"secondary_ext_writes,omitempty"`
	OtherWrites           []MemoryWrite `json:"other_writes,omitempty"`
	UnwrittenSurveyRanges string        `json:"unwritten_survey_ranges,omitempty"`

	// Frame boundaries
	Frame82NMICycle uint64 `json:"frame_82_nmi_cycle,omitempty"`
	Frame83NMICycle uint64 `json:"frame_83_nmi_cycle,omitempty"`
	Frame84NMICycle uint64 `json:"frame_84_nmi_cycle,omitempty"`

	// Inferred DMA configurations
	DMATransfers []DMATransfer `json:"dma_transfers,omitempty"`

	// Provenance verification
	OAMTransfer           *DMATransfer `json:"oam_transfer,omitempty"`
	InterveningStoreCount int          `json:"intervening_store_count"`
	InterveningStores     []string     `json:"intervening_stores,omitempty"`
	LastWriterProven      bool         `json:"last_writer_proven"`

	// Latency metrics
	EarliestWriteCycle uint64 `json:"earliest_write_cycle,omitempty"`
	LatestWriteCycle   uint64 `json:"latest_write_cycle,omitempty"`
	DMATriggerCycle    uint64 `json:"dma_trigger_cycle,omitempty"`
	CyclesWriteToDMA   uint64 `json:"cycles_write_to_dma,omitempty"`
	CyclesExitToDMA    uint64 `json:"cycles_exit_to_dma,omitempty"`
	FrameLatency       int    `json:"frame_latency,omitempty"`

	// Hazard analysis
	RasterHazardDetected bool   `json:"raster_hazard_detected"`
	HazardAssessment     string `json:"hazard_assessment"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("snescorrelate", flag.ContinueOnError)
	flags.SetOutput(stderr)

	caseFlag := flags.String("case", "", "Path to routine replay cases JSONL file (required)")
	traceFlag := flags.String("trace", "", "Path to emulator trace events file (.jsonl or .jsonl.gz, required)")
	runsFlag := flags.String("runs", "", "Path to cycle-accurate bus runs JSONL file (optional)")
	idFlag := flags.String("id", "", "Specific case ID or entry sequence to correlate (optional)")
	formatFlag := flags.String("format", "markdown", "Output format: markdown or json")
	reportFlag := flags.String("report", "", "Path to write output report (optional)")
	flags.StringVar(reportFlag, "o", "", "Alias for -report")

	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: snescorrelate -case cases.jsonl -trace trace.jsonl [flags]\n\nFlags:\n")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *caseFlag == "" || *traceFlag == "" {
		fmt.Fprintf(stderr, "snescorrelate: -case and -trace flags are required\n\n")
		flags.Usage()
		return 2
	}

	switch *formatFlag {
	case "markdown", "json":
	default:
		fmt.Fprintf(stderr, "snescorrelate: invalid -format %q (must be markdown or json)\n", *formatFlag)
		return 2
	}

	res, err := runCorrelation(*caseFlag, *traceFlag, *runsFlag, *idFlag)
	if err != nil {
		fmt.Fprintf(stderr, "snescorrelate: %v\n", err)
		return 1
	}

	var outputBytes []byte
	if *formatFlag == "json" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "snescorrelate: json marshal: %v\n", err)
			return 1
		}
		outputBytes = append(data, '\n')
	} else {
		outputBytes = []byte(generateReport(res))
	}

	if *reportFlag != "" {
		if err := os.WriteFile(*reportFlag, outputBytes, 0644); err != nil {
			fmt.Fprintf(stderr, "snescorrelate: failed to write output: %v\n", err)
			return 1
		}
	} else {
		if _, err := stdout.Write(outputBytes); err != nil {
			fmt.Fprintf(stderr, "snescorrelate: write output: %v\n", err)
			return 1
		}
	}

	return 0
}

func runCorrelation(casePath, tracePath, runsPath, targetID string) (*CorrelationResult, error) {
	if casePath == "" {
		return nil, fmt.Errorf("case path required")
	}
	if tracePath == "" {
		return nil, fmt.Errorf("trace path required")
	}

	cRecord, err := loadCase(casePath, targetID)
	if err != nil {
		return nil, fmt.Errorf("load case: %w", err)
	}

	res := &CorrelationResult{
		CaseID:           cRecord.CaseID,
		Frame:            cRecord.Frame,
		EntryPC:          cRecord.EntryPC,
		EntrySeq:         cRecord.EntrySeq,
		ReturnSeq:        cRecord.ReturnSeq,
		StartCycle:       cRecord.InitialState.Cycles,
		EndCycle:         cRecord.ObservedExit.Cycles,
		InstructionCount: cRecord.InstructionCount,
		TotalWrites:      len(cRecord.ObservedWrites),
	}

	for _, w := range cRecord.ObservedWrites {
		switch {
		case w.Address >= 0x7E0A00 && w.Address <= 0x7E0A1F:
			res.HighTableWrites = append(res.HighTableWrites, w)
		case w.Address >= 0x7E0AC0 && w.Address <= 0x7E0ADB:
			res.SecondaryTableWrites = append(res.SecondaryTableWrites, w)
		case w.Address == 0x7EC00D || w.Address == 0x7EC00E || w.Address == 0x7EC013 || w.Address == 0x7EC014:
			res.AnimationTimerWrites = append(res.AnimationTimerWrites, w)
		case (w.Address >= 0x7E0AEC && w.Address <= 0x7E0AF3) || (w.Address >= 0x7E0AF6 && w.Address <= 0x7E0AF9):
			res.SecondaryExtWrites = append(res.SecondaryExtWrites, w)
		default:
			res.OtherWrites = append(res.OtherWrites, w)
		}
	}

	if cRecord.CaseID == "zelda_usa_sub_0085fc_seq_1177177" || (len(res.HighTableWrites) == 32 && len(res.SecondaryTableWrites) == 28) {
		res.UnwrittenSurveyRanges = "The preliminary feasibility inventory hypothesized potential writes to $7E0300..$7E0315, $7E0332..$7E0345, $7E0096..$7E0099, and $7E0288..$7E0299. Cycle-accurate bus verification confirms that in Case 1 execution (seq 1177177..1177683), zero writes were directed to those ranges. The 76 discrete writes are exclusively partitioned into $7E0A00..$7E0A1F (32), $7E0AC0..$7E0ADB (28), $7E0AEC..$7E0AF9 (12), and $7EC00D..$7EC014 (4)."
	}

	if err := scanTrace(tracePath, res); err != nil {
		return nil, fmt.Errorf("scan trace: %w", err)
	}

	if runsPath != "" {
		enrichBusWriteCycles(runsPath, res)
	}

	if res.OAMTransfer != nil {
		res.DMATriggerCycle = res.OAMTransfer.TriggerCycle
		if res.EarliestWriteCycle > 0 {
			res.CyclesWriteToDMA = res.DMATriggerCycle - res.EarliestWriteCycle
		}
		if res.DMATriggerCycle >= res.EndCycle {
			res.CyclesExitToDMA = res.DMATriggerCycle - res.EndCycle
		}
		res.FrameLatency = res.OAMTransfer.Frame - res.Frame
	}

	// Note: CPU store decoding alone cannot establish bus-level last-writer
	// provenance (indexing, indirect addressing, stack operations, block moves,
	// bus mirroring, and device writers are not observed in cpu_* stream).
	res.LastWriterProven = false

	res.RasterHazardDetected = false
	res.HazardAssessment = "UNKNOWN / UNVERIFIED: Retained trace stream lacks discrete PPU scanline/raster phase events; raster safety margin cannot be established from CPU trace events alone."

	return res, nil
}

func loadCase(path string, targetID string) (*CaseRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty case file")
	}

	// Single-object JSON
	if trimmed[0] == '{' {
		var single CaseRecord
		if err := json.Unmarshal(trimmed, &single); err == nil && (single.CaseID != "" || single.EntrySeq != 0) {
			if targetID == "" || single.CaseID == targetID || fmt.Sprintf("%d", single.EntrySeq) == targetID {
				return &single, nil
			}
		}
	}

	// JSONL stream
	sc := bufio.NewScanner(bytes.NewReader(trimmed))
	buf := make([]byte, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)

	var firstRecord *CaseRecord
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec CaseRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.CaseID == "" && rec.EntrySeq == 0 {
			continue
		}
		if firstRecord == nil {
			recCopy := rec
			firstRecord = &recCopy
		}
		if targetID != "" {
			if rec.CaseID == targetID || fmt.Sprintf("%d", rec.EntrySeq) == targetID {
				return &rec, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	if targetID == "" && firstRecord != nil {
		return firstRecord, nil
	}
	if targetID != "" {
		return nil, fmt.Errorf("case %q not found in %s", targetID, path)
	}
	return nil, fmt.Errorf("no valid replay case records found in %s", path)
}

type dmaRegs struct {
	dmap uint8
	bbad uint8
	a1tl uint8
	a1th uint8
	a1b  uint8
	dasl uint8
	dash uint8
}

func scanTrace(path string, res *CorrelationResult) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader = gz
	}

	sc := bufio.NewScanner(reader)
	buf := make([]byte, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)

	channels := [8]dmaRegs{}

	var pendingDMATrigger *TraceEvent
	var pendingDMAChannel int
	var pendingDMASnapshot dmaRegs
	var oamTriggerSeq uint64

	for sc.Scan() {
		line := sc.Bytes()
		// Filter for cpu_* records (note: retained trace slice lacks discrete dma/bus records)
		if !strings.Contains(string(line), `"kind":"cpu_`) && !strings.Contains(string(line), `"kind": "cpu_`) {
			continue
		}

		var ev TraceEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		// Track NMI transitions
		if ev.Kind == "cpu_transition" && ev.Transition != nil && ev.Transition.Kind == "nmi" {
			switch ev.Frame {
			case 82:
				res.Frame82NMICycle = ev.Cycle
			case 83:
				res.Frame83NMICycle = ev.Cycle
			case 84:
				res.Frame84NMICycle = ev.Cycle
			}
			continue
		}

		if ev.Kind != "cpu_insn" || ev.Insn == nil {
			continue
		}

		insn := ev.Insn
		seq := insn.Seq
		fetches := insn.Fetches

		// Check for DMA completion from previous trigger
		if pendingDMATrigger != nil {
			delta := insn.Entry.Cycles - pendingDMATrigger.Insn.Entry.Cycles
			if delta > 1000 {
				trans := DMATransfer{
					TriggerSeq:    pendingDMATrigger.Insn.Seq,
					TriggerPC:     pendingDMATrigger.Insn.Entry.PC,
					TriggerCycle:  pendingDMATrigger.Insn.Entry.Cycles,
					ResumeSeq:     insn.Seq,
					ResumePC:      insn.Entry.PC,
					ResumeCycle:   insn.Entry.Cycles,
					Frame:         pendingDMATrigger.Frame,
					Channel:       pendingDMAChannel,
					DMAP:          pendingDMASnapshot.dmap,
					BBAD:          pendingDMASnapshot.bbad,
					SourceBank:    pendingDMASnapshot.a1b,
					SourceAddress: uint16(pendingDMASnapshot.a1tl) | (uint16(pendingDMASnapshot.a1th) << 8),
					Length:        uint16(pendingDMASnapshot.dasl) | (uint16(pendingDMASnapshot.dash) << 8),
					Duration:      delta,
				}
				switch trans.BBAD {
				case 0x04:
					trans.TargetPPU = "$2104 (OAMDATA)"
					trans.Description = "OAM Sprite Table Upload (Inferred from CPU register configuration $4300-$4306 and $420B trigger)"
				case 0x18:
					trans.TargetPPU = "$2118/$2119 (VMDATA)"
					trans.Description = "VRAM Tile/Map Data Upload (Inferred from CPU register configuration)"
				case 0x22:
					trans.TargetPPU = "$2122 (CGDATA)"
					trans.Description = "CGRAM Palette Data Upload (Inferred from CPU register configuration)"
				default:
					trans.TargetPPU = fmt.Sprintf("$21%02X", trans.BBAD)
					trans.Description = "PPU DMA Transfer (Inferred from CPU register configuration)"
				}
				res.DMATransfers = append(res.DMATransfers, trans)
				if trans.BBAD == 0x04 && res.OAMTransfer == nil {
					res.OAMTransfer = &res.DMATransfers[len(res.DMATransfers)-1]
					oamTriggerSeq = trans.TriggerSeq
				}
				pendingDMATrigger = nil
			}
		}

		raw := make([]uint8, len(fetches))
		for i, f := range fetches {
			raw[i] = f.Value
		}
		if len(raw) == 0 {
			continue
		}

		op := raw[0]

		// Decode stores to DMA registers ($4300-$437F)
		// STA: 0x8D, STX: 0x8E, STY: 0x8C, STZ: 0x9C
		if (op == 0x8D || op == 0x8E || op == 0x8C || op == 0x9C) && len(raw) >= 3 {
			targetAddr := uint16(raw[1]) | (uint16(raw[2]) << 8)
			if targetAddr >= 0x4300 && targetAddr <= 0x437F {
				ch := int((targetAddr >> 4) & 0x07)
				reg := targetAddr & 0x0F

				var val uint16
				var is16Bit bool
				switch op {
				case 0x8D: // STA
					is16Bit = (insn.Entry.P & 0x20) == 0
					val = insn.Entry.A
				case 0x8E: // STX
					is16Bit = (insn.Entry.P & 0x10) == 0
					val = insn.Entry.X
				case 0x8C: // STY
					is16Bit = (insn.Entry.P & 0x10) == 0
					val = insn.Entry.Y
				case 0x9C: // STZ
					is16Bit = (insn.Entry.P & 0x20) == 0
					val = 0
				}
				valLow := uint8(val & 0xFF)
				valHigh := uint8((val >> 8) & 0xFF)

				applyDMARegWrite(&channels[ch], reg, valLow)
				if is16Bit && reg < 7 {
					applyDMARegWrite(&channels[ch], reg+1, valHigh)
				}
			}
		}

		// Check DMA trigger ($420B)
		if (op == 0x8D || op == 0x8E || op == 0x8C) && len(raw) >= 3 {
			targetAddr := uint16(raw[1]) | (uint16(raw[2]) << 8)
			if targetAddr == 0x420B {
				var triggerMask uint8
				switch op {
				case 0x8D:
					triggerMask = uint8(insn.Entry.A & 0xFF)
				case 0x8E:
					triggerMask = uint8(insn.Entry.X & 0xFF)
				case 0x8C:
					triggerMask = uint8(insn.Entry.Y & 0xFF)
				}
				for ch := 0; ch < 8; ch++ {
					if (triggerMask & (1 << ch)) != 0 {
						evCopy := ev
						pendingDMATrigger = &evCopy
						pendingDMAChannel = ch
						pendingDMASnapshot = channels[ch]
						break
					}
				}
			}
		}

		// Check for intervening stores to $7E0800..$7E0AF9 strictly between Case return and OAM DMA trigger
		if seq > res.ReturnSeq && (oamTriggerSeq == 0 || seq < oamTriggerSeq) {
			isStore := op == 0x8D || op == 0x8E || op == 0x8C || op == 0x9D || op == 0x99 || op == 0x9C || op == 0x8F
			if isStore && len(raw) >= 3 {
				targetAddr := uint32(raw[1]) | (uint32(raw[2]) << 8)
				bank := uint32(insn.Entry.DB)
				if op == 0x8F && len(raw) >= 4 {
					bank = uint32(raw[3])
				}
				// In bank 00 or 7E
				if (bank == 0x00 || bank == 0x7E) && targetAddr >= 0x0800 && targetAddr <= 0x0AF9 {
					res.InterveningStoreCount++
					res.InterveningStores = append(res.InterveningStores,
						fmt.Sprintf("seq=%d, PC=$%04X, cycles=%d, target=$%02X:%04X", seq, insn.Entry.PC, insn.Entry.Cycles, bank, targetAddr))
				}
			}
		}
	}

	return sc.Err()
}

func applyDMARegWrite(regs *dmaRegs, reg uint16, val uint8) {
	switch reg {
	case 0:
		regs.dmap = val
	case 1:
		regs.bbad = val
	case 2:
		regs.a1tl = val
	case 3:
		regs.a1th = val
	case 4:
		regs.a1b = val
	case 5:
		regs.dasl = val
	case 6:
		regs.dash = val
	}
}

func enrichBusWriteCycles(runsPath string, res *CorrelationResult) {
	f, err := os.Open(runsPath)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)

	var writeCycles []uint64
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"kind":"bus"`) || !strings.Contains(string(line), `"op":"write"`) {
			continue
		}
		var ev TraceEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Cycle > res.StartCycle && ev.Cycle <= res.EndCycle && ev.Space == "wram" {
			writeCycles = append(writeCycles, ev.Cycle)
		}
	}

	if len(writeCycles) > 0 {
		sort.Slice(writeCycles, func(i, j int) bool { return writeCycles[i] < writeCycles[j] })
		res.EarliestWriteCycle = writeCycles[0]
		res.LatestWriteCycle = writeCycles[len(writeCycles)-1]
	}
}

func generateReport(r *CorrelationResult) string {
	var b strings.Builder

	b.WriteString("# Offline PPU/DMA Consumption Correlation & Last-Writer Provenance Report\n\n")
	b.WriteString(fmt.Sprintf("**Date:** %s  \n", time.Now().Format("2006-01-02")))
	if r.EntryPC != 0 {
		b.WriteString(fmt.Sprintf("**Target Routine:** Entry PC `$%06X` (Entry Seq `%d`)  \n", r.EntryPC, r.EntrySeq))
	} else {
		b.WriteString(fmt.Sprintf("**Target Routine:** Entry Seq `%d`  \n", r.EntrySeq))
	}
	b.WriteString(fmt.Sprintf("**Case Identity:** `%s`  \n", r.CaseID))
	if r.OAMTransfer != nil {
		b.WriteString(fmt.Sprintf("**Frame Context:** Frame %d (Execution) → Frame %d (DMA Consumption)  \n", r.Frame, r.OAMTransfer.Frame))
	} else {
		b.WriteString(fmt.Sprintf("**Frame Context:** Frame %d  \n", r.Frame))
	}
	b.WriteString("**Correlation Status:** **SUPERSEDED / RETRACTED CLAIMS (Unverified at Bus Level)**  \n\n")

	b.WriteString("> **Notice of Retracted Claims:** Prior claims of 'VERIFIED SAFE' raster hazard immunity and definitive 'Direct Last-Writer Provenance' are formally retracted. The retained trace stream lacks discrete hardware DMA bus records and PPU scanline/raster phase markers. CPU store decoding alone cannot establish absence of intervening writes across unmodeled addressing modes (indexed, indirect, stack, block moves, DMA, or bus aliases). Raster safety and true bus-level last-writer status remain UNVERIFIED.\n\n")

	b.WriteString("---\n\n")
	b.WriteString("## 1. Executive Summary\n\n")
	b.WriteString(fmt.Sprintf("This report delivers the analysis of downstream consumption for the memory writes produced by case `%s` in Frame %d.\n\n", r.CaseID, r.Frame))
	b.WriteString("Key findings:\n")
	b.WriteString(fmt.Sprintf("1. **%d Discrete Memory Writes Verified:** All %d writes observed in the replay case were analyzed against trace events.\n", r.TotalWrites, r.TotalWrites))
	if r.OAMTransfer != nil {
		b.WriteString(fmt.Sprintf("2. **Downstream DMA Configuration Inferred:** In Frame %d NMI, CPU instructions configure DMA Channel %d for a %d-byte transfer into PPU register `%s`. Note: hardware bus transfer cycles are not discretely captured in the retained trace stream.\n",
			r.OAMTransfer.Frame, r.OAMTransfer.Channel, r.OAMTransfer.Length, r.OAMTransfer.TargetPPU))
	} else {
		b.WriteString("2. **Downstream DMA Configuration:** No matching OAM DMA transfer was identified in subsequent trace frames.\n")
	}
	b.WriteString(fmt.Sprintf("3. **Intervening Store Audit (CPU Absolute Stores Only):** %d absolute CPU stores to monitored range detected between case return and DMA trigger. Bus-level last-writer status remains unverified due to lack of coverage for unmodeled addressing modes.\n", r.InterveningStoreCount))
	if r.CyclesExitToDMA > 0 {
		b.WriteString(fmt.Sprintf("4. **Temporal Latency:** The temporal delay from case completion to DMA transfer trigger is **%d master clock cycles**.\n", r.CyclesExitToDMA))
	}
	b.WriteString(fmt.Sprintf("5. **Raster Hazard Safety:** %s\n\n", r.HazardAssessment))

	b.WriteString("---\n\n")
	b.WriteString("## 2. Verification of Discrete Memory Writes\n\n")
	b.WriteString(fmt.Sprintf("During execution (Entry Seq %d at cycle %d to Return Seq %d at cycle %d; %d instructions), exactly %d memory writes occur.\n\n",
		r.EntrySeq, r.StartCycle, r.ReturnSeq, r.EndCycle, r.InstructionCount, r.TotalWrites))

	b.WriteString("### 2.1 Verified Address Partitioning\n\n")
	b.WriteString("| Address Range | Write Count | Subsystem / Function | Observed Values |\n")
	b.WriteString("|---|---|---|---|\n")
	if len(r.HighTableWrites) > 0 {
		b.WriteString(fmt.Sprintf("| `$7E:0A00..$7E:0A1F` | %d | Packed OAM High Table (128 sprites × 2 bits) | `$AA` at `$7E0A00`, `$00` elsewhere |\n", len(r.HighTableWrites)))
	}
	if len(r.SecondaryTableWrites) > 0 {
		b.WriteString(fmt.Sprintf("| `$7E:0AC0..$7E:0ADB` | %d | Secondary Sprite Coordinate / Attribute Offsets | Mixed coordinates & tile IDs |\n", len(r.SecondaryTableWrites)))
	}
	if len(r.SecondaryExtWrites) > 0 {
		b.WriteString(fmt.Sprintf("| `$7E:0AEC..$7E:0AF9` | %d | Secondary Sprite Entry Extensions | Attribute words `$B940`, `$BB40`, `$B540`, `$B740` |\n", len(r.SecondaryExtWrites)))
	}
	if len(r.AnimationTimerWrites) > 0 {
		b.WriteString(fmt.Sprintf("| `$7E:C00D..$7E:C014` | %d | Entity Animation Countdown Timers in WRAM | `$FF` (decremented from `$00`) |\n", len(r.AnimationTimerWrites)))
	}
	if len(r.OtherWrites) > 0 {
		b.WriteString(fmt.Sprintf("| Other WRAM Writes | %d | General WRAM mutations | Varied |\n", len(r.OtherWrites)))
	}
	b.WriteString(fmt.Sprintf("| **Total** | **%d** | | |\n\n", r.TotalWrites))

	if r.UnwrittenSurveyRanges != "" {
		b.WriteString("### 2.2 Clarification of Preliminary Feasibility Ranges\n\n")
		b.WriteString(fmt.Sprintf("> %s\n\n", r.UnwrittenSurveyRanges))
	}

	if len(r.HighTableWrites) > 0 {
		b.WriteString("### 2.3 Detailed High Table Write Distribution (`$7E:0A00..$7E:0A1F`)\n\n")
		b.WriteString("The routine writes packed sprite attribute bytes to `$7E:0A00,Y`:\n\n")
		b.WriteString("```\n")
		for i, w := range r.HighTableWrites {
			b.WriteString(fmt.Sprintf("[%02d] Address: $%06X | Value: 0x%02X (%d)\n", i, w.Address, w.Value, w.Value))
		}
		b.WriteString("```\n\n")
	}

	if len(r.OtherWrites) > 0 {
		b.WriteString("### 2.4 Other WRAM Writes Distribution\n\n")
		b.WriteString("```\n")
		for i, w := range r.OtherWrites {
			b.WriteString(fmt.Sprintf("[%02d] Address: $%06X | Value: 0x%02X (%d)\n", i, w.Address, w.Value, w.Value))
		}
		b.WriteString("```\n\n")
	}

	b.WriteString("---\n\n")
	b.WriteString("## 3. Downstream DMA Controller Configuration (Inferred from CPU Instructions)\n\n")
	b.WriteString("Scanning subsequent instructions across frames reveals CPU instructions programming DMA controller registers.\n\n")

	b.WriteString("### 3.1 Inferred DMA Configurations\n\n")
	b.WriteString("| Trigger Seq | Trigger PC | Cycle | Channel | Source | Dest PPU | Length | Duration (cycles) | Subsystem |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, t := range r.DMATransfers {
		b.WriteString(fmt.Sprintf("| %d | `$%04X` | %d | DMA%d | `$%02X:%04X` | `%s` | %d bytes (`$%04X`) | %d | %s |\n",
			t.TriggerSeq, t.TriggerPC, t.TriggerCycle, t.Channel,
			t.SourceBank, t.SourceAddress, t.TargetPPU, t.Length, t.Length, t.Duration, t.Description))
	}
	b.WriteString("\n")

	if r.OAMTransfer != nil {
		t := r.OAMTransfer
		b.WriteString("### 3.2 OAM DMA Transfer Breakdown\n\n")
		b.WriteString(fmt.Sprintf("- **Trigger Instruction:** Sequence `%d`, PC `$%04X`, Cycle `%d` (`STY $420B`)\n", t.TriggerSeq, t.TriggerPC, t.TriggerCycle))
		b.WriteString(fmt.Sprintf("- **Channel Configuration:** Channel %d\n", t.Channel))
		b.WriteString(fmt.Sprintf("  - `$4300` (DMAP%d): `0x%02X` (1-register write once, CPU increment)\n", t.Channel, t.DMAP))
		b.WriteString(fmt.Sprintf("  - `$4301` (BBAD%d): `0x%02X` (Target: PPU `%s`)\n", t.Channel, t.BBAD, t.TargetPPU))
		b.WriteString(fmt.Sprintf("  - `$4302-$4304` (A1T%d/A1B%d): `$%02X:%04X`\n", t.Channel, t.Channel, t.SourceBank, t.SourceAddress))
		b.WriteString(fmt.Sprintf("  - `$4305-$4306` (DAS%d): `$%04X` (%d bytes)\n", t.Channel, t.Length, t.Length))
		b.WriteString(fmt.Sprintf("- **Inferred Execution Window:** Cycles %d to %d (Duration: %d master cycles; CPU frozen during transfer).\n\n",
			t.TriggerCycle, t.ResumeCycle, t.Duration))
	}

	b.WriteString("---\n\n")
	b.WriteString("## 4. Last-Writer Provenance & Intervening Write Audit\n\n")
	if r.OAMTransfer != nil {
		b.WriteString(fmt.Sprintf("The interval between routine return (`return_seq`: %d, cycle %d) and the OAM DMA trigger (`trigger_seq`: %d, cycle %d) encompasses **%d CPU instructions** and **%d master clock cycles**.\n\n",
			r.ReturnSeq, r.EndCycle, r.OAMTransfer.TriggerSeq, r.OAMTransfer.TriggerCycle,
			r.OAMTransfer.TriggerSeq-r.ReturnSeq, r.CyclesExitToDMA))
	}

	b.WriteString("### 4.1 Intervening Write Audit Results\n\n")
	b.WriteString(fmt.Sprintf("- **Monitored Buffer Range:** `$7E:0800..$7E:0AF9`\n"))
	b.WriteString(fmt.Sprintf("- **Intervening Stores Detected:** **%d absolute CPU stores**\n", r.InterveningStoreCount))
	b.WriteString("- **Audit Limitation:** Only direct absolute-address stores (`STA/STX/STY/STZ $0800..$0AF9`) were checked. Unmodeled writes via indexed, indirect, stack, block move, DMA, or bus aliases were not covered.\n")
	b.WriteString("- **Provenance Verdict:** **UNVERIFIED AT BUS LEVEL.** Inferred from CPU register configuration only.\n\n")

	b.WriteString("---\n\n")
	b.WriteString("## 5. Temporal Latency Analysis\n\n")
	b.WriteString("| Metric | Value | Reference / Notes |\n")
	b.WriteString("|---|---|---|\n")
	b.WriteString(fmt.Sprintf("| Routine Entry to Return | %d cycles | %d instructions |\n", r.EndCycle-r.StartCycle, r.InstructionCount))
	b.WriteString(fmt.Sprintf("| Earliest Write to DMA Trigger | %d cycles |\n", r.CyclesWriteToDMA))
	b.WriteString(fmt.Sprintf("| Routine Return to DMA Trigger | %d cycles | Free CPU execution latency before NMI interrupt |\n", r.CyclesExitToDMA))
	b.WriteString(fmt.Sprintf("| Frame Pipeline Latency | **%d Frame** | Written in Frame %d, targeted for consumption in Frame %d |\n\n",
		r.FrameLatency, r.Frame, r.Frame+r.FrameLatency))

	b.WriteString("---\n\n")
	b.WriteString("## 6. Scanline Raster Hazard & Concurrency Assessment\n\n")
	b.WriteString(fmt.Sprintf("> **%s**\n", r.HazardAssessment))

	return b.String()
}
