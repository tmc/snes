package snes

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/apu"
	"github.com/tmc/snes/internal/bus"
	"github.com/tmc/snes/internal/cartridge"
	"github.com/tmc/snes/internal/cartridge/chips/gsu"
	"github.com/tmc/snes/internal/cartridge/chips/gsu/ppuvram"
	"github.com/tmc/snes/internal/cartridge/chips/updsp"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/dma"
	"github.com/tmc/snes/internal/input"
	"github.com/tmc/snes/internal/ppu"
	"github.com/tmc/snes/internal/scheduler"
)

const (
	deviceNone = iota
	deviceStandardController
	deviceMouse
	deviceSuperScope
	deviceMultitap
)

// AudioSampleRate is the number of stereo sample frames emitted per second.
const AudioSampleRate = apu.SampleRate

// System is the central coordinator of the SNES emulator.
type System struct {
	Bus         *bus.Bus
	CPU         *cpu.CPU
	PPU         *ppu.PPU
	APU         *apu.APU
	DMA         *dma.DMA
	Scheduler   *scheduler.Scheduler
	Controller1 *input.StandardController
	Controller2 *input.StandardController
	Mouse1      *input.Mouse
	Mouse2      *input.Mouse
	SuperScope1 *input.SuperScope
	SuperScope2 *input.SuperScope
	Multitap1   *input.Multitap
	Multitap2   *input.Multitap
	// MultitapSub1[i] / MultitapSub2[i] hold the four sub-controllers
	// pre-attached to each multitap (slots 0..3). The multitap routes
	// reads to one slot per pair according to its select line, mirroring
	// the bsnes sfc/controller/multitap path.
	MultitapSub1 [4]*input.StandardController
	MultitapSub2 [4]*input.StandardController

	wram *bus.RAMDevice
	io   *IODevice

	cart      *cartridge.Cartridge
	gsu       *gsu.Device
	romHash   [32]byte
	connected [2]uint
	devices   [2]input.Device

	autoJoypadEnabled bool
	joy1              uint16
	joy2              uint16
	wrio              uint8
	wramAddr          uint32
	palTiming         bool

	frameSkip uint
	runAhead  bool
	cheats    []Cheat

	traceAPUIO bool
}

// NewSystem returns a new SNES system instance.
func NewSystem(iface emulator.Interface) *System {
	b := bus.NewBus()
	sys := &System{
		Bus:         b,
		CPU:         cpu.NewCPU(b),
		PPU:         ppu.NewPPU(),
		APU:         apu.NewAPU(),
		Scheduler:   scheduler.NewScheduler(),
		Controller1: input.NewStandardController(),
		Controller2: input.NewStandardController(),
		Mouse1:      input.NewMouse(),
		Mouse2:      input.NewMouse(),
		wram:        bus.NewWRAMDevice(),
		traceAPUIO:  os.Getenv("SNES_TRACE_APUIO") != "",
	}
	// SuperScope's beam-position latcher reads back the PPU's latched
	// H/V counters at the moment of a $4016 latch strobe with the
	// trigger pulled. *ppu.PPU directly satisfies the
	// input.BeamLatcher interface via its LatchBeam method.
	sys.SuperScope1 = input.NewSuperScope(sys.PPU)
	sys.SuperScope2 = input.NewSuperScope(sys.PPU)
	// Multitap fans out to 4 standard sub-controllers per port. We
	// pre-allocate them so callers can drive button state via
	// MultitapSub{1,2}[slot].SetState(...) without having to attach
	// devices through the input package directly.
	sys.Multitap1 = input.NewMultitap()
	sys.Multitap2 = input.NewMultitap()
	for i := 0; i < 4; i++ {
		sys.MultitapSub1[i] = input.NewStandardController()
		sys.MultitapSub2[i] = input.NewStandardController()
		sys.Multitap1.Devices[i] = sys.MultitapSub1[i]
		sys.Multitap2.Devices[i] = sys.MultitapSub2[i]
	}
	sys.io = &IODevice{sys: sys}
	sys.connected[0] = deviceStandardController
	sys.setConnectedDevice(0)
	sys.setConnectedDevice(1)

	sys.Scheduler.RegisterCPU(sys.CPU, sys.CPU.Frequency())
	sys.Scheduler.RegisterAPU(sys.APU, sys.APU.Frequency())
	sys.Scheduler.RegisterPPU(sys.PPU, sys.PPU.Frequency())
	sys.Scheduler.SetAfterCPU(sys.stepCartridge)
	sys.DMA = dma.NewDMA(b, sys.Scheduler)
	sys.PPU.DMA = sys.DMA
	sys.CPU.ClockAdvanced = sys.Scheduler.SyncPPULazy
	sys.DMA.SetClock(sys.CPU.GetCycles, sys.Scheduler.AddDMACycles)
	sys.CPU.BusEdge = sys.dmaEdge

	sys.remapBaseDevices()
	return sys
}

