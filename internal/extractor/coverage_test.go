package extractor

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProducerCoverageMissing(t *testing.T) {
	d := t.TempDir()
	write := func(n, s string) string {
		p := filepath.Join(d, n)
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	_, err := Extract(Config{Candidate: Candidate{ID: "missing", Entry: 0x8000, InstructionCount: 1}, ROMPath: write("rom", "x"), HistoryPath: write("history", ""), CapturePath: write("capture", ""), FixturePath: write("fixture", "{\"run\":{\"engine_revision\":\"test\"}}\n")})
	if err == nil || !strings.Contains(err.Error(), "producer coverage") {
		t.Fatalf("missing coverage accepted: %v", err)
	}
}

func TestProducerCoverageFilters(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*producerSummary, *ReceiptData)
		want   string
	}{
		{"valid", func(*producerSummary, *ReceiptData) {}, ""},
		{"op", func(s *producerSummary, r *ReceiptData) { s.Op = "write" }, "restricted"},
		{"address", func(s *producerSummary, r *ReceiptData) {
			s.Addresses = []coverageRange{{Space: "wram", Start: 0, End: 255}}
		}, "restricted"},
		{"channel", func(s *producerSummary, r *ReceiptData) { s.Channels = []int{0} }, "restricted"},
		{"event", func(s *producerSummary, r *ReceiptData) { s.Events = s.Events[:5] }, "missing event"},
		{"truncated", func(s *producerSummary, r *ReceiptData) { s.Truncated = true }, "incomplete"},
		{"outcome", func(s *producerSummary, r *ReceiptData) { r.Outcome = "event_limit" }, "incomplete"},
		{"digest", func(s *producerSummary, r *ReceiptData) { s.Trace = "other" }, "identity"},
		{"ROM", func(s *producerSummary, r *ReceiptData) { s.ROM = "other" }, "identity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			s := producerSummary{ROM: "rom", Engine: "engine", Trace: "trace", Events: []string{"bus", "mmio", "cpu_insn", "cpu_transition", "dma", "hdma"}}
			r := ReceiptData{Schema: 2, Outcome: "complete", StreamSHA256: "trace"}
			tt.mutate(&s, &r)
			sp := filepath.Join(d, "summary")
			rp := filepath.Join(d, "receipt")
			for p, v := range map[string]any{sp: s, rp: r} {
				b, _ := json.Marshal(v)
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := loadProducerCoverage(sp, rp, "trace", "trace", "rom", true)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v want %s", err, tt.want)
			}
		})
	}
}

func TestProducerCoverageIntervalAndMemory(t *testing.T) {
	c := producerCoverage{summary: producerSummary{PC: []coverageRange{{Space: "cpu", Start: 0x8000, End: 0x8002}}}}
	ins := map[uint64]*RawEvent{}
	for i := uint64(1); i <= 3; i++ {
		ins[i] = &RawEvent{Insn: &RawInsn{Seq: i, Fetches: []FetchRec{{Value: 0xea}}, Status: "retired", Entry: CPUState{PC: uint16(0x7fff + i), P: 0x30}}}
	}
	if err := c.checkInterval(ins, 1, 3); err != nil {
		t.Fatal(err)
	}
	delete(ins, 2)
	if err := c.checkInterval(ins, 1, 3); err == nil {
		t.Fatal("missing instruction accepted")
	}
	ins[2] = &RawEvent{Insn: &RawInsn{Seq: 2, Status: "retired", Entry: CPUState{PC: 0x9000}}}
	if err := c.checkInterval(ins, 1, 3); err == nil {
		t.Fatal("uncovered PC accepted")
	}
	c.summary.Addresses = []coverageRange{{Space: "wram", Start: 0x100, End: 0x1ff}}
	if err := c.checkMemory([]MemoryCell{{Address: 0x7e01ff}}); err != nil {
		t.Fatal(err)
	}
	if err := c.checkMemory([]MemoryCell{{Address: 0x7e0200}}); err == nil {
		t.Fatal("uncovered byte accepted")
	}
}

