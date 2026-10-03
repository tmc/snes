package ppu

const (
	VRAMSize  = 64 * 1024 // 64KB (32K Words)
	OAMSize   = 544       // 512 bytes table + 32 bytes high table
	CGRAMSize = 512       // 256 colors * 2 bytes

	ntscVPeriod       = 262
	palVPeriod        = 312
	ntscHPeriod       = 1364
	ntscShortHPeriod  = 1360
	ntscShortScanline = 240
)

type PPU struct {
	VRAM  [VRAMSize]uint8
	OAM   [OAMSize]uint8
	CGRAM [CGRAMSize]uint8

	PPURegisters

	// DMA Controller reference for HDMA
	DMA interface {
		RequestHDMA(at uint64, setup bool)
	}

	// Output
	FrontBuffer      []uint16
	hiresFrontBuffer []uint16
	Width            int
	Height           int

	// Mode7LatchHook, if non-nil, is called after writes latch a Mode 7
	// scroll, matrix, or center register.
	Mode7LatchHook func(addr uint16, value uint16)

	// Mode7LatchEventHook, if non-nil, is called with beam timing after
	// writes latch a Mode 7 scroll, matrix, or center register.
	Mode7LatchEventHook func(Mode7LatchEvent)

	// Mode7MatrixPairHook, if non-nil, is called after writes latch M7A/M7B
	// or M7C/M7D in the order HDMA mode 3 uses for paired matrix updates.
	Mode7MatrixPairHook func(Mode7MatrixPairEvent)

	// Mode7ScanlineHook, if non-nil, is called before rendering a Mode 7
	// scanline with the matrix state and representative dot coordinates.
	Mode7ScanlineHook func(Mode7ScanlineEvent)

	// WriteHook, if non-nil, is called after direct VRAM, OAM, or CGRAM
	// storage mutations. It is intended for diagnostics and should remain nil
	// on the hot path.
	WriteHook func(WriteEvent)

	// FrameHook, if non-nil, is called once per frame when the beam
	// first enters vertical blank, after the last visible line has
	// been rendered. The FrameInfo is valid only during the call;
	// the hook may call CopyFrame but must not otherwise use the PPU.
	FrameHook func(*FrameInfo)

	// Per-frame capture state; see capture.go.
	frame        FrameInfo
	frameStarted bool
	frameDone    bool

	// Internal State
	cycles          uint64
	beamFrameStart  uint64
	beamVBlankStart uint64
	vblankActive    bool
	FrameCount      int
	hCounter        int
	vCounter        int
	palTiming       bool
	ppuField        bool
	ppuInterlace    bool
	vPeriod         int
	hPeriod         int
	NMIFlag         bool // $4210 Bit 7
	nmiHold         uint8
	AutoJoypad      bool
	RangeOver       bool
	TimeOver        bool
	latchedH        uint16
	latchedV        uint16
	lastM7Pair      Mode7MatrixPairEvent
	m7PairValid     [0x22]bool
	hReadHigh       bool
	vReadHigh       bool
	hvLatched       bool

	// Reusable per-scanline scratch buffers to keep the hot path allocation-free.
	mainSource  [512]uint8
	subLine     [512]uint16
	objColor    [512]uint16
	objPrio     [512]int
	objSource   [512]uint8
	objPalette  [512]uint8
	objOrder    [512]int
	lineSprites [32]spriteLine

	// Phase 2.5 pixel-walk renderer scratch. See
	// docs/planning/phase2.5-pixel-walk.md. Allocated unconditionally so
	// hi-res Modes 5/6 don't pay a per-scanline branch for buffer sizing.
	pwAbove    [512]layerPixel
	pwBelow    [512]layerPixel
	pwAbovePal [512]uint8 // palette index parallel to pwAbove, populated only when LayerTraceActive

	// LayerSourceTrace and LayerPaletteTrace record each visible pixel's
	// source and palette index when LayerTraceActive is set. The traces
	// expose compositor attribution without changing rendering.
	LayerSourceTrace  [240][256]uint8
	LayerPaletteTrace [240][256]uint8
	LayerTraceActive  bool

	// latchCGRAMAddr / latchOAMAddr mirror bsnes's
	// `latch.cgramAddress` / `latch.oamAddress` (sfc/ppu/io.cpp:60,
	// sfc/ppu/screen.cpp:148, sfc/ppu/io.cpp:47). Real hardware
	// constantly updates them with the most-recently-sampled
	// CGRAM palette index / OAM byte address during rendering. A
	// CGDATA / OAMDATA write that arrives during active display
	// is redirected to that latch instead of dropped, so the byte
	// always lands somewhere even if the destination is corrupted.
	// Renderer code updates these at each CGRAM/OAM read site;
	// CGDATA / OAMDATA register handlers consult them to choose
	// the redirect target.
	latchCGRAMAddr uint8
	latchOAMAddr   uint16
}

