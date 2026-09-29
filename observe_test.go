package snes

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/trace"
)

// observeTestROM returns a 32 KiB LoROM image whose program switches to
// native mode, jumps to the $80 bank mirror, copies a subroutine from
// ROM into WRAM with MVN, enables NMI and then loops: WAI, WRAM and PPU
// register traffic (including reads with side effects), and a JSL into
// the WRAM copy. The NMI handler acknowledges $4210 and counts frames
// at $0200.
func observeTestROM() []byte {
	rom := make([]byte, 0x8000)
	put := func(addr uint16, b ...byte) { copy(rom[addr-0x8000:], b) }
	put(0x8000,
		0x78,                   // SEI
		0x18,                   // CLC
		0xFB,                   // XCE
		0x5C, 0x07, 0x80, 0x80, // JML $808007
	)
	put(0x8007,
		0xC2, 0x30, // REP #$30
		0xA2, 0xFF, 0x1F, // LDX #$1FFF
		0x9A,             // TXS
		0xA9, 0x03, 0x00, // LDA #$0003
		0xA2, 0x00, 0x90, // LDX #$9000
		0xA0, 0x00, 0x03, // LDY #$0300
		0x54, 0x7E, 0x00, // MVN $7E,$00
		0xE2, 0x30, // SEP #$30
		0x4B,       // PHK
		0xAB,       // PLB
		0xA9, 0x0F, // LDA #$0F
		0x8D, 0x00, 0x21, // STA $2100
		0xA9, 0x81, // LDA #$81
		0x8D, 0x00, 0x42, // STA $4200
	)
	put(0x8027,
		0xCB,       // loop: WAI
		0xE6, 0x10, // INC $10
		0xA5, 0x10, // LDA $10
		0x9C, 0x21, 0x21, // STZ $2121
		0x8D, 0x22, 0x21, // STA $2122
		0x8D, 0x22, 0x21, // STA $2122
		0xAD, 0x37, 0x21, // LDA $2137
		0xAD, 0x3C, 0x21, // LDA $213C
		0x22, 0x00, 0x03, 0x7E, // JSL $7E0300
		0x80, 0xE6, // BRA loop
	)
	put(0x8100, // NMI
		0x48,             // PHA
		0xAD, 0x10, 0x42, // LDA $4210
		0xEE, 0x00, 0x02, // INC $0200
		0x68, // PLA
		0x40, // RTI
	)
	put(0x9000, 0xEA, 0xEA, 0xEA, 0x6B) // NOP NOP NOP RTL, copied to $7E:0300
	rom[0x7FD5] = 0x20                  // LoROM
	put(0xFFEA, 0x00, 0x81)             // native NMI
	put(0xFFFA, 0x00, 0x81)             // emulation NMI
	put(0xFFFC, 0x00, 0x80)             // reset
	return rom
}

// obsSink hashes a trace stream and checks each cpu_insn record's
// status without decoding it. Writer emits one record per Write.
type obsSink struct {
	h       hash.Hash
	n       int
	keep    bool
	buf     bytes.Buffer
	insns   int
	bad     int
	badLine []byte
}

func newObsSink(keep bool) *obsSink { return &obsSink{h: sha256.New(), keep: keep} }

var (
	obsKindInsn  = []byte(`"kind":"cpu_insn"`)
	obsRetired   = []byte(`"status":"retired"`)
	obsStatusKey = []byte(`"status":`)
)

func (s *obsSink) Write(p []byte) (int, error) {
	s.h.Write(p)
	s.n += len(p)
	if s.keep {
		s.buf.Write(p)
	}
	if bytes.Contains(p, obsKindInsn) {
		s.insns++
		if !bytes.Contains(p, obsRetired) || bytes.Count(p, obsStatusKey) != 1 {
			s.bad++
			if s.badLine == nil {
				s.badLine = append([]byte(nil), p...)
			}
		}
	}
	return len(p), nil
}

// obsTee forwards to a Recorder and independently checks the ROM
// provenance of every fetch.
type obsTee struct {
	rec      *trace.Recorder
	sys      *System
	lorom    bool // ROMProvenance reported plain LoROM
	insns    int
	trans    map[cpu.TransitionKind]int
	banks    map[uint8]int
	romFetch int
	ramFetch int
	offset0  bool
	errs     []string
}

