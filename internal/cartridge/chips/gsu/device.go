package gsu

// VRAMWriter is the hook through which the pixel-cache commits a finished
// 8-byte tile row into the PPU's VRAM. The outer system binds this when it
// wires the cartridge up. PLOT itself buffers pixels locally; the flush is
// the only operation that touches VRAM.
//
// A ready-made *ppu.PPU-backed implementation lives at
// internal/cartridge/chips/gsu/ppuvram.Writer; until a cartridge layer
// installs it via SetVRAMWriter the cache commits fall through to the
// in-memory shadow log used by pixel_test.go.
type VRAMWriter interface {
	// WriteTileRow writes eight bytes starting at the VRAM word address
	// derived from CBR, the character-base row, and the horizontal tile.
	WriteTileRow(vramAddr uint16, row [8]byte)
}

type bitplaneVRAMWriter interface {
	WriteBitplaneByte(vramAddr uint16, val uint8)
}

// Device is the GSU core. Its zero value is usable but a call to Reset is
// required before running any instructions.
type Device struct {
	// R is the 16-register file. R15 is the program counter; writes to R15
	// cause an instruction fetch on the next step.
	R [16]uint16

	// SFR is the status/flag register. Its bit layout is documented in
	// sfr.go.
	SFR uint16

	// PBR is the program-bank register (used by LJMP and the ROM fetcher).
	PBR uint8
	// ROMBR is the ROM-bank register used by GETB/GETC/GETBH/GETBL/GETBS.
	ROMBR uint8
	// RAMBR is the RAM-bank register used by LDB/STB/LDW/STW.
	RAMBR uint8
	// CBR is the character-base register. The high 15 bits address the
	// current tile row in VRAM.
	CBR uint16
	// SCBR is the screen character base register; it selects the tile
	// screen's VRAM base when PLOT mode is >0.
	SCBR uint8
	// SCMR is the screen mode register. It selects HT layout, ROM/RAM
	// access enable bits, and pixel mode.
	SCMR uint8
	// BRAMR selects backup RAM behavior.
	BRAMR uint8
	// VCR is the version code register.
	VCR uint8
	// CFGR is the config register.
	CFGR uint8
	// CLSR selects the GSU clock rate.
	CLSR uint8
	// COLR is the colour register, loaded by the COLOR opcode.
	COLR uint8
	// POR is the plot-option register, loaded by CMODE.
	POR uint8

	// SREG selects the source register for the next WITH/TO/FROM
	// prefix-modified opcode. DREG selects the destination.
	SREG uint8
	DREG uint8
	// RAMAddr is the last RAM address used by a load/store instruction.
	RAMAddr uint16

	// withPrefix is true while a WITH Rn prefix is active (next opcode
	// reads and writes Rn via SREG/DREG).
	withPrefix bool
	// toPrefix and fromPrefix reflect the TO/FROM prefix bits; mutually
	// exclusive with withPrefix.
	toPrefix   bool
	fromPrefix bool
	// withReg remembers which register the WITH prefix selected. It is
	// inspected by the flush path to know which accumulator to treat as
	// primary.
	withReg uint8

	// RAM is the GSU's on-cartridge RAM. The default 64 KiB covers the
	// largest factory-shipped size; games with smaller packages mirror
	// within the same buffer.
	RAM []byte
	// ROM is a reference to the cartridge ROM backing store.
	ROM []byte

	// Cache is the 512-byte CPU-visible instruction cache window at
	// $3100-$32ff. CACHE execution is still modeled as a no-op; this
	// storage pins the bus-visible state first.
	Cache [512]uint8
	// cacheValid tracks the 32 cache lines populated by opcode fetch or
	// CPU writes through the cache window.
	cacheValid [32]bool

	// vram is the commit sink for the pixel cache. If nil the cache
	// flushes into vramShadow for test inspection.
	vram VRAMWriter
	// vramShadow records the most recent commit row (address plus bytes)
	// when vram is nil. Tests inspect it through ShadowCommits.
	vramShadow []shadowCommit
	// vramRows mirrors committed rows so RPIX can read pixels after the
	// plot cache has been flushed. The external VRAM writer remains the
	// authoritative sink for the rest of the system.
	vramRows map[uint16][8]byte

	// pixels is the 8-pixel horizontal cache that PLOT writes into.
	// validMask marks which cache slots have been plotted since the last
	// flush; a flush clears it.
	pixels    [8]uint8
	validMask uint8
	// cacheRow is the CBR-derived tile row currently resident in the
	// cache. It is set on the first PLOT and compared on each subsequent
	// PLOT; a change forces a flush.
	cacheRow    uint16
	cacheHasRow bool

	// commits counts how many times the cache has been flushed. Exposed
	// for deterministic testing of the "flush only on row change" quirk.
	commits uint32

	// cycles counts modeled GSU wait-state cycles.
	cycles uint64
	// stepBudget carries scheduler cycles that have not yet retired an
	// instruction. stepDebt carries instruction overrun paid by later Step calls.
	stepBudget uint64
	stepDebt   uint64

	ramPending bool
	ramDelay   uint64
	ramBank    uint8
	ramAddr    uint16
	ramData    uint8

	romPending bool
	romDelay   uint64
	romData    uint8

	// Pipeline holds the prefetched opcode/operand byte. Per
	// bsnes/sfc/coprocessor/superfx/memory.cpp:73-85, peekpipe() and
	// pipe() return regs.pipeline then refill it; R15 modifications
	// (jumps/branches/IWT R15) do NOT flush, producing the SuperFX's
	// hardware-accurate one-byte pipeline-delay behaviour. Cold reset
	// value is $01 (NOP) per bsnes/processor/gsu/gsu.cpp:34.
	Pipeline uint8

	// r15Modified is set whenever a handler writes R15 by means
	// other than the post-step ++ (branches, JMP/LJMP, LOOP, IWT R15,
	// any setReg(15,...) call). stepOne uses it to skip the post-
	// instruction ++. Mirrors bsnes regs.r[15].modified
	// (sfc/coprocessor/superfx/superfx.cpp main()).
	r15Modified bool

	// TraceHook, when non-nil, is invoked once per dispatched non-prefix
	// opcode immediately after the opcode byte is fetched, before
	// execution. pbr/pc reflect the address the opcode byte was read
	// from (PC has already been incremented past it). Diagnostic only;
	// must be nil in production paths.
	TraceHook func(pbr uint8, pc uint16, op uint8)

	// TraceHookEx, when non-nil, fires TWICE per retire so a probe
	// can split the per-retire cycle Δ into prefetch cost vs handler
	// (instruction body) cost. Phases:
	//
	//   TracePhasePrePeek:  fires at stepOne entry, BEFORE peekpipe.
	//                       cycles = d.cycles before the opcode-fetch
	//                       and any cache-fill work for THIS retire.
	//                       op is the byte already in the pipeline
	//                       (i.e., the byte that will retire this step;
	//                       same as what TraceHook reports).
	//
	//   TracePhasePostPeek: fires AFTER peekpipe (which refilled the
	//                       pipeline at R15) but BEFORE the instruction
	//                       handler runs. cycles = d.cycles including
	//                       any cache-fill cost incurred by this step's
	//                       prefetch refill.
	//
	// Per-retire decomposition:
	//   prefetch_N = cycles[postPeek N] - cycles[prePeek N]
	//   handler_N  = cycles[prePeek N+1] - cycles[postPeek N]
	//
	// Diagnostic only; must be nil in production paths.
	TraceHookEx func(phase TracePhase, pbr uint8, pc uint16, op uint8, cycles uint64)

	// RAMReadHook, when non-nil, is invoked after every GSU RAM bus
	// read (ramRead). Covers LDB/LDW and the indirect-store family's
	// pre-read path. Diagnostic only; nil in production paths.
	RAMReadHook func(addr uint32, val uint8)

	// ROMReadHook, when non-nil, is invoked after every GSU ROM bus
	// read returns to the ALU (romRead). Covers GETB/GETC/GETBH/
	// GETBL/GETBS. Does not fire for opcode fetch (use TraceHook for
	// that). Diagnostic only; nil in production paths.
	ROMReadHook func(addr uint32, val uint8)
}

