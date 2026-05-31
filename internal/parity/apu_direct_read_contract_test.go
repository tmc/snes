package parity

import (
	"os"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/apu"
)

// TestAPUDirectReadPendingPublishContract is an opt-in canary for the
// direct S-CPU $2140-$2143 read contract suggested by local reference probes.
//
// Enable with:
//
//	SNES_APU_DIRECT_READ_CONTRACT=1 go test ./internal/parity -run '^TestAPUDirectReadPendingPublishContract$' -count=1 -v
//
// It guards the already-pending publish case: IODevice.Read must not let
// SyncPortRead publish a queued APU output-port byte before ReadPort samples
// OutPorts.
func TestAPUDirectReadPendingPublishContract(t *testing.T) {
	if os.Getenv("SNES_APU_DIRECT_READ_CONTRACT") == "" {
		t.Skip("set SNES_APU_DIRECT_READ_CONTRACT=1 to run")
	}

	sys := snes.NewSystem(nil)
	prepareAPUDirectReadPendingOutput(t, sys, 0x5A)

	if got := sys.APU.ReadPort(0); got != 0x00 {
		t.Fatalf("port before CPU read = %02X, want old value 00", got)
	}

	sys.CPU.Cycles = 32
	first := sys.Bus.Read(0x002140)
	if first != 0x00 {
		t.Fatalf("first direct $2140 read = %02X, want old value 00 before pending publish", first)
	}
	if got := sys.APU.ReadPort(0); got != 0x00 {
		t.Fatalf("port after first direct read = %02X, want pending publish still hidden", got)
	}

	sys.CPU.Cycles += 12
	sys.Scheduler.Sync(sys.APU)
	second := sys.Bus.Read(0x002140)
	if second != 0x5A {
		t.Fatalf("later $2140 read after safety sync = %02X, want published value 5A", second)
	}
}

func prepareAPUDirectReadPendingOutput(t *testing.T, sys *snes.System, value uint8) {
	t.Helper()

	sys.APU.Control = 0
	sys.APU.Processor.PC = 0x0200
	sys.APU.Processor.A = value
	sys.APU.RAM[0x0200] = 0xC4 // MOV dp,A
	sys.APU.RAM[0x0201] = 0xF4

	result := sys.APU.RunUntilTarget(4, apu.SyncPostCPU)
	if result.Yield != apu.YieldAPUPortWrite {
		t.Fatalf("test setup yield = %d, want %d", result.Yield, apu.YieldAPUPortWrite)
	}
}