func (o *obsTee) errorf(format string, args ...any) {
	if len(o.errs) < 10 {
		o.errs = append(o.errs, fmt.Sprintf(format, args...))
	}
}

func (o *obsTee) ObserveInstruction(ob cpu.Observation) {
	o.rec.ObserveInstruction(ob)
	o.insns++
	for i := 0; i < ob.NumFetches; i++ {
		a := ob.Fetches[i].Addr
		bank, addr := uint8(a>>16), uint16(a)
		o.banks[bank]++
		off, ok := o.sys.ROMAddress(a)
		wram := bank == 0x7E || bank == 0x7F || (bank&0x40 == 0 && addr < 0x2000)
		switch {
		case wram:
			o.ramFetch++
			if ok {
				o.errorf("WRAM fetch %06X has rom_offset %X", a, off)
			}
		case o.lorom && bank&0x7F < 0x7E && addr >= 0x8000:
			o.romFetch++
			if !ok {
				o.errorf("LoROM fetch %06X has no rom_offset", a)
			} else if want := uint32(bank&0x7F)<<15 | uint32(addr&0x7FFF); off != want%uint32(len(o.sys.cart.ROM)) {
				o.errorf("LoROM fetch %06X rom_offset = %X, want %X", a, off, want)
			}
		}
		if ok && off == 0 {
			o.offset0 = true
		}
	}
}

func (o *obsTee) ObserveTransition(t cpu.Transition) {
	o.rec.ObserveTransition(t)
	o.trans[t.Kind]++
}

// obsRun is the result of one traced run.
type obsRun struct {
	sum    [32]byte
	n      int
	stream []byte
	tee    *obsTee
	sink   *obsSink
	traced *System
}

// runObserved runs rom for frames frames with a trace.Recorder attached.
// If compare is set, it also runs an untraced system alongside and
// requires the two to be identical after power-on and after every
// frame.
//
// The comparison covers the full serialized state (CPU, bus MDR and
// MEMSEL, WRAM, PPU, APU, DMA, scheduler, input, cartridge), the
// component hashes of StateHashes (which add VRAM, CGRAM, OAM and the
// framebuffer), CPU cycles, the front buffer, and the drained audio
// samples. Nothing is excluded: observer fields on cpu.CPU are
// unexported and not part of cpu.CPUState.
func runObserved(t *testing.T, rom []byte, frames int, compare, keep bool) obsRun {
	t.Helper()
	traced := NewSystem(nil)
	if err := traced.LoadROM(rom); err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	var plain *System
	if compare {
		plain = NewSystem(nil)
		if err := plain.LoadROM(rom); err != nil {
			t.Fatalf("LoadROM: %v", err)
		}
	}
	mapper, ok := traced.ROMProvenance()
	sink := newObsSink(keep)
	w := trace.NewWriter(sink)
	rec := trace.NewRecorder(w)
	frame := 0
	rec.Frame = func() int { return frame }
	rec.ROMOffset = traced.ROMAddress
	tee := &obsTee{
		rec: rec, sys: traced, lorom: ok && mapper == "lorom",
		trans: map[cpu.TransitionKind]int{}, banks: map[uint8]int{},
	}
	detach, err := traced.CPU.Observe(tee)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	defer detach()

	traced.Power()
	if compare {
		plain.Power()
	}
	audioA := make([]int16, 1<<14)
	audioB := make([]int16, 1<<14)
	check := func() {
		t.Helper()
		sa, err := plain.Serialize()
		if err != nil {
			t.Fatalf("frame %d: Serialize: %v", frame, err)
		}
		sb, err := traced.Serialize()
		if err != nil {
			t.Fatalf("frame %d: Serialize: %v", frame, err)
		}
		ha, err := plain.StateHashes()
		if err != nil {
			t.Fatalf("frame %d: StateHashes: %v", frame, err)
		}
		hb, err := traced.StateHashes()
		if err != nil {
			t.Fatalf("frame %d: StateHashes: %v", frame, err)
		}
		if !reflect.DeepEqual(ha, hb) {
			for k := range ha {
				if ha[k] != hb[k] {
					t.Errorf("frame %d: %s hash differs", frame, k)
				}
			}
			t.FailNow()
		}
		if !bytes.Equal(sa, sb) {
			t.Fatalf("frame %d: serialized state differs (%d vs %d bytes)", frame, len(sa), len(sb))
		}
		if plain.CPU.Cycles != traced.CPU.Cycles {
			t.Fatalf("frame %d: CPU cycles %d untraced, %d traced", frame, plain.CPU.Cycles, traced.CPU.Cycles)
		}
		if !slices.Equal(plain.FrameBuffer(), traced.FrameBuffer()) {
			t.Fatalf("frame %d: framebuffer differs", frame)
		}
		for {
			na := plain.DrainAudio(audioA)
			nb := traced.DrainAudio(audioB)
			if na != nb || !slices.Equal(audioA[:na], audioB[:nb]) {
				t.Fatalf("frame %d: audio differs (%d vs %d samples)", frame, na, nb)
			}
			if na == 0 {
				break
			}
		}
	}
	if compare {
		check()
	}
	for frame = 1; frame <= frames; frame++ {
		if err := traced.RunFrame(); err != nil {
			t.Fatalf("frame %d: RunFrame: %v", frame, err)
		}
		if compare {
			if err := plain.RunFrame(); err != nil {
				t.Fatalf("frame %d: RunFrame: %v", frame, err)
			}
			check()
		} else {
			for traced.DrainAudio(audioB) != 0 {
			}
		}
		if err := rec.Err(); err != nil {
			t.Fatalf("frame %d: recorder: %v", frame, err)
		}
	}
	if err := rec.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if w.Truncated() {
		t.Fatalf("stream truncated: %s", w.TruncationReason())
	}
	if traced.CPU.Fault != nil {
		t.Fatalf("CPU fault: %v", traced.CPU.Fault)
	}
	if sink.insns != tee.insns {
		t.Fatalf("stream has %d cpu_insn records, observer saw %d", sink.insns, tee.insns)
	}
	if sink.bad != 0 {
		t.Fatalf("%d cpu_insn records not retired; first: %s", sink.bad, sink.badLine)
	}
	for _, e := range tee.errs {
		t.Error(e)
	}
	var r obsRun
	copy(r.sum[:], sink.h.Sum(nil))
	r.n, r.stream, r.tee, r.sink, r.traced = sink.n, sink.buf.Bytes(), tee, sink, traced
	return r
}

