package trace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cpu"
)

// obs builds an observation at pb:pc with the given entry P, E and
// fetches.
func obs(pb uint8, pc uint16, p uint8, e bool, fetches ...cpu.Fetch) cpu.Observation {
	o := cpu.Observation{
		Entry: cpu.Snapshot{PB: pb, PC: pc, P: p, E: e},
		Exit:  cpu.Snapshot{PB: pb, PC: pc + uint16(len(fetches)), P: p, E: e},
	}
	for i, f := range fetches {
		if i < cpu.MaxFetches {
			o.Fetches[i] = f
			o.NumFetches++
		} else {
			o.Overflow = true
		}
	}
	return o
}

func f(addr uint32, v uint8) cpu.Fetch { return cpu.Fetch{Addr: addr, Value: v} }

func TestNewInsn(t *testing.T) {
	tests := []struct {
		name       string
		o          cpu.Observation
		fault      error
		wantStatus string
		wantIssues []string
		wantLen    int
		wantSeq    PC
		wantSWI    string
	}{
		{
			name:       "retired 8-bit",
			o:          obs(0, 0x8000, 0x30, false, f(0x008000, 0xA9), f(0x008001, 0x12)),
			wantStatus: StatusRetired,
			wantLen:    2,
			wantSeq:    PC{0, 0x8002},
		},
		{
			name:       "retired emulation forces 8-bit",
			o:          obs(0, 0x8000, 0x00, true, f(0x008000, 0xA9), f(0x008001, 0x12)),
			wantStatus: StatusRetired,
			wantLen:    2,
			wantSeq:    PC{0, 0x8002},
		},
		{
			name:       "retired wrap within bank",
			o:          obs(0x01, 0xFFFF, 0x30, false, f(0x01FFFF, 0xA9), f(0x010000, 0x12)),
			wantStatus: StatusRetired,
			wantLen:    2,
			wantSeq:    PC{0x01, 0x0001},
		},
		{
			name:       "length mismatch",
			o:          obs(0, 0x8000, 0x00, false, f(0x008000, 0xA9), f(0x008001, 0x12)),
			wantStatus: StatusInvalid,
			wantIssues: []string{IssueLengthMismatch},
			wantLen:    3,
			wantSeq:    PC{0, 0x8003},
		},
		{
			name:       "fetch address mismatch across bank",
			o:          obs(0x01, 0xFFFF, 0x30, false, f(0x01FFFF, 0xA9), f(0x020000, 0x12)),
			wantStatus: StatusInvalid,
			wantIssues: []string{IssueFetchAddressMismatch},
			wantLen:    2,
			wantSeq:    PC{0x01, 0x0001},
		},
		{
			name: "overflow",
			o: obs(0, 0x8000, 0x30, false,
				f(0x008000, 0xA9), f(0x008001, 1), f(0x008002, 2), f(0x008003, 3), f(0x008004, 4)),
			wantStatus: StatusInvalid,
			wantIssues: []string{IssueFetchOverflow, IssueLengthMismatch},
			wantLen:    2,
			wantSeq:    PC{0, 0x8002},
		},
		{
			name:       "fault takes precedence over issues",
			o:          obs(0, 0x8000, 0x00, false, f(0x008000, 0xA9), f(0x018001, 0x12)),
			fault:      errors.New("boom"),
			wantStatus: StatusFaulted,
			wantIssues: []string{IssueLengthMismatch, IssueFetchAddressMismatch},
			wantLen:    3,
			wantSeq:    PC{0, 0x8003},
		},
		{
			name:       "brk",
			o:          obs(0, 0x8000, 0x30, false, f(0x008000, 0x00), f(0x008001, 0x42)),
			wantStatus: StatusRetired,
			wantLen:    2,
			wantSeq:    PC{0, 0x8002},
			wantSWI:    "brk",
		},
		{
			name:       "cop",
			o:          obs(0, 0x8000, 0x30, true, f(0x008000, 0x02), f(0x008001, 0x07)),
			wantStatus: StatusRetired,
			wantLen:    2,
			wantSeq:    PC{0, 0x8002},
			wantSWI:    "cop",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := tt.o
			o.Fault = tt.fault
			in := NewInsn(7, o, nil)
			if in.Seq != 7 {
				t.Fatalf("Seq = %d, want 7", in.Seq)
			}
			if in.Status != tt.wantStatus {
				t.Fatalf("Status = %q, want %q", in.Status, tt.wantStatus)
			}
			if !reflect.DeepEqual(in.Issues, tt.wantIssues) {
				t.Fatalf("Issues = %q, want %q", in.Issues, tt.wantIssues)
			}
			if in.Length != tt.wantLen {
				t.Fatalf("Length = %d, want %d", in.Length, tt.wantLen)
			}
			if in.SequentialPC != tt.wantSeq {
				t.Fatalf("SequentialPC = %v, want %v", in.SequentialPC, tt.wantSeq)
			}
			if in.SoftwareInterrupt != tt.wantSWI {
				t.Fatalf("SoftwareInterrupt = %q, want %q", in.SoftwareInterrupt, tt.wantSWI)
			}
			if tt.fault != nil && in.Fault != tt.fault.Error() {
				t.Fatalf("Fault = %q, want %q", in.Fault, tt.fault.Error())
			}
			if len(in.Fetches) != o.NumFetches {
				t.Fatalf("len(Fetches) = %d, want %d", len(in.Fetches), o.NumFetches)
			}
			for i, fr := range in.Fetches {
				want := RoleOperand
				if i == 0 {
					want = RoleOpcode
				}
				if fr.Role != want || fr.Addr != o.Fetches[i].Addr || fr.Value != o.Fetches[i].Value {
					t.Fatalf("fetch %d = %+v, want %+v role %s", i, fr, o.Fetches[i], want)
				}
				if fr.ROMOffset != nil {
					t.Fatalf("fetch %d has rom_offset with nil callback", i)
				}
			}
		})
	}
}