func (s *System) stepCartridge(masterCycles uint64) {
	if s.cart != nil {
		s.cart.Step(masterCycles)
	}
}

// GSU returns the attached GSU device, or nil for non-GSU cartridges.
// Intended for diagnostics and parity tests; the returned device shares
// state with the running system, so callers must only read from it.
func (s *System) GSU() *gsu.Device { return s.gsu }

func (s *System) dmaEdge(clocks uint64) {
	s.Scheduler.SyncPPULazy()
	s.DMA.BeginEdge(clocks)
	for s.DMA.Busy() {
		s.DMA.RunSlice(8)
	}
}

func (s *System) remapBaseDevices() {
	s.Bus.Clear()
	s.Bus.InitializeWaitStates()

	if s.cart != nil {
		s.cart.MapToBus(s.Bus)
	}

	s.Bus.Map(0x7E0000, 0x7FFFFF, s.wram)

	for i := uint32(0); i < 0x40; i++ {
		bankLow := i << 16
		bankHigh := (i | 0x80) << 16

		s.Bus.Map(bankLow|0x0000, bankLow|0x1FFF, s.wram)
		s.Bus.Map(bankHigh|0x0000, bankHigh|0x1FFF, s.wram)

		s.Bus.Map(bankLow|0x2100, bankLow|0x21FF, s.io)
		s.Bus.Map(bankHigh|0x2100, bankHigh|0x21FF, s.io)

		s.Bus.Map(bankLow|0x4000, bankLow|0x43FF, s.io)
		s.Bus.Map(bankHigh|0x4000, bankHigh|0x43FF, s.io)
	}
}

func (s *System) captureAutoJoypad() {
	if s.connected[0] != deviceStandardController {
		s.joy1 = 0
	} else {
		s.joy1 = s.Controller1.Poll()
	}
	if s.connected[1] != deviceStandardController {
		s.joy2 = 0
	} else {
		s.joy2 = s.Controller2.Poll()
	}
}

func (s *System) setConnectedDevice(port uint) {
	if port > 1 {
		return
	}
	switch s.connected[port] {
	case deviceStandardController:
		if port == 0 {
			s.devices[port] = s.Controller1
		} else {
			s.devices[port] = s.Controller2
		}
	case deviceMouse:
		if port == 0 {
			s.devices[port] = s.Mouse1
		} else {
			s.devices[port] = s.Mouse2
		}
	case deviceSuperScope:
		if port == 0 {
			s.devices[port] = s.SuperScope1
		} else {
			s.devices[port] = s.SuperScope2
		}
	case deviceMultitap:
		if port == 0 {
			s.devices[port] = s.Multitap1
		} else {
			s.devices[port] = s.Multitap2
		}
	default:
		s.devices[port] = nil
	}
}

// Information reports metadata about the emulated system.
func (s *System) Information() emulator.Information {
	return emulator.Information{
		Manufacturer: "Nintendo",
		Name:         "Super Nintendo Entertainment System",
		Extension:    "sfc,smc,fig",
		Resettable:   true,
	}
}

// Display reports the primary display geometry.
func (s *System) Display() emulator.Display {
	return emulator.Display{
		ID:               0,
		Name:             "Main",
		Type:             emulator.DisplayTypeCRT,
		Colors:           1 << 15,
		Width:            uint(s.PPU.Width),
		Height:           uint(s.PPU.Height),
		InternalWidth:    uint(s.PPU.Width),
		InternalHeight:   uint(s.PPU.Height),
		AspectCorrection: 8.0 / 7.0,
	}
}

// FrameBuffer returns the current front buffer.
func (s *System) FrameBuffer() []uint16 {
	return s.PPU.FrontBuffer
}

// DrainAudio copies available stereo samples into dst.
func (s *System) DrainAudio(dst []int16) int {
	return s.APU.DrainAudio(dst)
}

// Loaded reports whether a cartridge has been loaded through LoadROM.
func (s *System) Loaded() bool {
	return s.cart != nil
}

// ROMAddress returns the physical ROM offset addressed by a CPU bus read.
// It reports false when no cartridge is loaded or the address maps to
// cartridge RAM, WRAM, an SA-1 vector override, or another non-ROM region.
func (s *System) ROMAddress(addr uint32) (uint32, bool) {
	if s.cart == nil {
		return 0, false
	}
	if bank := uint8(addr >> 16); bank == 0x7E || bank == 0x7F {
		return 0, false
	}
	return s.cart.ROMAddress(addr)
}

// ROMSHA256 returns the SHA-256 of the loaded ROM image with any copier
// header removed, or the zero value when no cartridge is loaded.
func (s *System) ROMSHA256() [32]byte {
	return s.romHash
}