func TestObservationInvarianceSynthetic(t *testing.T) {
	rom := observeTestROM()
	const frames = 120
	r := runObserved(t, rom, frames, true, true)

	if mapper, ok := r.traced.ROMProvenance(); mapper != "lorom" || !ok {
		t.Fatalf("ROMProvenance = %q, %v; want lorom, true", mapper, ok)
	}
	if got, want := r.traced.ROMSHA256(), sha256.Sum256(rom); got != want {
		t.Fatalf("ROMSHA256 = %x, want %x", got, want)
	}

	// The program must actually have run its loop and NMI handler.
	var wram [0x201]byte
	if _, err := r.traced.ReadWRAMAt(wram[:], 0); err != nil {
		t.Fatal(err)
	}
	if wram[0x10] < frames/2 || wram[0x200] < frames/2 {
		t.Fatalf("loop count %d, NMI count %d; want >= %d", wram[0x10], wram[0x200], frames/2)
	}
	tee := r.tee
	if tee.trans[cpu.TransitionReset] != 1 || tee.trans[cpu.TransitionNMI] < frames/2 {
		t.Fatalf("transitions = %v, want 1 reset and >= %d nmi", tee.trans, frames/2)
	}
	for _, bank := range []uint8{0x00, 0x80, 0x7E} {
		if tee.banks[bank] == 0 {
			t.Fatalf("no fetches from bank %02X; banks = %v", bank, tee.banks)
		}
	}
	if !tee.offset0 {
		t.Fatalf("no fetch mapped to ROM offset 0")
	}

	// Check the records themselves.
	events, err := trace.DecodeStrict(bytes.NewReader(r.stream))
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	var lastSeq uint64
	var insns, withOffset, wramFetches, zero int
	for _, e := range events {
		var seq uint64
		switch e.Kind {
		case "cpu_insn":
			in := e.Insn
			seq = in.Seq
			insns++
			if in.Status != trace.StatusRetired || len(in.Issues) != 0 {
				t.Fatalf("insn %d at %v: status %q issues %v", in.Seq, in.Entry.PC, in.Status, in.Issues)
			}
			for _, f := range in.Fetches {
				switch bank := uint8(f.Addr >> 16); {
				case bank == 0x7E:
					wramFetches++
					if f.ROMOffset != nil {
						t.Fatalf("WRAM fetch %06X has rom_offset %X", f.Addr, *f.ROMOffset)
					}
				case f.Addr&0x8000 != 0:
					if f.ROMOffset == nil || *f.ROMOffset != f.Addr&0x7FFF {
						t.Fatalf("ROM fetch %06X rom_offset = %v, want %X", f.Addr, f.ROMOffset, f.Addr&0x7FFF)
					}
					withOffset++
					if *f.ROMOffset == 0 {
						zero++
					}
				}
			}
		case "cpu_transition":
			seq = e.Transition.Seq
		default:
			t.Fatalf("unexpected record kind %q", e.Kind)
		}
		if seq != lastSeq+1 {
			t.Fatalf("seq %d follows %d", seq, lastSeq)
		}
		lastSeq = seq
	}
	if insns == 0 || withOffset == 0 || wramFetches == 0 || zero == 0 {
		t.Fatalf("insns=%d romFetches=%d wramFetches=%d offset0=%d; want all > 0", insns, withOffset, wramFetches, zero)
	}

	// Determinism: a second traced run writes the same bytes.
	r2 := runObserved(t, rom, frames, false, false)
	if r.sum != r2.sum || r.n != r2.n {
		t.Fatalf("traced runs differ: %d bytes %x vs %d bytes %x", r.n, r.sum, r2.n, r2.sum)
	}
}

