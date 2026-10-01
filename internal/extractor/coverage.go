package extractor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
)

type coverageRange struct {
	Space string `json:"space"`
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}
type producerSummary struct {
	ROM       string          `json:"rom_hash"`
	Engine    string          `json:"emulator"`
	Trace     string          `json:"trace_hash"`
	Truncated bool            `json:"truncated"`
	Events    []string        `json:"event_kinds"`
	PC        []coverageRange `json:"pc_ranges"`
	Addresses []coverageRange `json:"address_ranges"`
	Op        string          `json:"op"`
	Channels  []int           `json:"dma_channels"`
}
type producerHeader struct {
	Kind string `json:"kind"`
	Run  *struct {
		ROM     string   `json:"rom_sha256"`
		Engine  string   `json:"engine_revision"`
		Dirty   bool     `json:"engine_dirty"`
		Events  []string `json:"events"`
		Filters *struct {
			PC []coverageRange `json:"pc_ranges"`
		} `json:"filters"`
	} `json:"run"`
}
type producerCoverage struct {
	summary                producerSummary
	summaryRef, receiptRef *FileRef
}

func readCoverageJSON(path string, out any) (*FileRef, error) {
	if path == "" {
		return nil, fmt.Errorf("producer coverage: missing metadata path")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("producer coverage: %w", err)
	}
	if err = json.Unmarshal(b, out); err != nil {
		return nil, fmt.Errorf("producer coverage: parse metadata: %w", err)
	}
	sum := sha256.Sum256(b)
	return &FileRef{Path: path, SHA256: hex.EncodeToString(sum[:])}, nil
}

func loadProducerCoverage(summaryPath, receiptPath, raw, decoded, rom string, capture bool) (*producerCoverage, error) {
	c := new(producerCoverage)
	var err error
	c.summaryRef, err = readCoverageJSON(summaryPath, &c.summary)
	if err != nil {
		return nil, err
	}
	var receipt ReceiptData
	c.receiptRef, err = readCoverageJSON(receiptPath, &receipt)
	if err != nil {
		return nil, err
	}
	s := c.summary
	if receipt.Schema != 2 || receipt.Outcome != "complete" || receipt.StreamSHA256 != decoded || s.Trace != decoded || s.Truncated {
		return nil, fmt.Errorf("producer coverage: incomplete or mismatched trace identity")
	}
	if s.ROM != rom || s.Engine == "" {
		return nil, fmt.Errorf("producer coverage: ROM or engine identity mismatch")
	}
	for _, r := range append(append([]coverageRange(nil), s.PC...), s.Addresses...) {
		if r.Start > r.End {
			return nil, fmt.Errorf("producer coverage: invalid range")
		}
	}
	if capture {
		for _, kind := range []string{"bus", "mmio", "cpu_insn", "cpu_transition", "dma", "hdma"} {
			if !hasCoverageEvent(s.Events, kind) {
				return nil, fmt.Errorf("producer coverage: missing event kind %s", kind)
			}
		}
		if s.Op != "" || len(s.Addresses) > 0 || len(s.Channels) > 0 {
			return nil, fmt.Errorf("producer coverage: restricted capture bus filters")
		}
	} else if !hasCoverageEvent(s.Events, "bus") || s.Op != "write" || len(s.PC) > 0 {
		return nil, fmt.Errorf("producer coverage: restricted history write coverage")
	}
	return c, nil
}
func hasCoverageEvent(events []string, kind string) bool {
	for _, e := range events {
		if e == kind {
			return true
		}
	}
	return false
}
func (c *producerCoverage) checkHeader(h *producerHeader) error {
	if h == nil || h.Kind != "run" || h.Run == nil {
		return fmt.Errorf("producer coverage: missing capture run header")
	}
	r := h.Run
	if r.ROM != c.summary.ROM || r.Engine != c.summary.Engine || r.Dirty {
		return fmt.Errorf("producer coverage: header identity mismatch")
	}
	var ranges []coverageRange
	if r.Filters != nil {
		ranges = r.Filters.PC
	}
	if !reflect.DeepEqual(ranges, c.summary.PC) || !sameCoverageEvents(r.Events, c.summary.Events) {
		return fmt.Errorf("producer coverage: header filters or events mismatch")
	}
	return nil
}
func sameCoverageEvents(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]bool)
	for _, s := range a {
		if m[s] {
			return false
		}
		m[s] = true
	}
	for _, s := range b {
		if !m[s] {
			return false
		}
		delete(m, s)
	}
	return len(m) == 0
}
func coveredBy(ranges []coverageRange, space string, addr uint32) bool {
	for _, r := range ranges {
		if r.Space == space && r.Start <= addr && addr <= r.End {
			return true
		}
	}
	return false
}
func (c *producerCoverage) checkInterval(insns map[uint64]*RawEvent, first, last uint64) error {
	if first > last || last-first > MaxCaptureEvents {
		return fmt.Errorf("producer coverage: invalid sequence interval")
	}
	for seq := first; ; seq++ {
		e := insns[seq]
		if e == nil || e.Insn == nil || e.Insn.Status != "retired" {
			return fmt.Errorf("producer coverage: missing interval instruction")
		}
		pc := uint32(e.Insn.Entry.PB)<<16 | uint32(e.Insn.Entry.PC)
		if len(c.summary.PC) > 0 && !coveredBy(c.summary.PC, "cpu", pc) {
			return fmt.Errorf("producer coverage: uncovered instruction PC")
		}
		if seq == last {
			break
		}
	}
	return nil
}
func (c *producerCoverage) checkMemory(cells []MemoryCell) error {
	for _, cell := range cells {
		addr := cell.Address
		if addr < 0x7e0000 || addr > 0x7fffff {
			return fmt.Errorf("producer coverage: initial memory is not canonical WRAM")
		}
		if len(c.summary.Addresses) > 0 && !coveredBy(c.summary.Addresses, "wram", addr-0x7e0000) {
			return fmt.Errorf("producer coverage: uncovered initial memory")
		}
	}
	return nil
}