func TestProducerCoverageWordStore(t *testing.T) {
	insn := &RawInsn{Seq: 1, Entry: CPUState{P: 0x10, PC: 0x8000, Cycles: 10}, Exit: CPUState{Cycles: 20}, Fetches: []FetchRec{{Value: 0x8d}}}
	pc := &EventPC{Addr: 0x8000}
	one := BusEventList{{Raw: []byte(`{"cpu":{"opcode":141,"pbr":0,"pc":32768,"bytes":[141]}}`), Kind: "bus", Space: "wram", CPU: &EventCPU{}, PC: pc, Op: "write", Cycle: 15, Addr: 0x100}}
	if err := checkCoveredAccesses([]*RawInsn{insn}, one); err == nil {
		t.Fatal("missing high-byte write accepted")
	}
	two := append(one, &RawEvent{Raw: []byte(`{"cpu":{"opcode":141,"pbr":0,"pc":32768,"bytes":[141]}}`), Kind: "bus", Space: "wram", CPU: &EventCPU{}, PC: pc, Op: "write", Cycle: 18, Addr: 0x101})
	if err := checkCoveredAccesses([]*RawInsn{insn}, two); err != nil {
		t.Fatal(err)
	}
}

func TestProducerCoverageHeader(t *testing.T) {
	c := producerCoverage{summary: producerSummary{ROM: "r", Engine: "e", Events: []string{"bus"}, PC: []coverageRange{{Space: "cpu", Start: 0x8000, End: 0xffff}}}}
	var h producerHeader
	if err := json.Unmarshal([]byte(`{"kind":"run","run":{"rom_sha256":"r","engine_revision":"e","events":["bus"],"filters":{"pc_ranges":[{"space":"cpu","start":32768,"end":65535}]}}}`), &h); err != nil {
		t.Fatal(err)
	}
	if err := c.checkHeader(&h); err != nil {
		t.Fatal(err)
	}
	h.Run.Filters.PC[0].End--
	if err := c.checkHeader(&h); err == nil {
		t.Fatal("conflicting header filter accepted")
	}
	h.Run.Filters.PC[0].End++
	h.Run.Engine = "different"
	if err := c.checkHeader(&h); err == nil {
		t.Fatal("conflicting engine accepted")
	}
}

func TestProducerCoverageWidthCounts(t *testing.T) {
	for _, tt := range []struct {
		op, p         byte
		reads, writes int
	}{
		{0x8d, 0x10, 0, 2}, {0x8d, 0x30, 0, 1}, {0xad, 0x10, 2, 0}, {0x8e, 0x20, 0, 2}, {0xda, 0x20, 0, 2}, {0x48, 0x10, 0, 2}, {0x68, 0x10, 2, 0}, {0x08, 0, 0, 1}, {0x6b, 0, 3, 0},
	} {
		r, w, err := coveredAccessCounts(&RawInsn{Entry: CPUState{P: tt.p}, Fetches: []FetchRec{{Value: tt.op}}})
		if err != nil || r != tt.reads || w != tt.writes {
			t.Fatalf("op%x p%x: %d %d %v", tt.op, tt.p, r, w, err)
		}
	}
	if _, _, err := coveredAccessCounts(&RawInsn{Fetches: []FetchRec{{Value: 0x91}}}); err == nil {
		t.Fatal("unmodeled pointer reads accepted")
	}
}