// ROMProvenance names the loaded cartridge's mapper and reports whether
// ROMAddress fully describes its ROM mapping. Only plain LoROM
// cartridges without a coprocessor are supported; for others the
// mapping may depend on runtime state.
func (s *System) ROMProvenance() (mapper string, ok bool) {
	if s.cart == nil {
		return "", false
	}
	switch s.cart.Mode {
	case cartridge.LoROM:
		mapper = "lorom"
	case cartridge.HiROM:
		mapper = "hirom"
	case cartridge.ExLoROM:
		mapper = "exlorom"
	case cartridge.ExHiROM:
		mapper = "exhirom"
	default:
		mapper = "unknown"
	}
	if s.cart.CoprocessorID != "" {
		return mapper + "+" + s.cart.CoprocessorID, false
	}
	return mapper, s.cart.Mode == cartridge.LoROM
}

// LoadROM installs a ROM into the system without powering it on.
func (s *System) LoadROM(data []byte) error {
	return s.LoadROMWithOptions(data, LoadROMOptions{})
}

// LoadROMOptions controls cartridge admission.
type LoadROMOptions struct {
	// DiagnosticPassthrough permits inspection of incomplete hardware or
	// missing DSP firmware. It does not make those cartridges executable.
	DiagnosticPassthrough bool
	// DSPVariant selects DSP-1, DSP-1A, DSP-1B, DSP-2, DSP-3 or DSP-4 when
	// the header cannot identify the firmware. Firmware is read from the
	// matching SNES_DSP*_ROM variables.
	DSPVariant string
	// Coprocessor selects explicit board hardware when the header is ambiguous.
	// An empty value uses standard header detection.
	Coprocessor string
}

// LoadROMWithOptions installs a cartridge after validating its header, hardware
// support and firmware selection. A failure preserves the previous cartridge.
func (s *System) LoadROMWithOptions(data []byte, opts LoadROMOptions) error {
	if len(data) == 0 {
		return errors.New("load rom: empty rom")
	}
	if err := cartridge.ValidateHeader(data); err != nil {
		return fmt.Errorf("load rom: %w", err)
	}
	cart, err := cartridge.NewWithCoprocessor(data, opts.Coprocessor)
	if err != nil {
		return fmt.Errorf("load rom: %w", err)
	}
	// ST-018 (SETA ARM6) is documented as a stub-only deliverable per
	// implementation_plan.md:495 + 507. Detection is shipped, but
	// execution is not implemented. Fail closed so the caller cannot
	// silently boot a cartridge that would produce wrong output.
	if cart.CoprocessorID == "st018" && !opts.DiagnosticPassthrough {
		return fmt.Errorf("load rom: %w: ST-018 (SETA ARM6) execution is not implemented",
			cartridge.ErrUnsupportedCoprocessor)
	}
	// SPC7110 stub per implementation_plan.md:494 + 509. The DCU
	// decompressor + dataport + MCU + EpsonRTC are not implemented;
	// the bounded ALU sub-unit at $4820-$482F is feasible (~260-370
	// LOC per /tmp/collab-469-spc7110-feasibility.md Path 1) but
	// not game-observable in isolation because every known SPC7110
	// title invokes the DCU first for graphics decompression. Fail
	// closed until the full chip lands.
	if cart.CoprocessorID == "spc7110" && !opts.DiagnosticPassthrough {
		return fmt.Errorf("load rom: %w: SPC7110 execution is not implemented",
			cartridge.ErrUnsupportedCoprocessor)
	}
	if !opts.DiagnosticPassthrough {
		switch cart.CoprocessorID {
		case "sa1", "sdd1", "st01x", "supergameboy":
			return fmt.Errorf("load rom: %w: %s execution is not implemented", cartridge.ErrUnsupportedCoprocessor, cart.CoprocessorID)
		}
	}
	if opts.DSPVariant != "" && cart.CoprocessorID != "dsp1" {
		return fmt.Errorf("load rom: dsp variant specified for a non-dsp cartridge")
	}
	var nextGSU *gsu.Device
	if cart.CoprocessorID == "gsu" {
		nextGSU = cart.AttachGSU(ppuvram.New(s.PPU))
	}
	if cart.CoprocessorID == "dsp1" {
		variant := updsp.VariantUnknown
		if opts.DSPVariant != "" {
			selected := updsp.VariantUnknown
			for v := updsp.VariantDSP1; v <= updsp.VariantDSP4; v++ {
				if opts.DSPVariant == v.String() {
					selected = v
				}
			}
			if selected == updsp.VariantUnknown {
				return fmt.Errorf("load rom: invalid dsp variant %q", opts.DSPVariant)
			}
			variant = selected
		}
		if variant == updsp.VariantUnknown {
			return fmt.Errorf("load rom: dsp variant is ambiguous; select DSPVariant explicitly")
		}
		loader, err := updsp.LoadWithEnv(variant)
		if errors.Is(err, updsp.ErrROMMissing) && opts.DiagnosticPassthrough {
			loader, err = updsp.Load(variant, nil, nil)
		}
		if err != nil {
			return fmt.Errorf("load rom: updsp: %w", err)
		}
		cart.AttachUPDSP(loader)
	}
	s.cart = cart
	s.gsu = nextGSU
	s.romHash = sha256.Sum256(s.cart.ROM)
	s.palTiming = s.cart.PAL
	s.APU.SetPortComparePatch(true)
	s.Scheduler.SetPAL(s.palTiming)
	s.PPU.SetPAL(s.palTiming)
	s.remapBaseDevices()
	return nil
}

