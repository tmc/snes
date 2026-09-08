// Package snes implements a Super Nintendo Entertainment System emulator in Go.
// It coordinates the 65c816 CPU, PPU, SPC700/DSP audio subsystem, memory bus,
// controllers, and cartridge hardware. Timing and cartridge compatibility remain
// under development.
//
// Construct a [System] with [NewSystem], load a ROM with [System.LoadROM], call
// [System.Power], and advance it with [System.RunFrame]. Read video through
// [System.FrameBuffer] and stereo audio through [System.DrainAudio] at
// [AudioSampleRate] sample frames per second. A System
// requires initialization and must be accessed by one goroutine at a time.
//
// [System.Serialize] and [System.Unserialize] save and restore version-3 states.
// Older states are rejected because their hardware state schemas differ.
// States normally require the same ROM; they are not a stable interchange format
// across emulator versions. Use [System.SaveRAM] for cartridge battery data.
package snes
