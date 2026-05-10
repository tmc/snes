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

const stateVersion = 1

type systemState struct {
	Version uint32

	ROMHash [32]byte

	BusMDR    uint8
	BusMEMSEL uint8
	WRAM      []byte
	WRAMAddr  uint32

	CPU       cpu.CPUState
	PPU       ppu.PPUState
	APU       apu.APUState
	DMA       dma.DMAState
	Scheduler scheduler.SchedulerState

	AutoJoypadEnabled bool
	Joy1              uint16
	Joy2              uint16
	PendingDMA        uint8
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
	CartRAM           []byte
	CartState         []byte

	FrameSkip uint
	RunAhead  bool
	Cheats    []Cheat
}

// Serialize serializes the emulator state.
func (s *System) Serialize() ([]byte, error) {
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
		CPU:               s.CPU.SaveState(),
		PPU:               s.PPU.SaveState(),
		APU:               s.APU.SaveState(),
		DMA:               s.DMA.SaveState(),
		Scheduler:         s.Scheduler.SaveState(),
		AutoJoypadEnabled: s.autoJoypadEnabled,
		Joy1:              s.joy1,
		Joy2:              s.joy2,
		PendingDMA:        s.pendingDMA,
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
		CartRAM:   s.SaveRAM(),
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

// Unserialize restores the emulator state.
func (s *System) Unserialize(data []byte) error {
	var state systemState
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return fmt.Errorf("unserialize: %w", err)
	}
	if state.Version != stateVersion {
		return fmt.Errorf("unserialize: unsupported state version %d", state.Version)
	}
	if state.ROMHash != ([32]byte{}) && s.romHash != ([32]byte{}) && state.ROMHash != s.romHash {
		return errors.New("unserialize: loaded cartridge does not match state")
	}
	if err := s.wram.LoadData(state.WRAM); err != nil {
		return fmt.Errorf("unserialize: wram: %w", err)
	}

	s.Bus.MDR = state.BusMDR
	s.Bus.WriteMEMSEL(state.BusMEMSEL)
	s.wramAddr = state.WRAMAddr & 0x1ffff
	s.CPU.LoadState(state.CPU)
	s.PPU.LoadState(state.PPU)
	s.APU.LoadState(state.APU)
	s.DMA.LoadState(state.DMA)
	s.Scheduler.LoadState(state.Scheduler)
	s.autoJoypadEnabled = state.AutoJoypadEnabled
	s.joy1 = state.Joy1
	s.joy2 = state.Joy2
	s.pendingDMA = state.PendingDMA
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
	if len(state.CartRAM) > 0 {
		if err := s.LoadSaveRAM(state.CartRAM); err != nil {
			return fmt.Errorf("unserialize: cartridge ram: %w", err)
		}
	}
	return nil
}