// Unload removes the currently loaded cartridge.
func (s *System) Unload() {
	s.cart = nil
	s.gsu = nil
	s.romHash = [32]byte{}
	s.palTiming = false
	s.APU.SetPortComparePatch(false)
	s.Scheduler.SetPAL(false)
	s.PPU.SetPAL(false)
	s.remapBaseDevices()
}

// Power powers on the system.
func (s *System) Power() {
	if s.cart != nil {
		s.cart.Power()
	}
	s.Scheduler.Reset()
	s.APU.Power(true)
	s.DMA.Reset()
	s.PPU.Power(true)
	s.wrio = 0xff
	s.autoJoypadEnabled = false
	s.PPU.AutoJoypad = false
	s.wramAddr = 0
	s.Scheduler.SetPAL(s.palTiming)
	s.PPU.SetPAL(s.palTiming)
	// Reset all request sources before reset-vector reads advance the bus.
	s.CPU.Power(true)
	s.captureAutoJoypad()
}

// Reset resets the system.
func (s *System) Reset() {
	s.Power()
}

// RunFrame runs the system for one frame.
func (s *System) RunFrame() error {
	steps := s.frameSkip + 1
	for i := uint(0); i < steps; i++ {
		s.applyCheats()
		s.Scheduler.RunFrame()
		if s.autoJoypadEnabled {
			s.captureAutoJoypad()
		}
	}

	if !s.runAhead {
		return nil
	}

	state, err := s.Serialize()
	if err != nil {
		return fmt.Errorf("run frame: serialize run-ahead state: %w", err)
	}

	hook := s.PPU.FrameHook
	s.PPU.FrameHook = nil
	s.applyCheats()
	s.Scheduler.RunFrame()
	if s.autoJoypadEnabled {
		s.captureAutoJoypad()
	}
	s.PPU.FrameHook = hook
	speculative := append([]uint16(nil), s.PPU.FrontBuffer...)

	if err := s.Unserialize(state); err != nil {
		return fmt.Errorf("run frame: restore run-ahead state: %w", err)
	}
	copy(s.PPU.FrontBuffer, speculative)
	return nil
}

// SaveRAM returns a copy of the loaded cartridge RAM.
func (s *System) SaveRAM() []byte {
	if s.cart == nil {
		return nil
	}
	return s.cart.SaveRAM()
}

// LoadSaveRAM restores cartridge RAM.
func (s *System) LoadSaveRAM(data []byte) error {
	if s.cart == nil {
		if len(data) == 0 {
			return nil
		}
		return errors.New("load save ram: no cartridge loaded")
	}
	return s.cart.LoadSaveRAM(data)
}

// Ports reports the controller ports exposed by the system.
func (s *System) Ports() []emulator.Port {
	return []emulator.Port{
		{ID: 0, Name: "Controller Port 1"},
		{ID: 1, Name: "Controller Port 2"},
	}
}

// Devices reports supported devices for a port.
func (s *System) Devices(port uint) []emulator.Device {
	switch port {
	case 0:
		return []emulator.Device{
			{ID: deviceNone, Name: "None"},
			{ID: deviceStandardController, Name: "Standard Controller"},
			{ID: deviceMouse, Name: "Mouse"},
			{ID: deviceSuperScope, Name: "Super Scope"},
			{ID: deviceMultitap, Name: "Multitap"},
		}
	case 1:
		return []emulator.Device{
			{ID: deviceNone, Name: "None"},
			{ID: deviceStandardController, Name: "Standard Controller"},
			{ID: deviceMouse, Name: "Mouse"},
			{ID: deviceSuperScope, Name: "Super Scope"},
			{ID: deviceMultitap, Name: "Multitap"},
		}
	default:
		return nil
	}
}