// TracePhase distinguishes the two TraceHookEx fire points within a
// single retire. See Device.TraceHookEx for cycle-decomposition use.
type TracePhase uint8

const (
	TracePhasePrePeek  TracePhase = 0
	TracePhasePostPeek TracePhase = 1
)

// shadowCommit records one flush for tests when no VRAMWriter is bound.
type shadowCommit struct {
	Addr uint16
	Row  [8]byte
}

// New returns a reset GSU device with the provided RAM and ROM buffers.
// ram may be nil; if it is, a 64 KiB buffer is allocated.
func New(rom, ram []byte) *Device {
	if ram == nil {
		ram = make([]byte, 64*1024)
	}
	d := &Device{
		ROM: rom,
		RAM: ram,
	}
	d.Reset()
	return d
}

// Reset returns the GSU to power-on state.
func (d *Device) Reset() {
	for i := range d.R {
		d.R[i] = 0
	}
	d.SFR = 0
	d.PBR = 0
	d.ROMBR = 0
	d.RAMBR = 0
	d.CBR = 0
	d.SCBR = 0
	d.SCMR = 0
	d.BRAMR = 0
	d.VCR = 0x04
	d.CFGR = 0
	d.CLSR = 0
	d.COLR = 0
	d.POR = 0
	d.SREG = 0
	d.DREG = 0
	d.RAMAddr = 0
	// Pipeline cold-resets to $01 (NOP) per bsnes/processor/gsu/gsu.cpp:34
	// (regs.pipeline = 0x01). This makes the first dispatched byte a
	// harmless no-op rather than ROM[R15], matching hardware power-on.
	d.Pipeline = 0x01
	d.r15Modified = false
	clear(d.Cache[:])
	clear(d.cacheValid[:])
	d.withPrefix = false
	d.toPrefix = false
	d.fromPrefix = false
	d.withReg = 0
	d.validMask = 0
	d.cacheHasRow = false
	d.commits = 0
	d.cycles = 0
	d.stepBudget = 0
	d.stepDebt = 0
	d.ramPending = false
	d.ramDelay = 0
	d.ramBank = 0
	d.ramAddr = 0
	d.ramData = 0
	d.romPending = false
	d.romDelay = 0
	d.romData = 0
	d.vramShadow = d.vramShadow[:0]
	clear(d.vramRows)
}

