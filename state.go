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

	CPU       cpu.CPUState
	PPU       ppu.PPUState
	APU       apu.APUState
	DMA       dma.DMAState
	Scheduler scheduler.SchedulerState

	AutoJoypadEnabled bool
	Joy1              uint16
	Joy2              uint16
	Connected         [2]uint
	Controller1       input.State
	Controller2       input.State
	CartRAM           []byte

	FrameSkip uint
	RunAhead  bool
	Cheats    []Cheat
}

// Serialize serializes the emulator state.
func (s *System) Serialize() ([]byte, error) {
	if s.wram == nil {
		return nil, errors.New("serialize: system not initialized")
	}

	state := systemState{
		Version:           stateVersion,
		ROMHash:           s.romHash,
		BusMDR:            s.Bus.MDR,
		BusMEMSEL:         s.Bus.MEMSEL,
		WRAM:              s.wram.Data(),
		CPU:               s.CPU.SaveState(),
		PPU:               s.PPU.SaveState(),
		APU:               s.APU.SaveState(),
		DMA:               s.DMA.SaveState(),
		Scheduler:         s.Scheduler.SaveState(),
		AutoJoypadEnabled: s.autoJoypadEnabled,
		Joy1:              s.joy1,
		Joy2:              s.joy2,
		Connected:         s.connected,
		Controller1:       s.Controller1.SaveState(),
		Controller2:       s.Controller2.SaveState(),
		CartRAM:           s.SaveRAM(),
		FrameSkip:         s.frameSkip,
		RunAhead:          s.runAhead,
		Cheats:            s.Cheats(),
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
	s.CPU.LoadState(state.CPU)
	s.PPU.LoadState(state.PPU)
	s.APU.LoadState(state.APU)
	s.DMA.LoadState(state.DMA)
	s.Scheduler.LoadState(state.Scheduler)
	s.autoJoypadEnabled = state.AutoJoypadEnabled
	s.joy1 = state.Joy1
	s.joy2 = state.Joy2
	s.connected = state.Connected
	s.Controller1.LoadState(state.Controller1)
	s.Controller2.LoadState(state.Controller2)
	s.frameSkip = state.FrameSkip
	s.runAhead = state.RunAhead
	if err := s.SetCheats(state.Cheats); err != nil {
		return fmt.Errorf("unserialize: cheats: %w", err)
	}
	if len(state.CartRAM) > 0 {
		if err := s.LoadSaveRAM(state.CartRAM); err != nil {
			return fmt.Errorf("unserialize: cartridge ram: %w", err)
		}
	}
	return nil
}