// Inputs reports the supported inputs for a device.
func (s *System) Inputs(port, device uint) []emulator.Input {
	if port != 0 && port != 1 {
		return nil
	}
	switch device {
	case deviceStandardController:
		return []emulator.Input{
			{Type: emulator.InputTypeButton, Name: "B"},
			{Type: emulator.InputTypeButton, Name: "Y"},
			{Type: emulator.InputTypeButton, Name: "Select"},
			{Type: emulator.InputTypeButton, Name: "Start"},
			{Type: emulator.InputTypeButton, Name: "Up"},
			{Type: emulator.InputTypeButton, Name: "Down"},
			{Type: emulator.InputTypeButton, Name: "Left"},
			{Type: emulator.InputTypeButton, Name: "Right"},
			{Type: emulator.InputTypeButton, Name: "A"},
			{Type: emulator.InputTypeButton, Name: "X"},
			{Type: emulator.InputTypeButton, Name: "L"},
			{Type: emulator.InputTypeButton, Name: "R"},
		}
	case deviceMouse:
		return []emulator.Input{
			{Type: emulator.InputTypeButton, Name: "Left"},
			{Type: emulator.InputTypeButton, Name: "Right"},
			{Type: emulator.InputTypeAxis, Name: "X"},
			{Type: emulator.InputTypeAxis, Name: "Y"},
		}
	case deviceSuperScope:
		return []emulator.Input{
			{Type: emulator.InputTypeButton, Name: "Trigger"},
			{Type: emulator.InputTypeButton, Name: "Cursor"},
			{Type: emulator.InputTypeButton, Name: "Turbo"},
			{Type: emulator.InputTypeButton, Name: "Pause"},
		}
	case deviceMultitap:
		// A Multitap fans out to four StandardController sub-ports
		// addressed via the select line; the input list is the
		// standard 12 buttons, replicated four times by slot.
		return []emulator.Input{
			{Type: emulator.InputTypeButton, Name: "B"},
			{Type: emulator.InputTypeButton, Name: "Y"},
			{Type: emulator.InputTypeButton, Name: "Select"},
			{Type: emulator.InputTypeButton, Name: "Start"},
			{Type: emulator.InputTypeButton, Name: "Up"},
			{Type: emulator.InputTypeButton, Name: "Down"},
			{Type: emulator.InputTypeButton, Name: "Left"},
			{Type: emulator.InputTypeButton, Name: "Right"},
			{Type: emulator.InputTypeButton, Name: "A"},
			{Type: emulator.InputTypeButton, Name: "X"},
			{Type: emulator.InputTypeButton, Name: "L"},
			{Type: emulator.InputTypeButton, Name: "R"},
		}
	default:
		return nil
	}
}

// Connected reports the currently connected device for a port.
func (s *System) Connected(port uint) uint {
	if int(port) >= len(s.connected) {
		return deviceNone
	}
	return s.connected[port]
}

// Connect attaches a supported device to a port.
func (s *System) Connect(port, device uint) error {
	switch port {
	case 0:
		if device != deviceNone && device != deviceStandardController && device != deviceMouse && device != deviceSuperScope && device != deviceMultitap {
			return fmt.Errorf("connect port %d: unsupported device %d", port, device)
		}
		s.connected[port] = device
		if device == deviceNone {
			s.Controller1.SetState(0)
		}
		s.setConnectedDevice(port)
		return nil
	case 1:
		if device != deviceNone && device != deviceStandardController && device != deviceMouse && device != deviceSuperScope && device != deviceMultitap {
			return fmt.Errorf("connect port %d: unsupported device %d", port, device)
		}
		s.connected[port] = device
		if device == deviceNone {
			s.Controller2.SetState(0)
		}
		s.setConnectedDevice(port)
		return nil
	default:
		return fmt.Errorf("connect: unsupported port %d", port)
	}
}

// SetInputState pushes the current input state for a port.
func (s *System) SetInputState(port uint, state uint16) error {
	if port > 1 {
		return fmt.Errorf("set input state: unsupported port %d", port)
	}
	if s.connected[port] != deviceStandardController {
		return fmt.Errorf("set input state: no controller on port %d", port)
	}
	if port == 0 {
		s.Controller1.SetState(state)
	} else {
		s.Controller2.SetState(state)
	}
	return nil
}

// SetMultitapSubState pushes the current button state for a multitap
// sub-controller. port selects the multitap's connected port (0 or 1)
// and slot selects one of the four sub-controllers (0..3). The sub-
// controller multiplexes onto the multitap's serial line according to
// the select-line state set via SetMultitapSelect.
func (s *System) SetMultitapSubState(port, slot uint, state uint16) error {
	if port > 1 {
		return fmt.Errorf("set multitap sub state: unsupported port %d", port)
	}
	if s.connected[port] != deviceMultitap {
		return fmt.Errorf("set multitap sub state: no multitap on port %d", port)
	}
	if slot > 3 {
		return fmt.Errorf("set multitap sub state: invalid slot %d (want 0..3)", slot)
	}
	subs := &s.MultitapSub1
	if port == 1 {
		subs = &s.MultitapSub2
	}
	subs[slot].SetState(state)
	return nil
}

// SetMultitapSelect drives the multitap's select line for a port. On
// hardware this is bit 1 of a $4017 write; callers can drive it
// directly here when a test wants to flip the sub-pair without going
// through the bus. False routes slots {0,1}; true routes slots {2,3}.
func (s *System) SetMultitapSelect(port uint, sel bool) error {
	if port > 1 {
		return fmt.Errorf("set multitap select: unsupported port %d", port)
	}
	if s.connected[port] != deviceMultitap {
		return fmt.Errorf("set multitap select: no multitap on port %d", port)
	}
	tap := s.Multitap1
	if port == 1 {
		tap = s.Multitap2
	}
	tap.SetSelect(sel)
	return nil
}