// TestObservationInvarianceROM repeats the invariance check on the ROM
// named by SNES_OBS_ROM.
func TestObservationInvarianceROM(t *testing.T) {
	path := os.Getenv("SNES_OBS_ROM")
	if path == "" {
		t.Skip("SNES_OBS_ROM not set")
	}
	rom, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const frames = 300
	start := time.Now()
	r := runObserved(t, rom, frames, true, false)
	t.Logf("compared run: %v, %d insns, %d stream bytes, transitions %v", time.Since(start), r.tee.insns, r.n, r.tee.trans)
	mapper, ok := r.traced.ROMProvenance()
	t.Logf("rom %x mapper %s provenance %v; ROM fetches %d, WRAM fetches %d", r.traced.ROMSHA256(), mapper, ok, r.tee.romFetch, r.tee.ramFetch)
	if ok {
		if r.tee.romFetch == 0 || !r.tee.offset0 {
			t.Fatalf("ROM fetches %d, offset 0 seen %v; want both", r.tee.romFetch, r.tee.offset0)
		}
	}

	start = time.Now()
	r2 := runObserved(t, rom, frames, false, false)
	t.Logf("second traced run: %v", time.Since(start))
	if r.sum != r2.sum || r.n != r2.n {
		t.Fatalf("traced runs differ: %d bytes %x vs %d bytes %x", r.n, r.sum, r2.n, r2.sum)
	}
}

func TestROMProvenance(t *testing.T) {
	hirom := make([]byte, 0x10000)
	hirom[0xFFD5] = 0x21
	hirom[0x7FD5] = 0x21 // a zero LoROM map byte would outscore HiROM
	hirom[0xFFFC], hirom[0xFFFD] = 0x00, 0x80
	tests := []struct {
		name       string
		rom        []byte
		wantMapper string
		wantOK     bool
	}{
		{"lorom", observeTestROM(), "lorom", true},
		{"hirom", hirom, "hirom", false},
		{"lorom with gsu", newGSUTestROM(), "lorom+gsu", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := NewSystem(nil)
			if mapper, ok := sys.ROMProvenance(); mapper != "" || ok {
				t.Fatalf("unloaded ROMProvenance = %q, %v; want \"\", false", mapper, ok)
			}
			if err := sys.LoadROM(tt.rom); err != nil {
				t.Fatalf("LoadROM: %v", err)
			}
			mapper, ok := sys.ROMProvenance()
			if mapper != tt.wantMapper || ok != tt.wantOK {
				t.Fatalf("ROMProvenance = %q, %v; want %q, %v", mapper, ok, tt.wantMapper, tt.wantOK)
			}
		})
	}
}
