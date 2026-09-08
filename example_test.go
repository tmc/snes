package snes_test

import (
	"fmt"

	"github.com/tmc/snes"
)

func Example() {
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(exampleROM()); err != nil {
		panic(err)
	}
	sys.Power()
	if err := sys.RunFrame(); err != nil {
		panic(err)
	}
	var memory [1]byte
	if _, err := sys.ReadWRAMAt(memory[:], 0x2000); err != nil {
		panic(err)
	}
	fmt.Printf("program wrote %02x\n", memory[0])
	// Output: program wrote 5a
}

func ExampleSystem_Serialize() {
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(exampleROM()); err != nil {
		panic(err)
	}
	sys.Power()
	state, err := sys.Serialize()
	if err != nil {
		panic(err)
	}
	if err := sys.RunFrame(); err != nil {
		panic(err)
	}
	if err := sys.Unserialize(state); err != nil {
		panic(err)
	}
	var memory [1]byte
	if _, err := sys.ReadWRAMAt(memory[:], 0x2000); err != nil {
		panic(err)
	}
	fmt.Printf("restored byte %02x\n", memory[0])
	// Output: restored byte 00
}

func ExampleAudioSampleRate() {
	// Allocate one second of interleaved stereo audio.
	buffer := make([]int16, 2*snes.AudioSampleRate)
	fmt.Println(len(buffer))
	// Output: 64080
}

// exampleROM writes $5a to WRAM $7e2000, then loops. It needs no external file.
func exampleROM() []byte {
	rom := make([]byte, 32<<10)
	copy(rom, []byte{0xa9, 0x5a, 0x8f, 0x00, 0x20, 0x7e, 0x80, 0xfe})
	rom[0x7fd5] = 0x20 // LoROM
	rom[0x7fd7] = 5    // 32 KiB
	rom[0x7ffd] = 0x80 // reset at $008000
	return rom
}