// SetSuperScopeState pushes the current Super Scope button state for a port.
// The four buttons (trigger, cursor, turbo, pause) match the bits the SNSP-
// SCOPE-A reports through $4016/$4017; beam-position latching is driven by
// the trigger edge at the next $4016 latch strobe via *ppu.PPU.LatchBeam.
func (s *System) SetSuperScopeState(port uint, trigger, cursor, turbo, pause bool) error {
	if port > 1 {
		return fmt.Errorf("set super scope state: unsupported port %d", port)
	}
	if s.connected[port] != deviceSuperScope {
		return fmt.Errorf("set super scope state: no super scope on port %d", port)
	}
	scope := s.SuperScope1
	if port == 1 {
		scope = s.SuperScope2
	}
	scope.SetButtons(trigger, cursor, turbo, pause)
	return nil
}

// SetMouseState pushes the current mouse button state and relative motion for
// a port. The motion delta is accumulated until the next controller latch.
func (s *System) SetMouseState(port uint, left, right bool, dx, dy int32) error {
	if port > 1 {
		return fmt.Errorf("set mouse state: unsupported port %d", port)
	}
	if s.connected[port] != deviceMouse {
		return fmt.Errorf("set mouse state: no mouse on port %d", port)
	}
	var m *input.Mouse
	if port == 0 {
		m = s.Mouse1
	} else {
		m = s.Mouse2
	}
	m.SetButton(left, right)
	m.SetDelta(dx, dy)
	return nil
}

// FrameSkip returns the configured additional frames to execute per RunFrame call.
func (s *System) FrameSkip() uint {
	return s.frameSkip
}

// SetFrameSkip sets the additional frames to execute per RunFrame call.
func (s *System) SetFrameSkip(frameSkip uint) {
	if frameSkip > 9 {
		frameSkip = 9
	}
	s.frameSkip = frameSkip
}

// RunAhead reports whether one-frame run-ahead is enabled.
func (s *System) RunAhead() bool {
	return s.runAhead
}

// SetRunAhead toggles one-frame run-ahead.
func (s *System) SetRunAhead(runAhead bool) {
	s.runAhead = runAhead
}

// Cap reports whether a named capability is supported.
func (s *System) Cap(name string) bool {
	switch name {
	case "frameskip", "frame_skip", "runahead", "run_ahead", "cheats":
		return true
	default:
		return false
	}
}

// Get reads a named capability value.
func (s *System) Get(name string) interface{} {
	switch name {
	case "frameskip", "frame_skip":
		return s.FrameSkip()
	case "runahead", "run_ahead":
		return s.RunAhead()
	case "cheats":
		return s.Cheats()
	default:
		return nil
	}
}

// Set updates a named capability value.
func (s *System) Set(name string, value interface{}) bool {
	switch name {
	case "frameskip", "frame_skip":
		v, ok := value.(uint)
		if !ok {
			return false
		}
		s.SetFrameSkip(v)
		return true
	case "runahead", "run_ahead":
		v, ok := value.(bool)
		if !ok {
			return false
		}
		s.SetRunAhead(v)
		return true
	case "cheats":
		v, ok := value.([]Cheat)
		if !ok {
			return false
		}
		if err := s.SetCheats(v); err != nil {
			return false
		}
		return true
	default:
		return false
	}
}

// Load powers on the system for callers using the legacy API.
func (s *System) Load() bool {
	s.Power()
	return true
}

// Run runs the system for one frame for callers using the legacy API.
func (s *System) Run() error {
	s.applyCheats()
	s.Scheduler.RunDisplayFrame()
	if s.autoJoypadEnabled {
		s.captureAutoJoypad()
	}
	return nil
}

type IODevice struct {
	sys *System
}