type Mode7LatchEvent struct {
	Addr       uint16
	Value      uint16
	FrameCount int
	HCounter   int
	VCounter   int
}

type Mode7MatrixPairEvent struct {
	FirstAddr  uint16
	FirstValue uint16
	NextAddr   uint16
	NextValue  uint16
	FrameCount int
	HCounter   int
	VCounter   int
}

type Mode7TracePoint struct {
	X      int
	TexelX int
	TexelY int
}

type Mode7ScanlineEvent struct {
	Y          int
	Matrix     [4]uint16
	Center     [2]uint16
	Scroll     [2]uint16
	M7SEL      uint8
	FrameCount int
	HCounter   int
	VCounter   int
	Points     [3]Mode7TracePoint
	LastPair   Mode7MatrixPairEvent
}

type WriteEvent struct {
	Space    string
	Addr     uint32
	Register uint16
	Before   uint8
	After    uint8
}

func NewPPU() *PPU {
	p := &PPU{
		FrontBuffer:      make([]uint16, 256*240),
		hiresFrontBuffer: make([]uint16, 512*240),
		Width:            256,
		Height:           224,
		vPeriod:          ntscVPeriod,
		hPeriod:          ntscHPeriod,
	}
	return p
}

// EnableLayerTrace toggles the LayerSourceTrace capture. When enabled,
// renderScanlinePixelWalk copies pwAbove[x].source for x in [0,256)
// into LayerSourceTrace[y] for visible scanlines y in [0,240). The
// trace is overwritten each frame; callers should snapshot it after
// the frame they care about.
func (p *PPU) EnableLayerTrace(on bool) {
	p.LayerTraceActive = on
	if !on {
		p.LayerSourceTrace = [240][256]uint8{}
		p.LayerPaletteTrace = [240][256]uint8{}
	}
}

// tracePalAbove stamps the palette index of the just-plotted pixel
// into pwAbovePal when LayerTraceActive is set and the plot landed on
// the above-side compositor buffer with the expected source. The
// source equality check is what tells us our plot won the slot
// compare; without it a higher-priority pixel that overrode ours
// would be mis-attributed to our palette. Below-side plots are
// ignored — palette tracing only follows the main-screen winner.
func (p *PPU) tracePalAbove(buf *[512]layerPixel, x int, source uint8, palIdx uint8) {
	if !p.LayerTraceActive || buf != &p.pwAbove {
		return
	}
	if buf[x].source == source {
		p.pwAbovePal[x] = palIdx
	}
}

// LayerSourceCSV maps a LayerSourceTrace byte to the snes9x v2 CSV
// layer-source string. OBJ1/OBJ2 collapse to "OBJ" because the v2
// CSV schema does not split them. Backdrop maps to "BACK". Unknown
// (zero) bytes also map to "BACK" so the harness has a defined
// answer for pre-trace scanlines. Color-math output ("COL") is not
// produced today; the trace records the main-screen winner before
// color math runs.
func LayerSourceCSV(b uint8) string {
	// SourceCOL is the color-math overlay marker stamped on top of an
	// underlying source byte; check it first so the trace reports
	// "COL" for math-output pixels rather than falling through to
	// "BACK" via the default branch.
	switch {
	case b&SourceCOL != 0:
		return "COL"
	case b&SourceBG1 != 0:
		return "BG1"
	case b&SourceBG2 != 0:
		return "BG2"
	case b&SourceBG3 != 0:
		return "BG3"
	case b&SourceBG4 != 0:
		return "BG4"
	case b&(SourceOBJ|SourceOBJ1) != 0:
		return "OBJ"
	default:
		return "BACK"
	}
}

func (p *PPU) visibleLines() int {
	if (p.SETINI & 0x04) != 0 {
		return 240
	}
	return 224
}

// ForceBlank reports INIDISP bit 7. When set, the screen is blanked to black
// and the VRAM/OAM/CGRAM write-protection gates are disabled — software uses
// it to stream graphics during active display.
func (p *PPU) ForceBlank() bool {
	return (p.INIDISP & 0x80) != 0
}

func (p *PPU) vdisp() int {
	if (p.SETINI & 0x04) != 0 {
		return 240
	}
	return 225
}