// coveredAccessCounts describes only the reviewed direct-memory and stack modes.
// Indirect pointer reads and read-modify-write bus sequences remain unsupported.
func coveredAccessCounts(insn *RawInsn) (int, int, error) {
	if len(insn.Fetches) == 0 {
		return 0, 0, fmt.Errorf("producer coverage: missing instruction fetch")
	}
	op := insn.Fetches[0].Value
	c, ok := supportedOpcodeContracts[op]
	if !ok {
		return 0, 0, fmt.Errorf("producer coverage: unsupported memory opcode")
	}
	width := 1
	switch op {
	case 0x81, 0x91, 0x87, 0x97, 0xa1, 0xb1, 0xa7, 0xb7, 0xd4:
		return 0, 0, fmt.Errorf("producer coverage: unsupported indirect access count")
	case 0x85, 0x8d, 0x9d, 0x99, 0x8f, 0x9f, 0x95, 0x64, 0x9c, 0x74, 0x9e, 0xa5, 0xad, 0xaf, 0xb5, 0xbd, 0xb9, 0xbf, 0x48, 0x68:
		if insn.Entry.P&0x20 == 0 {
			width = 2
		}
	case 0x86, 0x8e, 0x96, 0x84, 0x8c, 0x94, 0xa6, 0xae, 0xb6, 0xbe, 0xa4, 0xac, 0xb4, 0xbc, 0xda, 0x5a, 0xfa, 0x7a:
		if insn.Entry.P&0x10 == 0 {
			width = 2
		}
	}
	return c.MinReads * width, c.MinWrites * width, nil
}
func checkCoveredAccesses(insns []*RawInsn, events BusEventList) error {
	for _, insn := range insns {
		wantRead, wantWrite, err := coveredAccessCounts(insn)
		if err != nil {
			return err
		}
		reads, writes := 0, 0
		for _, e := range events {
			if e.Cycle <= insn.Entry.Cycles || e.Cycle > insn.Exit.Cycles {
				continue
			}
			if e.Space == "cpu" && e.Source != nil && e.Source.Space == "rom" {
				continue
			}
			var actor struct {
				Actor string          `json:"actor"`
				DMA   json.RawMessage `json:"dma"`
				CPU   *struct {
					Opcode *uint8  `json:"opcode"`
					PB     *uint8  `json:"pbr"`
					PC     *uint16 `json:"pc"`
					Bytes  []uint8 `json:"bytes"`
				} `json:"cpu"`
			}
			if len(e.Raw) > 0 {
				if err := json.Unmarshal(e.Raw, &actor); err != nil {
					return fmt.Errorf("producer coverage: invalid bus context")
				}
			}
			if e.CPU == nil || (actor.Actor != "" && actor.Actor != "cpu") || (len(actor.DMA) > 0 && string(actor.DMA) != "null") {
				return fmt.Errorf("producer coverage: non-CPU data access")
			}
			c := actor.CPU
			if c == nil || c.Opcode == nil || c.PB == nil || c.PC == nil || *c.Opcode != insn.Fetches[0].Value || *c.PB != insn.Entry.PB || *c.PC != insn.Entry.PC {
				return fmt.Errorf("producer coverage: mismatched CPU instruction context")
			}
			if len(c.Bytes) > len(insn.Fetches) {
				return fmt.Errorf("producer coverage: mismatched CPU fetched bytes")
			}
			for i, b := range c.Bytes {
				if b != insn.Fetches[i].Value {
					return fmt.Errorf("producer coverage: mismatched CPU fetched bytes")
				}
			}
			if e.Space != "wram" || e.PC == nil || e.PC.Bank != insn.Entry.PB || e.PC.Addr != insn.Entry.PC {
				return fmt.Errorf("producer coverage: unjoined data access")
			}
			switch e.Op {
			case "read":
				reads++
			case "write":
				writes++
			default:
				return fmt.Errorf("producer coverage: invalid bus operation")
			}
		}
		if reads != wantRead || writes != wantWrite {
			return fmt.Errorf("producer coverage: instruction access count mismatch: seq=%d read=%d/%d write=%d/%d", insn.Seq, reads, wantRead, writes, wantWrite)
		}
	}
	return nil
}