func (d *IODevice) Read(addr uint32) uint8 {
	offset := addr & 0xFFFF

	if offset >= 0x2100 && offset <= 0x213F {
		d.sys.Scheduler.Sync(d.sys.PPU)
		switch offset {
		case 0x2137:
			if d.sys.wrio&0x80 != 0 {
				d.sys.PPU.LatchBeamAt(d.sys.CPU.Cycles)
			}
			return d.sys.Bus.MDR
		case 0x213F:
			return d.sys.PPU.ReadSTAT78(d.sys.wrio&0x80 != 0)
		}
		return d.sys.PPU.ReadRegisterWithCPUOpenBus(uint16(addr), d.sys.Bus.MDR)
	}
	if offset >= 0x2140 && offset <= 0x2143 {
		d.sys.Scheduler.SyncPortRead(d.sys.APU)
		value := d.sys.APU.ReadPort(addr)
		if d.sys.traceAPUIO && d.sys.CPU.Cycles <= 300000 {
			log.Printf("apuior cyc=%d pc=%02X:%04X addr=%04X val=%02X in=%02X/%02X/%02X/%02X out=%02X/%02X/%02X/%02X apupc=%04X",
				d.sys.CPU.Cycles, d.sys.CPU.PB, d.sys.CPU.PC, offset, value,
				d.sys.APU.InPorts[0], d.sys.APU.InPorts[1], d.sys.APU.InPorts[2], d.sys.APU.InPorts[3],
				d.sys.APU.OutPorts[0], d.sys.APU.OutPorts[1], d.sys.APU.OutPorts[2], d.sys.APU.OutPorts[3],
				d.sys.APU.Processor.PC)
		}
		return value
	}
	if offset == 0x2180 {
		value := d.sys.wram.Read(0x7e0000 | d.sys.wramAddr)
		d.sys.wramAddr = (d.sys.wramAddr + 1) & 0x1ffff
		return value
	}
	if offset >= 0x4300 && offset <= 0x437F {
		return d.sys.DMA.Read(offset)
	}

	if offset >= 0x4200 && offset <= 0x42FF {
		switch offset {
		case 0x4210:
			d.sys.Scheduler.Sync(d.sys.PPU)
			return (d.sys.Bus.MDR & 0x70) | (d.sys.PPU.ReadRDNMI() & 0x8F)
		case 0x4211:
			return d.sys.Scheduler.ReadTIMEUP()
		case 0x4212:
			hcounter, vcounter := cpuBeamAt(d.sys.CPU.Cycles, d.sys.palTiming)
			return (d.sys.Bus.MDR & 0x3E) | d.sys.PPU.ReadHVBJOYAt(hcounter, vcounter)
		case 0x4213:
			return d.sys.wrio
		case 0x4214:
			return uint8(d.sys.CPU.Quotient)
		case 0x4215:
			return uint8(d.sys.CPU.Quotient >> 8)
		case 0x4216:
			return uint8(d.sys.CPU.MultiplicationResult)
		case 0x4217:
			return uint8(d.sys.CPU.MultiplicationResult >> 8)
		}

		if offset >= 0x4218 && offset <= 0x421F {
			switch offset {
			case 0x4218:
				return uint8(d.sys.joy1)
			case 0x4219:
				return uint8(d.sys.joy1 >> 8)
			case 0x421A:
				return uint8(d.sys.joy2)
			case 0x421B:
				return uint8(d.sys.joy2 >> 8)
			default:
				return 0
			}
		}
	}

	if offset == 0x4016 {
		serial := uint8(0)
		if dev := d.sys.devices[0]; dev != nil {
			serial = dev.ReadSerial()
		}
		return (d.sys.Bus.MDR & 0xfc) | (serial & 1)
	}
	if offset == 0x4017 {
		serial := uint8(0)
		if dev := d.sys.devices[1]; dev != nil {
			serial = dev.ReadSerial()
		}
		return (d.sys.Bus.MDR & 0xe0) | 0x1c | (serial & 1)
	}

	return 0
}

