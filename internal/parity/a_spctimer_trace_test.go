package parity

import (
	"os"
	"testing"

	"github.com/tmc/snes/internal/parity/libretro/bsnes"
)

func TestASPCTimerBsnesReferenceTrace(t *testing.T) {
	tracePath := os.Getenv("BSNES_SPCTIMER_TRACE")
	if tracePath == "" {
		t.Skip("set BSNES_SPCTIMER_TRACE=/tmp/spctimer-bsnes.jsonl with patched bsnes libretro commit 1f6b30251ec153cf0ad61e5fee9f1fb415a8a4e7 to run the SPCTimer reference trace gate")
	}
	corePath := bsnes.DefaultPath()
	checkFile(t, corePath)
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes(core); got != bsnesSPCTimerTraceCoreSHA256 {
		t.Skipf("BSNES_SPCTIMER_TRACE requires patched bsnes core %s sha256 %s, got %s", corePath, bsnesSPCTimerTraceCoreSHA256, got)
	}

	tc, ok := higanManifestCase(t, "SPCTimer")
	if !ok {
		t.Fatalf("%s has no SPCTimer row", higanTestROMManifestPath)
	}
	checkFile(t, tc.Path)
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes(rom); got != tc.SHA256 {
		t.Fatalf("%s sha256 = %s, want %s", tc.Path, got, tc.SHA256)
	}
	if err := os.Remove(tracePath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	goSys := runHiganGoSystem(t, rom, tc.Frames)
	bsn := runHiganReference(t, corePath, tc.Path, tc.Frames)
	if got := goSys.Bus.Read(0x7E0001); got != 0x10 {
		t.Fatalf("SPCTimer Go result WRAM $0001=%02X, want current diagnostic value 10", got)
	}
	if got := bsn.PeekWRAM(1); got != 0x38 {
		t.Fatalf("SPCTimer bsnes trace result WRAM $0001=%02X, want 38", got)
	}

	summary := readSPCTimerBsnesTrace(t, tracePath)
	if !summary.nonzeroFDRead {
		t.Fatalf("SPCTimer trace %s has no nonzero S-SMP $00FD timer read", tracePath)
	}
	if !summary.apuramDCDFSignal {
		t.Fatalf("SPCTimer trace %s has no APURAM $00DC-$00DF d06cd0a9 signal", tracePath)
	}
	if !summary.cpuAPUPort {
		t.Fatalf("SPCTimer trace %s has no S-CPU $2140-$2143 APU port event", tracePath)
	}
	if !summary.cpu2141Read0A {
		t.Fatalf("SPCTimer trace %s has no S-CPU $2141 read of 0a", tracePath)
	}
	if !summary.smpPort {
		t.Fatalf("SPCTimer trace %s has no S-SMP $00F4-$00F7 port event", tracePath)
	}
	if !summary.smpF5Write50 {
		t.Fatalf("SPCTimer trace %s has no S-SMP $00F5 write of 50", tracePath)
	}
	t.Logf("SPCTimer bsnes trace %s rows=%d sha256=%s first_nonzero_fd_frame=%d first_cpu_port_frame=%d first_smp_port_frame=%d",
		tracePath, summary.rows, summary.sha256, summary.firstNonzeroFDFrame, summary.firstCPUPortFrame, summary.firstSMPPortFrame)
}
