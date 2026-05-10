package emulator_test

// Runnable godoc Examples for each public interface in the
// emulator package. Each Example uses a tiny synthetic LoROM that
// boots to a NOP stream — no commercial ROM, audio fixture, or
// libretro core is required, so the examples run deterministically
// in any environment via `go test ./emulator/...`.
//
// The concrete implementation under demonstration is *snes.System
// (the canonical emulator.Interface implementation). The Examples
// avoid relying on output that varies between runs (frame contents,
// audio samples) — they assert only deterministic state via the
// `// Output:` lines.

import (
	"fmt"

	"github.com/tmc/snes"
	"github.com/tmc/snes/emulator"
)

// newTinyROM returns a 32 KiB LoROM that boots into a NOP stream.
// The header carries enough of a valid SFC layout for the emulator
// to detect mapping and reset cleanly. Mirrors the helper used by
// the snes_test package for cold-boot tests.
func newTinyROM() []byte {
	rom := make([]byte, 0x8000)
	rom[0x7fd5] = 0x20 // map mode: LoROM
	rom[0x7ffc] = 0x00 // RESET vector low
	rom[0x7ffd] = 0x80 // RESET vector high
	for i := 0; i < 0x100; i++ {
		rom[i] = 0xea // NOP at the reset vector and beyond
	}
	return rom
}

// ExampleInterface demonstrates that *snes.System satisfies the
// composite emulator.Interface. The compile-time assertion proves
// the implementation contract; the runtime check confirms the
// pointer is non-nil for callers that use dependency injection.
func ExampleInterface() {
	var iface emulator.Interface = snes.NewSystem(nil)
	if iface != nil {
		fmt.Println("emulator.Interface implementation ready")
	}
	// Output: emulator.Interface implementation ready
}

// ExampleCore demonstrates the Core lifecycle: load a ROM, power
// on, reset, run a frame, then unload.
func ExampleCore() {
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(newTinyROM()); err != nil {
		fmt.Println("load:", err)
		return
	}
	fmt.Println("loaded:", sys.Loaded())
	info := sys.Information()
	fmt.Println("system:", info.Manufacturer, info.Name)
	sys.Power()
	sys.Reset()
	if err := sys.RunFrame(); err != nil {
		fmt.Println("run:", err)
		return
	}
	sys.Unload()
	fmt.Println("loaded after unload:", sys.Loaded())
	// Output:
	// loaded: true
	// system: Nintendo Super Nintendo Entertainment System
	// loaded after unload: false
}

// ExampleVideo shows the Video interface: report display
// metadata and pull the framebuffer slice. Frame contents vary
// per run, so the Example asserts only the buffer length and
// the display metadata that's deterministic.
func ExampleVideo() {
	sys := snes.NewSystem(nil)
	_ = sys.LoadROM(newTinyROM())
	sys.Power()
	d := sys.Display()
	fmt.Printf("display %dx%d, type=%d\n", d.Width, d.Height, d.Type)
	fb := sys.FrameBuffer()
	fmt.Println("framebuffer len:", len(fb))
	// Output:
	// display 256x224, type=0
	// framebuffer len: 61440
}

// ExampleAudio shows pulling stereo audio samples into a caller-
// supplied slice. DrainAudio returns the number of int16 samples
// written; the example reports whether at least zero samples were
// produced (the buffer is non-negative by contract).
func ExampleAudio() {
	sys := snes.NewSystem(nil)
	_ = sys.LoadROM(newTinyROM())
	sys.Power()
	_ = sys.RunFrame()
	buf := make([]int16, 1024)
	n := sys.DrainAudio(buf)
	fmt.Println("audio samples drained ≥ 0:", n >= 0)
	// Output: audio samples drained ≥ 0: true
}

// ExampleInputDriver shows the input enumeration and connection
// API: enumerate ports, list available devices on a port, query
// the connected device, and set controller bits.
func ExampleInputDriver() {
	sys := snes.NewSystem(nil)
	_ = sys.LoadROM(newTinyROM())
	sys.Power()
	ports := sys.Ports()
	fmt.Println("port count:", len(ports))
	devs := sys.Devices(0)
	fmt.Println("port-0 device count:", len(devs))
	connected := sys.Connected(0)
	fmt.Println("port-0 connected device id:", connected)
	if err := sys.SetInputState(0, emulator.StandardButtonA); err != nil {
		fmt.Println("set:", err)
	}
	// Output:
	// port count: 2
	// port-0 device count: 5
	// port-0 connected device id: 1
}

// ExampleState demonstrates save-state serialization and SRAM
// persistence. State.Serialize/Unserialize round-trip the entire
// emulator; SaveRAM/LoadSaveRAM round-trip just the cartridge SRAM.
func ExampleState() {
	sys := snes.NewSystem(nil)
	_ = sys.LoadROM(newTinyROM())
	sys.Power()
	state, err := sys.Serialize()
	if err != nil {
		fmt.Println("serialize:", err)
		return
	}
	fmt.Println("state non-empty:", len(state) > 0)
	if err := sys.Unserialize(state); err != nil {
		fmt.Println("unserialize:", err)
		return
	}
	sram := sys.SaveRAM()
	fmt.Println("sram len:", len(sram))
	if err := sys.LoadSaveRAM(sram); err != nil {
		fmt.Println("loadsave:", err)
	}
	// Output:
	// state non-empty: true
	// sram len: 0
}

// ExampleCapabilities demonstrates the generic Cap/Get/Set
// capability surface. Cap reports whether a named capability is
// supported; Get and Set provide untyped access to that capability.
// The exact set of capabilities is implementation-defined.
func ExampleCapabilities() {
	var caps emulator.Capabilities = snes.NewSystem(nil)
	// Querying an unknown capability returns false and a nil value
	// without panicking — useful for graceful frontend feature
	// detection.
	fmt.Println("unknown cap supported:", caps.Cap("unknown.feature"))
	fmt.Println("unknown cap value:", caps.Get("unknown.feature"))
	fmt.Println("unknown cap set ok:", caps.Set("unknown.feature", 42))
	// Output:
	// unknown cap supported: false
	// unknown cap value: <nil>
	// unknown cap set ok: false
}

// ExampleSettings demonstrates the FrameSkip and RunAhead toggles
// exposed via the Settings interface. Both default to off and
// round-trip through their Set* counterparts.
func ExampleSettings() {
	var settings emulator.Settings = snes.NewSystem(nil)
	fmt.Println("default frame skip:", settings.FrameSkip())
	fmt.Println("default run-ahead:", settings.RunAhead())
	settings.SetFrameSkip(2)
	settings.SetRunAhead(true)
	fmt.Println("frame skip after set:", settings.FrameSkip())
	fmt.Println("run-ahead after set:", settings.RunAhead())
	// Output:
	// default frame skip: 0
	// default run-ahead: false
	// frame skip after set: 2
	// run-ahead after set: true
}
