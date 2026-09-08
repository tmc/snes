package snes

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/tmc/snes/internal/apu"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/dma"
	"github.com/tmc/snes/internal/input"
	"github.com/tmc/snes/internal/ppu"
	"github.com/tmc/snes/internal/scheduler"
)

const stateVersion = 3

type systemState struct {
	Version uint32

	ROMHash [32]byte

	BusMDR    uint8
	BusMEMSEL uint8
	WRAM      []byte
	WRAMAddr  uint32
	WRIO      uint8

	CPU       cpu.CPUState
	PPU       ppu.PPUState
	APU       apu.APUState
	DMA       dma.DMAState
	Scheduler scheduler.SchedulerState

	AutoJoypadEnabled bool
	Joy1              uint16
	Joy2              uint16
	PALTiming         bool
	Connected         [2]uint
	Controller1       input.State
	Controller2       input.State
	Mouse1            input.MouseState
	Mouse2            input.MouseState
	SuperScope1       input.SuperScopeState
	SuperScope2       input.SuperScopeState
	Multitap1         input.MultitapState
	Multitap2         input.MultitapState
	MultitapSub1      [4]input.State
	MultitapSub2      [4]input.State
	CartState         []byte

	FrameSkip uint
	RunAhead  bool
	Cheats    []Cheat
}