// writesBlocked reports whether the PPU currently drops writes to VRAM,
// OAM, and CGRAM. Returns true inside active display (vCounter < vdisp)
// when force-blank is off. Matches bsnes sfc/ppu/io.cpp writeVRAM / writeOAM
// / writeCGRAM display-disable gate.
func (p *PPU) writesBlocked() bool {
	if p.ForceBlank() {
		return false
	}
	return p.vCounter < p.vdisp()
}

// vramReadsBlocked reports whether $2139/$213A should return the display
// fetcher's blocked value instead of VRAM. Address and latch bookkeeping still
// runs through the register handlers.
func (p *PPU) vramReadsBlocked() bool {
	if p.ForceBlank() {
		return false
	}
	return p.vCounter < p.vdisp()
}

func (p *PPU) currentHPeriod() int {
	if p.hPeriod == 0 {
		return ntscHPeriod
	}
	return p.hPeriod
}

func (p *PPU) currentVPeriod() int {
	if p.vPeriod == 0 {
		return p.baseVPeriod()
	}
	return p.vPeriod
}

func (p *PPU) baseVPeriod() int {
	if p.palTiming {
		return palVPeriod
	}
	return ntscVPeriod
}

func (p *PPU) scanlineDots() int {
	return p.currentHPeriod() / 4
}

func (p *PPU) tickScanline() bool {
	p.vCounter++
	if p.vCounter == 128 {
		p.ppuInterlace = p.SETINI&0x01 != 0
		if p.ppuInterlace && !p.ppuField {
			p.vPeriod = p.baseVPeriod() + 1
		}
	}

	frameWrapped := false
	if p.vCounter == p.currentVPeriod() {
		p.vPeriod = p.baseVPeriod()
		p.vCounter = 0
		p.ppuField = !p.ppuField
		p.FrameCount++
		frameWrapped = true
	}

	p.hPeriod = ntscHPeriod
	if !p.palTiming && !p.ppuInterlace && p.ppuField && p.vCounter == ntscShortScanline {
		p.hPeriod = ntscShortHPeriod
	}
	if p.palTiming && p.ppuInterlace && p.ppuField && p.vCounter == palVPeriod-1 {
		p.hPeriod = ntscHPeriod + 4
	}
	return frameWrapped
}

func (p *PPU) stat78Field() bool {
	// bsnes/ares keep separate CPU and PPU PPUcounter instances. During
	// VBlank the PPU thread advances a whole scanline when synchronized, so
	// STAT78 can observe the PPU field one line ahead of the CPU beam without
	// changing the latched OPHCT/OPVCT coordinates.
	vCounter := p.vCounter
	field := p.ppuField
	if p.hCounter > 0 && vCounter > ntscShortScanline {
		vCounter++
		if vCounter == p.currentVPeriod() {
			field = !field
		}
	}
	return field
}

func (p *PPU) resetCounter() {
	p.cycles = 0
	p.beamFrameStart = 0
	p.beamVBlankStart = 0
	p.vblankActive = false
	p.FrameCount = 0
	p.hCounter = 0
	p.vCounter = 0
	p.ppuField = false
	p.ppuInterlace = false
	p.vPeriod = p.baseVPeriod()
	p.hPeriod = ntscHPeriod
	p.NMIFlag = false
	p.nmiHold = 0
	p.RangeOver = false
	p.TimeOver = false
	p.hReadHigh = false
	p.vReadHigh = false
	p.hvLatched = false
	p.startFrame()
}

// Scheduler Thread Interface
func (p *PPU) Run() {
	// Advance cycles
	p.cycles += 4
	if p.nmiHold > 0 {
		p.nmiHold--
	}
	p.hCounter++

	if p.hCounter >= p.scanlineDots() {
		p.hCounter = 0
		frameWrapped := p.tickScanline()
		if frameWrapped {
			p.beamFrameStart = p.cycles
			p.startFrame()
			// Frame Start Logic
			// Clear NMI Flag on new frame (V-Line 0)
			p.NMIFlag = false
			p.RangeOver = false
			p.TimeOver = false
		}

		// Render Line Logic (if in visible range)
		visible := p.visibleLines()
		p.Height = visible
		if p.vCounter >= 1 && p.vCounter <= visible {
			p.RenderScanline(p.vCounter - 1)
		}
	}

	// The NMI line is a beam level, separate from the read-to-clear flag.
	// SETINI changes vdisp immediately; interlace is captured at line 128.
	active := p.vCounter >= p.vdisp()
	if active != p.vblankActive {
		p.vblankActive = active
		p.NMIFlag = active
		if active {
			p.beamVBlankStart = p.cycles
			p.nmiHold = 1
			if p.FrameHook != nil {
				p.completeFrame()
			}
		}
	}

	// Beam events only request bus ownership; CPU edges perform all DMA work.
	if p.DMA != nil {
		if p.vCounter == 0 && uint64(p.hCounter*4) == 12+p.beamFrameStart%8 {
			p.DMA.RequestHDMA(p.cycles, true)
		}
		if p.hCounter == 276 && p.vCounter < p.vdisp() {
			p.DMA.RequestHDMA(p.cycles, false)
		}
	}
}