// SetVRAMWriter installs the sink the pixel cache will flush into.
func (d *Device) SetVRAMWriter(w VRAMWriter) { d.vram = w }

// Running reports whether the GSU is executing (SFR.G set).
func (d *Device) Running() bool { return d.SFR&SFRG != 0 }

// OwnsRAM reports whether the running GSU has the shared RAM bus.
func (d *Device) OwnsRAM() bool { return d.Running() && d.SCMR&SCMRRAN != 0 }

// OwnsROM reports whether the running GSU has the ROM bus.
func (d *Device) OwnsROM() bool { return d.Running() && d.SCMR&SCMRRON != 0 }

// Go sets SFR.G so the next Step executes.
func (d *Device) Go() {
	d.stepBudget = 0
	d.stepDebt = 0
	d.SFR |= SFRG
}

// Stop clears SFR.G and retires any pending pixel cache by flushing it.
func (d *Device) Stop() {
	if d.CFGR&0x80 == 0 {
		d.SFR |= SFRIRQ
	}
	d.SFR &^= SFRG
	d.flushPixelCache()
}

// PC returns the current program counter (R15).
func (d *Device) PC() uint16 { return d.R[15] }

// SetPC updates R15. Test helper. Marks the R15 write as "modified"
// so the GSU's pipeline-aware stepOne treats this as a jump target
// (skips the post-step ++ and preserves the current pipeline byte).
// Tests that expect the post-write R15 to retire ROM[pc] should call
// PrimePipeline (or run a step) to refill the pipeline first.
func (d *Device) SetPC(pc uint16) { d.R[15] = pc; d.r15Modified = true }

// PrimePipeline forces the prefetch pipeline to load the byte at the
// current R15 immediately (without consuming any cycles in
// readOpcode's bus accounting). Test helper for unit tests that want
// to start dispatch from a clean post-jump state.
func (d *Device) PrimePipeline() {
	d.Pipeline = d.romAt((uint32(d.PBR) << 16) | uint32(d.R[15]))
	d.r15Modified = false
}

// Commits returns the number of pixel-cache flushes observed since the last
// Reset. Intended for testing the deferred-commit quirk.
func (d *Device) Commits() uint32 { return d.commits }

// Cycles returns modeled GSU wait-state cycles since Reset.
func (d *Device) Cycles() uint64 { return d.cycles }

// ShadowCommits returns a snapshot of the commit log when no VRAMWriter is
// installed. The slice is a copy; callers may retain it safely.
func (d *Device) ShadowCommits() []struct {
	Addr uint16
	Row  [8]byte
} {
	out := make([]struct {
		Addr uint16
		Row  [8]byte
	}, len(d.vramShadow))
	for i, c := range d.vramShadow {
		out[i].Addr = c.Addr
		out[i].Row = c.Row
	}
	return out
}