func TestNewInsnROMOffset(t *testing.T) {
	o := obs(0, 0x8000, 0x30, false, f(0x008000, 0xA9), f(0x008001, 0x12))
	romOffset := func(addr uint32) (uint32, bool) {
		if addr == 0x008000 {
			return 0, true
		}
		return 0, false
	}
	in := NewInsn(1, o, romOffset)
	if in.Fetches[0].ROMOffset == nil || *in.Fetches[0].ROMOffset != 0 {
		t.Fatalf("fetch 0 rom_offset = %v, want 0", in.Fetches[0].ROMOffset)
	}
	if in.Fetches[1].ROMOffset != nil {
		t.Fatalf("fetch 1 rom_offset = %d, want absent", *in.Fetches[1].ROMOffset)
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"rom_offset":0`); n != 1 {
		t.Fatalf("encoded rom_offset count = %d, want 1: %s", n, data)
	}
}

// flat is 16 MiB of plain memory.
type flat struct{ b []uint8 }

func (m *flat) Read(a uint32) uint8              { return m.b[a&0xFFFFFF] }
func (m *flat) Write(a uint32, v uint8)          { m.b[a&0xFFFFFF] = v }
func (m *flat) BlockRead(a uint32, n int) []byte { return m.b[a : a+uint32(n)] }

type collect struct {
	insns []cpu.Observation
	trans []cpu.Transition
}

func (c *collect) ObserveInstruction(o cpu.Observation) { c.insns = append(c.insns, o) }
func (c *collect) ObserveTransition(t cpu.Transition)   { c.trans = append(c.trans, t) }

// TestNewInsnFromCPU checks records built from real CPU observations of
// software interrupts and a faulting dispatch.
func TestNewInsnFromCPU(t *testing.T) {
	tests := []struct {
		name       string
		e          bool
		prog       []uint8
		clear      int // opcode to make unimplemented, or -1
		wantStatus string
		wantSWI    string
		wantLen    int
	}{
		{"brk native", false, []uint8{0x00, 0x11}, -1, StatusRetired, "brk", 2},
		{"cop native", false, []uint8{0x02, 0x22}, -1, StatusRetired, "cop", 2},
		{"brk emulation", true, []uint8{0x00, 0x33}, -1, StatusRetired, "brk", 2},
		{"cop emulation", true, []uint8{0x02, 0x44}, -1, StatusRetired, "cop", 2},
		{"fault", false, []uint8{0x42, 0x00}, 0x42, StatusFaulted, "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.clear >= 0 {
				saved := cpu.Opcodes[tt.clear]
				cpu.Opcodes[tt.clear].Op = nil
				t.Cleanup(func() { cpu.Opcodes[tt.clear] = saved })
			}
			b := bus.NewBus()
			m := &flat{b: make([]uint8, 1<<24)}
			b.Map(0, 0xFFFFFF, m)
			copy(m.b[0x8000:], tt.prog)
			c := cpu.NewCPU(b)
			c.E, c.P, c.PC, c.S = tt.e, 0x34, 0x8000, 0x01FF
			var col collect
			if _, err := c.Observe(&col); err != nil {
				t.Fatal(err)
			}
			c.Step()
			if len(col.insns) != 1 || len(col.trans) != 0 {
				t.Fatalf("got %d observations, %d transitions; want 1, 0", len(col.insns), len(col.trans))
			}
			in := NewInsn(1, col.insns[0], nil)
			if in.Status != tt.wantStatus || in.SoftwareInterrupt != tt.wantSWI || in.Length != tt.wantLen {
				t.Fatalf("status=%q swi=%q len=%d, want %q %q %d", in.Status, in.SoftwareInterrupt, in.Length, tt.wantStatus, tt.wantSWI, tt.wantLen)
			}
			if tt.wantStatus == StatusRetired && (len(in.Fetches) != 2 || in.Fetches[1].Value != tt.prog[1]) {
				t.Fatalf("fetches = %+v, want opcode and signature %02X", in.Fetches, tt.prog[1])
			}
			if tt.wantStatus == StatusFaulted && in.Fault == "" {
				t.Fatalf("faulted record has no fault text")
			}
		})
	}
}

func TestNewTransition(t *testing.T) {
	tests := []struct {
		name        string
		tr          cpu.Transition
		wantKind    string
		wantVector  bool
		wantHandler PC
	}{
		{
			name:        "nmi",
			tr:          cpu.Transition{Kind: cpu.TransitionNMI, Vector: 0xFFEA, After: cpu.Snapshot{PB: 0, PC: 0x8100}},
			wantKind:    "nmi",
			wantVector:  true,
			wantHandler: PC{0, 0x8100},
		},
		{
			name:        "irq from wait",
			tr:          cpu.Transition{Kind: cpu.TransitionIRQ, Vector: 0xFFFE, FromWait: true, After: cpu.Snapshot{PC: 0x9000}},
			wantKind:    "irq",
			wantVector:  true,
			wantHandler: PC{0, 0x9000},
		},
		{name: "wake", tr: cpu.Transition{Kind: cpu.TransitionWake, FromWait: true}, wantKind: "wake"},
		{name: "reset", tr: cpu.Transition{Kind: cpu.TransitionReset, Vector: 0xFFFC}, wantKind: "reset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewTransition(3, tt.tr)
			if r.Seq != 3 || r.Kind != tt.wantKind || r.FromWait != tt.tr.FromWait {
				t.Fatalf("got %+v, want seq 3 kind %q from_wait %v", r, tt.wantKind, tt.tr.FromWait)
			}
			if (r.VectorAddr != nil) != tt.wantVector || (r.HandlerPC != nil) != tt.wantVector {
				t.Fatalf("vector=%v handler=%v, want present=%v", r.VectorAddr, r.HandlerPC, tt.wantVector)
			}
			if tt.wantVector && (*r.VectorAddr != tt.tr.Vector || *r.HandlerPC != tt.wantHandler) {
				t.Fatalf("vector=%X handler=%v, want %X %v", *r.VectorAddr, *r.HandlerPC, tt.tr.Vector, tt.wantHandler)
			}
		})
	}
}

// rec is a record summary: kind and seq, or first/last seq for a gap.
type rec struct {
	kind        string
	seq, first  uint64
	last        uint64
	transKind   string
	entryPCAddr uint16
}

func summarize(t *testing.T, data []byte) []rec {
	t.Helper()
	events, err := DecodeStrict(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	var out []rec
	for i, e := range events {
		if e.ID != uint64(i) {
			t.Fatalf("event %d has id %d", i, e.ID)
		}
		switch e.Kind {
		case "cpu_insn":
			out = append(out, rec{kind: e.Kind, seq: e.Insn.Seq, entryPCAddr: e.Insn.Entry.PC})
		case "cpu_transition":
			out = append(out, rec{kind: e.Kind, seq: e.Transition.Seq, transKind: e.Transition.Kind})
		case "gap":
			out = append(out, rec{kind: e.Kind, first: e.Gap.FirstSeq, last: e.Gap.LastSeq})
		default:
			t.Fatalf("unexpected kind %q", e.Kind)
		}
	}
	return out
}

func insnAt(pc uint16) cpu.Observation {
	return obs(0, pc, 0x30, false, f(uint32(pc), 0xEA))
}

func TestRecorderFilter(t *testing.T) {
	type input struct {
		pc    uint16 // instruction entry PC; 0 means a transition
		trans cpu.TransitionKind
	}
	tests := []struct {
		name         string
		keep         func(pb uint8, pc uint16) bool
		instructions bool
		transitions  bool
		in           []input
		want         []rec
	}{
		{
			name:         "keep 8000",
			keep:         func(_ uint8, pc uint16) bool { return pc == 0x8000 },
			instructions: true,
			transitions:  true,
			in: []input{
				{pc: 0x8000}, {pc: 0x8001}, {pc: 0x8002}, {trans: cpu.TransitionNMI},
				{pc: 0x8003}, {pc: 0x8000}, {pc: 0x8004},
			},
			want: []rec{
				{kind: "cpu_insn", seq: 1, entryPCAddr: 0x8000},
				{kind: "gap", first: 2, last: 3},
				{kind: "cpu_transition", seq: 4, transKind: "nmi"},
				{kind: "gap", first: 5, last: 5},
				{kind: "cpu_insn", seq: 6, entryPCAddr: 0x8000},
				{kind: "gap", first: 7, last: 7},
			},
		},
		{
			name:         "no filter",
			instructions: true,
			transitions:  true,
			in:           []input{{pc: 0x8000}, {trans: cpu.TransitionIRQ}, {pc: 0x8001}},
			want: []rec{
				{kind: "cpu_insn", seq: 1, entryPCAddr: 0x8000},
				{kind: "cpu_transition", seq: 2, transKind: "irq"},
				{kind: "cpu_insn", seq: 3, entryPCAddr: 0x8001},
			},
		},
		{
			name:         "transitions only",
			instructions: false,
			transitions:  true,
			in:           []input{{pc: 0x8000}, {pc: 0x8001}, {trans: cpu.TransitionWake}, {pc: 0x8002}},
			want: []rec{
				{kind: "gap", first: 1, last: 2},
				{kind: "cpu_transition", seq: 3, transKind: "wake"},
				{kind: "gap", first: 4, last: 4},
			},
		},
		{
			name:         "instructions only",
			instructions: true,
			transitions:  false,
			in:           []input{{trans: cpu.TransitionReset}, {pc: 0x8000}},
			want: []rec{
				{kind: "gap", first: 1, last: 1},
				{kind: "cpu_insn", seq: 2, entryPCAddr: 0x8000},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRecorder(NewWriter(&buf))
			r.Keep = tt.keep
			r.Instructions, r.Transitions = tt.instructions, tt.transitions
			for _, in := range tt.in {
				if in.pc == 0 {
					r.ObserveTransition(cpu.Transition{Kind: in.trans})
				} else {
					r.ObserveInstruction(insnAt(in.pc))
				}
			}
			if err := r.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if got := r.LastSeq(); got != uint64(len(tt.in)) {
				t.Fatalf("LastSeq = %d, want %d", got, len(tt.in))
			}
			got := summarize(t, buf.Bytes())
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("records:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestRecorderLimits(t *testing.T) {
	// Measure one record to size the byte limit.
	var one bytes.Buffer
	NewRecorder(NewWriter(&one)).ObserveInstruction(insnAt(0x8000))
	size := one.Len()

	tests := []struct {
		name       string
		setup      func(w *Writer)
		keep       func(uint8, uint16) bool
		wantEvents int
		wantReason string
	}{
		{"event limit", func(w *Writer) { w.SetLimit(3) }, nil, 3, "event_limit"},
		{"byte limit", func(w *Writer) { w.SetByteLimit(2*size + size/2) }, nil, 2, "byte_limit"},
		{
			// A gap record must not be written past the limit either.
			"event limit with filter",
			func(w *Writer) { w.SetLimit(2) },
			func(_ uint8, pc uint16) bool { return pc%2 == 0 },
			2, "event_limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			tt.setup(w)
			r := NewRecorder(w)
			r.Keep = tt.keep
			n := 0
			for pc := uint16(0x8000); pc < 0x8010; pc++ {
				r.ObserveInstruction(insnAt(pc))
				if w.Truncated() && n == 0 {
					n = buf.Len()
				}
			}
			r.ObserveTransition(cpu.Transition{Kind: cpu.TransitionNMI})
			if err := r.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if !w.Truncated() || w.TruncationReason() != tt.wantReason {
				t.Fatalf("Truncated=%v reason=%q, want %q", w.Truncated(), w.TruncationReason(), tt.wantReason)
			}
			if buf.Len() != n {
				t.Fatalf("wrote %d bytes after truncation", buf.Len()-n)
			}
			events, err := DecodeStrict(&buf)
			if err != nil {
				t.Fatalf("DecodeStrict: %v", err)
			}
			if len(events) != tt.wantEvents {
				t.Fatalf("events = %d, want %d", len(events), tt.wantEvents)
			}
			if r.LastSeq() != 17 {
				t.Fatalf("LastSeq = %d, want 17", r.LastSeq())
			}
		})
	}
}

// failWriter accepts ok writes and then fails every write with err,
// writing short bytes of the failing record.
type failWriter struct {
	ok     int
	short  int
	err    error
	writes int
	buf    bytes.Buffer
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.ok > 0 {
		w.ok--
		return w.buf.Write(p)
	}
	n := min(w.short, len(p))
	w.buf.Write(p[:n])
	return n, w.err
}

func TestRecorderWriterFailure(t *testing.T) {
	errDisk := errors.New("disk full")
	tests := []struct {
		name  string
		ok    int
		short int
		err   error
	}{
		{"error on first write", 0, 0, errDisk},
		{"error after two writes", 2, 0, errDisk},
		{"short write", 1, 10, io.ErrShortWrite},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fw := &failWriter{ok: tt.ok, short: tt.short, err: tt.err}
			w := NewWriter(fw)
			r := NewRecorder(w)
			r.ObserveTransition(cpu.Transition{Kind: cpu.TransitionReset})
			for pc := uint16(0x8000); pc < 0x8008; pc++ {
				r.ObserveInstruction(insnAt(pc))
			}
			if !errors.Is(r.Err(), tt.err) {
				t.Fatalf("Err = %v, want %v", r.Err(), tt.err)
			}
			if fw.writes != tt.ok+1 {
				t.Fatalf("writes = %d, want %d", fw.writes, tt.ok+1)
			}
			n := fw.buf.Len()
			r.ObserveTransition(cpu.Transition{Kind: cpu.TransitionNMI})
			if err := r.Flush(); !errors.Is(err, tt.err) {
				t.Fatalf("Flush = %v, want %v", err, tt.err)
			}
			if err := w.Emit(Event{Kind: "x"}); !errors.Is(err, tt.err) {
				t.Fatalf("Emit after failure = %v, want %v", err, tt.err)
			}
			if fw.writes != tt.ok+1 || fw.buf.Len() != n {
				t.Fatalf("wrote after failure: writes=%d bytes %d->%d", fw.writes, n, fw.buf.Len())
			}
			if r.LastSeq() != 10 {
				t.Fatalf("LastSeq = %d, want 10", r.LastSeq())
			}
		})
	}
}

func TestWriteReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipt.json")
	want := Receipt{
		Schema:           1, // overwritten
		Outcome:          OutcomeLimit,
		LastSeq:          42,
		EventCount:       40,
		StreamSHA256:     strings.Repeat("ab", 32),
		TruncationReason: "byte_limit",
		Error:            "",
	}
	if err := WriteReceipt(path, want); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Receipt
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	want.Schema = SchemaVersion
	if got != want {
		t.Fatalf("receipt = %+v, want %+v", got, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want only the receipt", len(entries))
	}
	if err := WriteReceipt(filepath.Join(dir, "missing", "r.json"), want); err == nil {
		t.Fatalf("WriteReceipt into missing dir succeeded")
	}
}

func TestDecodeStrict(t *testing.T) {
	var cur bytes.Buffer
	w := NewWriter(&cur)
	if err := w.Emit(Event{Kind: "run"}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"current", cur.String(), false},
		{"schema 1", `{"id":0,"schema":1,"kind":"cpu_step"}` + "\n", true},
		{"missing schema", `{"id":0,"kind":"cpu_step"}` + "\n", true},
		{"mixed", cur.String() + `{"id":1,"schema":1,"kind":"cpu_step"}` + "\n", true},
		{"malformed", `{"id":0,`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeStrict(strings.NewReader(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
