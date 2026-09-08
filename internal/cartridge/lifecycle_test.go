package cartridge

import (
	"fmt"
	"testing"
	"time"

	"github.com/tmc/snes/internal/cartridge/chips/srtc"
)

func ExampleCartridge_ValidateState() {
	cart := New(make([]byte, 0x8000))
	data, err := cart.Serialize()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(cart.ValidateState(data))
	// Output: <nil>
}

func TestCartridgeStateSRTC(t *testing.T) {
	rom := make([]byte, 0x8000)
	rom[loROMHeader+0x15] = 0x20
	rom[loROMHeader+0x16] = 0x55
	source := New(rom)
	source.Write(0x2801, 0xe)
	source.Write(0x2801, 0)
	for _, n := range []byte{6, 5, 4, 3, 2, 1, 7, 0, 8, 5, 9, 9} {
		source.Write(0x2801, n)
	}
	source.Write(0x2801, 0xd)
	source.Read(0x2800) // sentinel
	source.Read(0x2800) // second ones
	data, err := source.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []*Cartridge{source, New(rom)} {
		chip := target.coprocessor.(*srtc.Device)
		clockCalls := 0
		chip.SetClock(func() time.Time { clockCalls++; return time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC) })
		target.Write(0x2801, 0xe)
		if err := target.Unserialize(data); err != nil {
			t.Fatal(err)
		}
		if target.coprocessor != chip {
			t.Fatal("restore replaced clock-bearing device")
		}
		if got := target.Read(0x2800); got != 5 {
			t.Fatalf("second tens = %d, want 5", got)
		}
		if got := target.Read(0x2800); got != 4 {
			t.Fatalf("minute ones = %d, want 4", got)
		}
		chip.SyncTime()
		if clockCalls != 1 {
			t.Fatalf("clock calls = %d, want 1", clockCalls)
		}
	}
}

func TestCartridgeStateOBC1(t *testing.T) {
	rom := make([]byte, 0x8000)
	rom[loROMHeader+0x15] = 0x20
	rom[loROMHeader+0x16] = 0x25
	source := New(rom)
	source.Write(0x7ff5, 1)
	source.Write(0x7ff6, 0x11)
	source.Write(0x7ff0, 0x56)
	data, err := source.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []*Cartridge{source, New(rom)} {
		ram := &target.RAM[0]
		chip := target.obc1
		target.Write(0x7ff5, 0)
		target.Write(0x7ff6, 0x20)
		if err := target.Unserialize(data); err != nil {
			t.Fatal(err)
		}
		if &target.RAM[0] != ram || target.obc1 != chip {
			t.Fatal("restore replaced borrowed memory or device")
		}
		if got := target.Read(0x7ff0); got != 0x56 {
			t.Fatalf("indirect read = %02x, want 56", got)
		}
		target.Write(0x7ff1, 0xab)
		target.Write(0x7ff4, 3)
		if target.RAM[0x1845] != 0xab || target.RAM[0x1a04] != 0x0c {
			t.Fatal("restored base, address or shift is incorrect")
		}
	}
}