// Serialize serializes the emulator state.
func (s *System) Serialize() ([]byte, error) {
	if (s.CPU != nil && s.CPU.Executing()) || (s.DMA != nil && s.DMA.Busy()) {
		return nil, errors.New("serialize: execution is active")
	}
	if s.wram == nil {
		return nil, errors.New("serialize: system not initialized")
	}

	var cartState []byte
	if s.cart != nil {
		data, err := s.cart.Serialize()
		if err != nil {
			return nil, fmt.Errorf("serialize: cartridge: %w", err)
		}
		cartState = data
	}

	state := systemState{
		Version:           stateVersion,
		ROMHash:           s.romHash,
		BusMDR:            s.Bus.MDR,
		BusMEMSEL:         s.Bus.MEMSEL,
		WRAM:              s.wram.Data(),
		WRAMAddr:          s.wramAddr,
		WRIO:              s.wrio,
		CPU:               s.CPU.SaveState(),
		PPU:               s.PPU.SaveState(),
		APU:               s.APU.SaveState(),
		DMA:               s.DMA.SaveState(),
		Scheduler:         s.Scheduler.SaveState(),
		AutoJoypadEnabled: s.autoJoypadEnabled,
		Joy1:              s.joy1,
		Joy2:              s.joy2,
		PALTiming:         s.palTiming,
		Connected:         s.connected,
		Controller1:       s.Controller1.SaveState(),
		Controller2:       s.Controller2.SaveState(),
		Mouse1:            s.Mouse1.SaveState(),
		Mouse2:            s.Mouse2.SaveState(),
		SuperScope1:       s.SuperScope1.SaveState(),
		SuperScope2:       s.SuperScope2.SaveState(),
		Multitap1:         s.Multitap1.SaveState(),
		Multitap2:         s.Multitap2.SaveState(),
		MultitapSub1: [4]input.State{
			s.MultitapSub1[0].SaveState(),
			s.MultitapSub1[1].SaveState(),
			s.MultitapSub1[2].SaveState(),
			s.MultitapSub1[3].SaveState(),
		},
		MultitapSub2: [4]input.State{
			s.MultitapSub2[0].SaveState(),
			s.MultitapSub2[1].SaveState(),
			s.MultitapSub2[2].SaveState(),
			s.MultitapSub2[3].SaveState(),
		},
		CartState: cartState,
		FrameSkip: s.frameSkip,
		RunAhead:  s.runAhead,
		Cheats:    s.Cheats(),
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize restores a version-3 emulator state. Older states are rejected
// because they omit required hardware latches or use the previous DMA schema.
func (s *System) Unserialize(data []byte) error {
	return s.unserialize(data, false)
}

// UnserializeOptions controls save-state restoration.
type UnserializeOptions struct {
	// IgnoreROMHash permits restoring a state whose embedded ROM hash differs
	// from the loaded cartridge. It is intended for diagnostics and
	// provenance tooling that compares local ROM variants. Normal callers
	// should leave it false.
	IgnoreROMHash bool
}

// UnserializeWithOptions restores the emulator state with explicit diagnostic
// options.
func (s *System) UnserializeWithOptions(data []byte, opts UnserializeOptions) error {
	return s.unserialize(data, opts.IgnoreROMHash)
}

func (s *System) unserialize(data []byte, ignoreROMHash bool) error {
	if (s.CPU != nil && s.CPU.Executing()) || (s.DMA != nil && s.DMA.Busy()) {
		return errors.New("unserialize: execution is active")
	}
	if s.wram == nil {
		return errors.New("unserialize: system not initialized")
	}
	if len(data) > 64<<20 {
		return errors.New("unserialize: state too large")
	}
	var state systemState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return fmt.Errorf("unserialize: %w", err)
	}
	if state.Version != stateVersion {
		return fmt.Errorf("unserialize: unsupported state version %d", state.Version)
	}
	if !ignoreROMHash && state.ROMHash != s.romHash {
		return errors.New("unserialize: loaded cartridge does not match state")
	}
	if err := s.validateState(&state); err != nil {
		return fmt.Errorf("unserialize: %w", err)
	}
	if err := s.wram.LoadData(state.WRAM); err != nil {
		return fmt.Errorf("unserialize: wram: %w", err)
	}

	s.Bus.MDR = state.BusMDR
	s.Bus.WriteMEMSEL(state.BusMEMSEL)
	s.wramAddr = state.WRAMAddr
	s.wrio = state.WRIO
	s.CPU.LoadState(state.CPU)
	s.PPU.LoadState(state.PPU)
	if err := s.APU.LoadState(state.APU); err != nil {
		return fmt.Errorf("unserialize: apu: %w", err)
	}
	if err := s.DMA.LoadState(state.DMA); err != nil {
		return fmt.Errorf("unserialize: dma: %w", err)
	}
	s.Scheduler.LoadState(state.Scheduler)
	s.autoJoypadEnabled = state.AutoJoypadEnabled
	s.PPU.AutoJoypad = s.autoJoypadEnabled
	s.joy1 = state.Joy1
	s.joy2 = state.Joy2
	s.palTiming = state.PALTiming
	s.connected = state.Connected
	s.Controller1.LoadState(state.Controller1)
	s.Controller2.LoadState(state.Controller2)
	s.Mouse1.LoadState(state.Mouse1)
	s.Mouse2.LoadState(state.Mouse2)
	s.SuperScope1.LoadState(state.SuperScope1)
	s.SuperScope2.LoadState(state.SuperScope2)
	s.Multitap1.LoadState(state.Multitap1)
	s.Multitap2.LoadState(state.Multitap2)
	for i := 0; i < 4; i++ {
		s.MultitapSub1[i].LoadState(state.MultitapSub1[i])
		s.MultitapSub2[i].LoadState(state.MultitapSub2[i])
	}
	s.setConnectedDevice(0)
	s.setConnectedDevice(1)
	s.frameSkip = state.FrameSkip
	s.runAhead = state.RunAhead
	if err := s.SetCheats(state.Cheats); err != nil {
		return fmt.Errorf("unserialize: cheats: %w", err)
	}
	if len(state.CartState) > 0 {
		if s.cart == nil {
			return errors.New("unserialize: cartridge state without loaded cartridge")
		}
		if err := s.cart.Unserialize(state.CartState); err != nil {
			return fmt.Errorf("unserialize: cartridge: %w", err)
		}
	}
	return nil
}

// validateState checks every fallible restore operation before live state changes.
func (s *System) validateState(state *systemState) error {
	if err := dma.ValidateExecution(state.DMA.Execution); err != nil {
		return err
	}
	if state.DMA.Execution.Phase != 0 {
		return errors.New("state contains an active DMA continuation without CPU continuation")
	}
	if state.FrameSkip > 9 {
		return errors.New("frame skip exceeds maximum 9")
	}
	for _, memory := range []struct {
		name      string
		got, want int
	}{
		{"wram", len(state.WRAM), len(s.wram.Data())},
		{"vram", len(state.PPU.VRAM), len(s.PPU.VRAM)},
		{"oam", len(state.PPU.OAM), len(s.PPU.OAM)},
		{"cgram", len(state.PPU.CGRAM), len(s.PPU.CGRAM)},
		{"apu ram", len(state.APU.RAM), len(s.APU.RAM)},
		{"frame buffer", len(state.PPU.FrontBuffer), 256 * 240},
		{"hires frame buffer", len(state.PPU.HiresFrontBuffer), 512 * 240},
	} {
		if memory.got != memory.want {
			return fmt.Errorf("%s size %d, want %d", memory.name, memory.got, memory.want)
		}
	}
	if state.WRAMAddr > 0x1ffff || state.Scheduler.IRQMode > 3 || state.Scheduler.IRQH > 0x1ff || state.Scheduler.IRQV > 0x1ff {
		return errors.New("invalid register state")
	}
	p := &state.PPU
	if p.Width < 1 || p.Width > 256 || p.Height < 1 || p.Height > 240 ||
		p.HCounter < 0 || p.HCounter >= p.HPeriod/4 || p.VCounter < 0 || p.VCounter >= p.VPeriod ||
		(p.HPeriod != 1360 && p.HPeriod != 1364 && p.HPeriod != 1368) ||
		(p.VPeriod != 262 && p.VPeriod != 263 && p.VPeriod != 312 && p.VPeriod != 313) {
		return errors.New("invalid ppu dimensions or timing")
	}
	for _, device := range state.Connected {
		if device > deviceMultitap {
			return errors.New("invalid controller device")
		}
	}
	for i, cheat := range state.Cheats {
		if cheat.Address > 0xffffff {
			return fmt.Errorf("cheat %d has out-of-range address", i)
		}
	}
	if err := apu.ValidateState(state.APU); err != nil {
		return err
	}
	if s.cart == nil {
		if len(state.CartState) != 0 {
			return errors.New("cartridge state without loaded cartridge")
		}
	} else {
		if len(state.CartState) == 0 {
			return errors.New("missing cartridge state")
		}
		if err := s.cart.ValidateState(state.CartState); err != nil {
			return err
		}
	}
	return nil
}