func (d *IODevice) Write(addr uint32, value uint8) {
	offset := addr & 0xFFFF

	if offset >= 0x2100 && offset <= 0x213F {
		d.sys.Scheduler.Sync(d.sys.PPU)
		d.sys.PPU.WriteRegister(uint16(addr), value)
		return
	}
	if offset >= 0x2140 && offset <= 0x2143 {
		d.sys.Scheduler.SyncBefore(d.sys.APU)
		d.sys.APU.WritePort(addr, value)
		d.sys.Scheduler.SyncPortWrite(d.sys.APU)
		if d.sys.traceAPUIO && d.sys.CPU.Cycles <= 300000 {
			log.Printf("apuiow cyc=%d pc=%02X:%04X addr=%04X val=%02X in=%02X/%02X/%02X/%02X out=%02X/%02X/%02X/%02X apupc=%04X",
				d.sys.CPU.Cycles, d.sys.CPU.PB, d.sys.CPU.PC, offset, value,
				d.sys.APU.InPorts[0], d.sys.APU.InPorts[1], d.sys.APU.InPorts[2], d.sys.APU.InPorts[3],
				d.sys.APU.OutPorts[0], d.sys.APU.OutPorts[1], d.sys.APU.OutPorts[2], d.sys.APU.OutPorts[3],
				d.sys.APU.Processor.PC)
		}
		return
	}
	switch offset {
	case 0x2180:
		d.sys.wram.Write(0x7e0000|d.sys.wramAddr, value)
		d.sys.wramAddr = (d.sys.wramAddr + 1) & 0x1ffff
		return
	case 0x2181:
		d.sys.wramAddr = (d.sys.wramAddr & 0x1ff00) | uint32(value)
		return
	case 0x2182:
		d.sys.wramAddr = (d.sys.wramAddr & 0x100ff) | uint32(value)<<8
		return
	case 0x2183:
		d.sys.wramAddr = (d.sys.wramAddr & 0x0ffff) | uint32(value&1)<<16
		return
	}
	if offset >= 0x4300 && offset <= 0x437F {
		d.sys.DMA.Write(offset, value)
		return
	}

	if offset == 0x4016 {
		enabled := value&1 != 0
		if dev := d.sys.devices[0]; dev != nil {
			dev.Latch(enabled)
		}
		if dev := d.sys.devices[1]; dev != nil {
			dev.Latch(enabled)
		}
		return
	}

	if offset >= 0x4200 && offset <= 0x42FF {
		switch offset {
		case 0x4200:
			// Observe beam events under the previous interrupt enable state.
			d.sys.Scheduler.Sync(d.sys.PPU)
			nmiEnabled := (value & 0x80) != 0
			rising := d.sys.Scheduler.SetNMI(nmiEnabled)
			// bsnes nmiPoll: a 0->1 transition on NMITIMEN.7 while the
			// PPU NMI line is still latched (RDNMI bit 7 == 1) delivers
			// the held NMI immediately. Without this, a game that
			// disables NMI inside its handler and re-enables mid-frame
			// loses one NMI per cycle.
			if rising && d.sys.PPU.NMIFlag {
				d.sys.CPU.TriggerNMI()
			}

			d.sys.Scheduler.SetIRQMode((value >> 4) & 0x03)
			d.sys.autoJoypadEnabled = value&0x01 != 0
			d.sys.PPU.AutoJoypad = d.sys.autoJoypadEnabled
			if d.sys.autoJoypadEnabled {
				d.sys.captureAutoJoypad()
			}
			return
		case 0x4201:
			if d.sys.wrio&0x80 != 0 && value&0x80 == 0 {
				d.sys.Scheduler.Sync(d.sys.PPU)
				d.sys.PPU.LatchBeam()
			}
			d.sys.wrio = value
			return
		case 0x4202:
			d.sys.CPU.MultiplicandA = value
			return
		case 0x4203:
			d.sys.CPU.StartMultiply(value)
			return
		case 0x4204:
			d.sys.CPU.Dividend = (d.sys.CPU.Dividend & 0xFF00) | uint16(value)
			return
		case 0x4205:
			d.sys.CPU.Dividend = (d.sys.CPU.Dividend & 0x00FF) | (uint16(value) << 8)
			return
		case 0x4206:
			d.sys.CPU.Divisor = value
			if value == 0 {
				d.sys.CPU.Quotient = 0xFFFF
				d.sys.CPU.MultiplicationResult = d.sys.CPU.Dividend
			} else {
				d.sys.CPU.Quotient = d.sys.CPU.Dividend / uint16(value)
				d.sys.CPU.MultiplicationResult = d.sys.CPU.Dividend % uint16(value)
			}
			d.sys.CPU.MultiplyDividend = d.sys.CPU.Quotient
			return
		case 0x4207:
			d.sys.Scheduler.SetHTimerLow(value)
			return
		case 0x4208:
			d.sys.Scheduler.SetHTimerHigh(value)
			return
		case 0x4209:
			d.sys.Scheduler.SetVTimerLow(value)
			return
		case 0x420A:
			d.sys.Scheduler.SetVTimerHigh(value)
			return
		case 0x420B:
			d.sys.DMA.Request(value)
			return
		case 0x420C:
			d.sys.DMA.HDMAEnable = value
			return
		case 0x420D:
			d.sys.Bus.WriteMEMSEL(value)
			return
		}
	}
}

func cpuBeamAt(cycles uint64, pal bool) (hcounter, vcounter int) {
	const (
		lineCycles         uint64 = 1364
		scanlinesPerFrame  uint64 = 262
		shortScanline      uint64 = 240
		shortScanlineDelta uint64 = 4
		evenFrameCycles           = scanlinesPerFrame * lineCycles
		oddFrameCycles            = evenFrameCycles - shortScanlineDelta
		fieldPairCycles           = evenFrameCycles + oddFrameCycles
	)
	if pal {
		rem := cycles % (312 * lineCycles)
		return int(rem % lineCycles), int(rem / lineCycles)
	}

	rem := cycles % fieldPairCycles
	if rem < evenFrameCycles {
		return int(rem % lineCycles), int(rem / lineCycles)
	}

	rem -= evenFrameCycles
	shortStart := shortScanline * lineCycles
	switch {
	case rem < shortStart:
		return int(rem % lineCycles), int(rem / lineCycles)
	case rem < shortStart+lineCycles-shortScanlineDelta:
		return int(rem - shortStart), int(shortScanline)
	default:
		rem -= lineCycles - shortScanlineDelta
		return int(rem % lineCycles), int(shortScanline + 1 + rem/lineCycles)
	}
}

func (d *IODevice) BlockRead(addr uint32, length int) []byte {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = d.Read(addr + uint32(i))
	}
	return buf
}