func TestProducerCoverageHistoryFilters(t *testing.T) {
	for _, name := range []string{"valid", "read", "PC", "missingbus"} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			s := producerSummary{ROM: "rom", Engine: "engine", Trace: "trace", Events: []string{"bus"}, Op: "write"}
			r := ReceiptData{Schema: 2, Outcome: "complete", StreamSHA256: "trace"}
			switch name {
			case "read":
				s.Op = "read"
			case "PC":
				s.PC = []coverageRange{{Space: "cpu", Start: 0x8000, End: 0xffff}}
			case "missingbus":
				s.Events = nil
			}
			sp := filepath.Join(d, "summary")
			rp := filepath.Join(d, "receipt")
			for p, v := range map[string]any{sp: s, rp: r} {
				b, _ := json.Marshal(v)
				if err := os.WriteFile(p, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := loadProducerCoverage(sp, rp, "trace", "trace", "rom", false)
			if (err == nil) != (name == "valid") {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestProducerCoverageCPUActor(t *testing.T) {
	insn := &RawInsn{Entry: CPUState{P: 0x30, PC: 0x8000, Cycles: 10}, Exit: CPUState{Cycles: 20}, Fetches: []FetchRec{{Value: 0x8d}}}
	e := &RawEvent{Raw: []byte(`{"cpu":{"opcode":141,"pbr":0,"pc":32768,"bytes":[141]}}`), Kind: "bus", Space: "wram", PC: &EventPC{Addr: 0x8000}, CPU: &EventCPU{}, Op: "write", Cycle: 15, Addr: 0x100}
	if err := checkCoveredAccesses([]*RawInsn{insn}, BusEventList{e}); err != nil {
		t.Fatal(err)
	}
	e.Raw = []byte(`{"dma":{"channel":0}}`)
	if err := checkCoveredAccesses([]*RawInsn{insn}, BusEventList{e}); err == nil {
		t.Fatal("DMA masquerading as CPU accepted")
	}
	e.Raw = nil
	e.CPU = nil
	if err := checkCoveredAccesses([]*RawInsn{insn}, BusEventList{e}); err == nil {
		t.Fatal("missing CPU context accepted")
	}
}

func TestProducerCoverageGzip(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "capture.jsonl.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := gzip.NewWriter(f)
	if _, err := z.Write([]byte("{\"kind\":\"run\"}\n")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	scanner, finish, err := OpenHashedStream(p)
	if err != nil {
		t.Fatal(err)
	}
	for scanner.Scan() {
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	raw, decoded, err := finish()
	if err != nil {
		t.Fatal(err)
	}
	if raw == decoded {
		t.Fatal("gzip hashes unexpectedly equal")
	}
	s := producerSummary{ROM: "rom", Engine: "engine", Trace: decoded, Events: []string{"bus", "mmio", "cpu_insn", "cpu_transition", "dma", "hdma"}}
	r := ReceiptData{Schema: 2, Outcome: "complete", StreamSHA256: decoded}
	sp := filepath.Join(d, "summary")
	rp := filepath.Join(d, "receipt")
	write := func() {
		for p, v := range map[string]any{sp: s, rp: r} {
			b, _ := json.Marshal(v)
			if err := os.WriteFile(p, b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	write()
	if _, err := loadProducerCoverage(sp, rp, raw, decoded, "rom", true); err != nil {
		t.Fatal(err)
	}
	s.Trace = raw
	write()
	if _, err := loadProducerCoverage(sp, rp, raw, decoded, "rom", true); err == nil {
		t.Fatal("container SHA used as logical stream accepted")
	}
	s.Trace = decoded
	r.StreamSHA256 = raw
	write()
	if _, err := loadProducerCoverage(sp, rp, raw, decoded, "rom", true); err == nil {
		t.Fatal("wrong decoded receipt accepted")
	}
}

func TestProducerCoverageCPUJoin(t *testing.T) {
	insn := &RawInsn{Entry: CPUState{P: 0x30, PC: 0x8000, Cycles: 10}, Exit: CPUState{Cycles: 20}, Fetches: []FetchRec{{Value: 0x85}, {Value: 0x9c}}}
	for _, tt := range []struct {
		name, raw string
		valid     bool
	}{
		{"valid", `{"cpu":{"opcode":133,"pbr":0,"pc":32768,"bytes":[133,156]}}`, true},
		{"prefix", `{"cpu":{"opcode":133,"pbr":0,"pc":32768,"bytes":[133]}}`, true},
		{"opcode", `{"cpu":{"opcode":0,"pbr":0,"pc":32768,"bytes":[133,156]}}`, false},
		{"PB", `{"cpu":{"opcode":133,"pbr":1,"pc":32768,"bytes":[133,156]}}`, false},
		{"PC", `{"cpu":{"opcode":133,"pbr":0,"pc":32769,"bytes":[133,156]}}`, false},
		{"missingopcode", `{"cpu":{"pbr":0,"pc":32768}}`, false},
		{"missingPB", `{"cpu":{"opcode":133,"pc":32768}}`, false},
		{"missingPC", `{"cpu":{"opcode":133,"pbr":0}}`, false},
		{"bytes", `{"cpu":{"opcode":133,"pbr":0,"pc":32768,"bytes":[133,155]}}`, false},
		{"extra", `{"cpu":{"opcode":133,"pbr":0,"pc":32768,"bytes":[133,156,0]}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := &RawEvent{Kind: "bus", Space: "wram", PC: &EventPC{Addr: 0x8000}, CPU: &EventCPU{}, Op: "write", Cycle: 15, Addr: 0x9c, Raw: []byte(tt.raw)}
			err := checkCoveredAccesses([]*RawInsn{insn}, BusEventList{e})
			if (err == nil) != tt.valid {
				t.Fatalf("got %v valid=%v", err, tt.valid)
			}
		})
	}
}