// QuietUntil returns the GetCycles value at which the next Run with an
// effect beyond advancing the beam starts: rendering a scanline, changing
// vblank, or requesting HDMA. Runs that start before it only advance the
// beam. It is computed from current state, so register writes need no
// invalidation.
func (p *PPU) QuietUntil() uint64 {
	if p.nmiHold > 0 || (p.vCounter >= p.vdisp()) != p.vblankActive {
		return p.cycles
	}
	h := p.hCounter
	k := p.scanlineDots() - h // Run that wraps the scanline
	if h < 276 && 276-h < k {
		k = 276 - h // HDMA line request
	}
	if p.vCounter == 0 {
		// Frame-start HDMA request, when 12+beamFrameStart%8 is dot aligned.
		if t := 12 + int(p.beamFrameStart%8); t%4 == 0 && h < t/4 && t/4-h < k {
			k = t/4 - h
		}
	}
	if k < 1 {
		k = 1
	}
	return p.cycles + 4*uint64(k-1)
}

// RunUntil runs the PPU while GetCycles is below cycles, advancing the
// beam over quiet dots in one step.
func (p *PPU) RunUntil(cycles uint64) {
	for p.cycles < cycles {
		if quiet := p.QuietUntil(); quiet > p.cycles {
			n := min((quiet-p.cycles)/4, (cycles-p.cycles+3)/4)
			p.cycles += 4 * n
			p.hCounter += int(n)
			continue
		}
		p.Run()
	}
}

// RenderScanline moved to render.go

func (p *PPU) GetCycles() uint64 {
	return p.cycles
}

func (p *PPU) ResetCycles() {
	p.cycles = 0
}

func (p *PPU) Frequency() uint64 {
	return 21477272 // 21.477 MHz (Master Clock)
}

func (p *PPU) Power(reset bool) {
	p.PPU1OpenBus = 0xff
	p.PPU2OpenBus = 0xff
	p.resetCounter()
}

// SetPAL configures the PPU frame period for PAL or NTSC timing.
func (p *PPU) SetPAL(enabled bool) {
	p.palTiming = enabled
	if p.vCounter == 0 || p.vPeriod == ntscVPeriod || p.vPeriod == palVPeriod {
		p.vPeriod = p.baseVPeriod()
	}
}

func (p *PPU) Refresh() {
	// End of frame logic
}

// Memory Access Helpers (used by Register logic)

func (p *PPU) ReadVRAM(addr uint16) uint8 {
	return p.VRAM[addr]
}

func (p *PPU) WriteVRAM(addr uint16, val uint8) {
	p.writeVRAM(addr, val, 0)
}

func (p *PPU) ReadOAM(addr uint16) uint8 {
	if int(addr) < len(p.OAM) {
		return p.OAM[addr]
	}
	return 0
}

func (p *PPU) WriteOAM(addr uint16, val uint8) {
	if int(addr) < len(p.OAM) {
		p.writeOAM(addr, val, 0)
	}
}

func (p *PPU) ReadCGRAM(addr uint8) uint8 {
	// CGRAM address is usually 9-bit handled by registers.
	// We'll wrap or panic?
	// The implementation here just exposes the array.
	// Let's assume input is byte offset.
	// But CGRAM is 512 bytes. addr uint8 covers 256.
	// We need uint16 addr for full access if linear.
	// Register $2121 sets address. Read/Write $2122 accesses it.
	// Wait, we keep this simple.
	return p.CGRAM[addr]
}

func (p *PPU) WriteCGRAM(addr uint16, val uint8) {
	if int(addr) < len(p.CGRAM) {
		if addr < 16 { // Just log first few to avoid spam
			// fmt.Printf("PPU: WriteCGRAM[%x] = %x\n", addr, val)
		}
		p.writeCGRAM(addr, val, 0)
	}
}

func (p *PPU) writeVRAM(addr uint16, val uint8, register uint16) {
	before := p.VRAM[addr]
	p.VRAM[addr] = val
	p.noteWrite("vram", uint32(addr), register, before, val)
}

func (p *PPU) writeOAM(addr uint16, val uint8, register uint16) {
	if int(addr) >= len(p.OAM) {
		return
	}
	before := p.OAM[addr]
	p.OAM[addr] = val
	p.noteWrite("oam", uint32(addr), register, before, val)
}

func (p *PPU) writeCGRAM(addr uint16, val uint8, register uint16) {
	if int(addr) >= len(p.CGRAM) {
		return
	}
	before := p.CGRAM[addr]
	p.CGRAM[addr] = val
	p.noteWrite("cgram", uint32(addr), register, before, val)
}

func (p *PPU) noteWrite(space string, addr uint32, register uint16, before, after uint8) {
	if p.WriteHook != nil {
		p.WriteHook(WriteEvent{
			Space:    space,
			Addr:     addr,
			Register: register,
			Before:   before,
			After:    after,
		})
	}
}

// LatchBeam returns the current (h, v) counter pair in dot/scanline
// coordinates. It is the hook the Super Scope uses to sample the PPU's
// H/V latch at the moment the light-pen trigger pulls — the hardware
// equivalent is a WRIO pulse latching OPHCT/OPVCT readable via $213C/$213D.
// Horizontal wraps at 340 dots; vertical wraps at 262 lines (NTSC).
func (p *PPU) LatchBeam() (h, v uint16) {
	p.latchedH = uint16(p.hCounter)
	p.latchedV = uint16(p.vCounter)
	p.hReadHigh = false
	p.vReadHigh = false
	p.hvLatched = true
	return p.latchedH, p.latchedV
}

func (p *PPU) LatchBeamAt(masterCycles uint64) (h, v uint16) {
	// The scheduler may have advanced the PPU to the first 4-master tick past
	// a CPU read. SLHV latches bsnes/ares hdot() at the CPU read timestamp, so
	// use the previous dot when synchronization rounded the PPU ahead.
	hCounter := p.hCounter
	if p.cycles > masterCycles && hCounter > 0 {
		hCounter--
	}
	p.latchedH = uint16(hCounter)
	p.latchedV = uint16(p.vCounter)
	p.hReadHigh = false
	p.vReadHigh = false
	p.hvLatched = true
	return p.latchedH, p.latchedV
}

func (p *PPU) ReadRDNMI() uint8 {
	val := uint8(0x02) // CPU Version
	if p.NMIFlag {
		val |= 0x80
		if p.nmiHold == 0 {
			p.NMIFlag = false // Clear on Read
		}
	}
	return val
}

func (p *PPU) ReadHVBJOY() uint8 {
	res := uint8(0)
	// Bit 7: V-Blank Status (1=VBlank)
	if p.vCounter >= p.vdisp() {
		res |= 0x80
	}
	// Bit 6: H-Blank Status (1=HBlank)
	// Project bsnes' master-cycle HBlank bounds onto this dot counter.
	if p.hCounter <= 1 || p.hCounter >= 275 {
		res |= 0x40
	}
	// Bit 0: Auto-Joypad Status (1=Active/Busy)
	if p.AutoJoypad && p.autoJoypadBusy() {
		res |= 0x01
	}
	return res
}

func (p *PPU) ReadHVBJOYAt(hcounter, vcounter int) uint8 {
	res := uint8(0)
	// Bit 7: V-Blank Status (1=VBlank)
	if vcounter >= p.vdisp() {
		res |= 0x80
	}
	// Bit 6: H-Blank Status (1=HBlank)
	if hcounter <= 2 || hcounter >= 1096 {
		res |= 0x40
	}
	// Bit 0: Auto-Joypad Status (1=Active/Busy)
	if p.AutoJoypad && p.autoJoypadBusyAt(hcounter, vcounter) {
		res |= 0x01
	}
	return res
}

func (p *PPU) autoJoypadBusy() bool {
	firstVBlank := p.vdisp()
	if p.vCounter != firstVBlank {
		return false
	}
	return p.hCounter >= 32
}

func (p *PPU) autoJoypadBusyAt(hcounter, vcounter int) bool {
	firstVBlank := p.vdisp()
	if vcounter != firstVBlank {
		return false
	}
	return hcounter >= 128
}

// BeamEvents returns master-clock timestamps of the latest field start and
// vblank rising edge. Zero means no such edge has occurred since reset.
// Reading $4210 does not consume these events.
func (p *PPU) BeamEvents() (frameStart, vblankStart uint64) {
	return p.beamFrameStart, p.beamVBlankStart
}
