//go:build parity_diagnostic

package parity

import (
	"os"
	"testing"

	"github.com/tmc/snes"
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
	if got := goSys.Bus.Read(0x7E0001); got != 0x38 {
		t.Fatalf("SPCTimer Go result WRAM $0001=%02X, want 38", got)
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

func TestSPCTimerFDReadPlacementAnchor(t *testing.T) {
	tracePath := os.Getenv("BSNES_SPCTIMER_TRACE")
	if tracePath == "" {
		tracePath = "/tmp/spctimer-bsnes.jsonl"
	}
	if _, err := os.Stat(tracePath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("SPCTimer bsnes trace %s is missing; run with BSNES_SPCTIMER_TRACE=/tmp/spctimer-bsnes.jsonl", tracePath)
		}
		t.Fatal(err)
	}
	ev, sha, ok := findSPCTimerTraceEvent(t, tracePath, func(ev spcTimerTraceEvent) bool {
		return ev.Frame == 11 &&
			ev.CPUAPUCycle == 4044240 &&
			ev.Event == "smp-read" &&
			ev.SPCPC == "04c5" &&
			ev.Addr == "00fd" &&
			ev.Data == "02" &&
			ev.APURAMDCDF == "4810237c"
	})
	if sha != bsnesSPCTimerTraceSHA256 {
		t.Fatalf("SPCTimer trace sha256 = %s, want %s", sha, bsnesSPCTimerTraceSHA256)
	}
	if !ok {
		t.Fatalf("SPCTimer trace %s lacks the frame 11 PC $04C5 $FD read-placement anchor", tracePath)
	}
	if ev.T0.Stage0 != 100 || ev.T0.Stage1 != 1 || ev.T0.Stage2 != 0 || ev.T0.Stage3 != 2 || ev.T0.Target != 1 {
		t.Fatalf("SPCTimer $04C5 t0 = stage0/%d stage1/%d stage2/%d stage3/%d target/%d, want 100/1/0/2/1",
			ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
	}
	t.Logf("SPCTimer read-placement anchor: frame=%d cpu_cycle=%d spc_pc=%s addr=%s data=%s apuram_dc_df=%s t0=%d/%d/%d/%d/%d",
		ev.Frame, ev.CPUAPUCycle, ev.SPCPC, ev.Addr, ev.Data, ev.APURAMDCDF,
		ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
}

func TestSPCTimerNextReferenceAnchor(t *testing.T) {
	tracePath := os.Getenv("BSNES_SPCTIMER_TRACE")
	if tracePath == "" {
		tracePath = "/tmp/spctimer-bsnes.jsonl"
	}
	if _, err := os.Stat(tracePath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("SPCTimer bsnes trace %s is missing; run with BSNES_SPCTIMER_TRACE=/tmp/spctimer-bsnes.jsonl", tracePath)
		}
		t.Fatal(err)
	}
	ev, sha, ok := findSPCTimerTraceEvent(t, tracePath, func(ev spcTimerTraceEvent) bool {
		return ev.Frame == 13 &&
			ev.CPUAPUCycle == 4805564 &&
			ev.Event == "smp-read" &&
			ev.SPCPC == "0749" &&
			ev.Addr == "00fd" &&
			ev.Data == "01" &&
			ev.APURAMDCDF == "08370624"
	})
	if sha != bsnesSPCTimerTraceSHA256 {
		t.Fatalf("SPCTimer trace sha256 = %s, want %s", sha, bsnesSPCTimerTraceSHA256)
	}
	if !ok {
		t.Fatalf("SPCTimer trace %s lacks the frame 13 PC $0749 $FD post-fix anchor", tracePath)
	}
	if ev.T0.Stage0 != 36 || ev.T0.Stage1 != 0 || ev.T0.Stage2 != 1 || ev.T0.Stage3 != 1 || ev.T0.Target != 2 {
		t.Fatalf("SPCTimer $0749 t0 = stage0/%d stage1/%d stage2/%d stage3/%d target/%d, want 36/0/1/1/2",
			ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
	}
	t.Logf("SPCTimer post-fix reference anchor: frame=%d cpu_cycle=%d spc_pc=%s addr=%s data=%s apuram_dc_df=%s t0=%d/%d/%d/%d/%d",
		ev.Frame, ev.CPUAPUCycle, ev.SPCPC, ev.Addr, ev.Data, ev.APURAMDCDF,
		ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
}

func TestSPCTimerResolvedDownstreamReferenceAnchor(t *testing.T) {
	tracePath := os.Getenv("BSNES_SPCTIMER_TRACE")
	if tracePath == "" {
		tracePath = "/tmp/spctimer-bsnes.jsonl"
	}
	if _, err := os.Stat(tracePath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("SPCTimer bsnes trace %s is missing; run with BSNES_SPCTIMER_TRACE=/tmp/spctimer-bsnes.jsonl", tracePath)
		}
		t.Fatal(err)
	}
	ev, sha, ok := findSPCTimerTraceEvent(t, tracePath, func(ev spcTimerTraceEvent) bool {
		return ev.Frame == 13 &&
			ev.CPUAPUCycle == 4830146 &&
			ev.Event == "smp-read" &&
			ev.SPCPC == "0749" &&
			ev.Addr == "00fd" &&
			ev.Data == "02" &&
			ev.APURAMDCDF == "3f8e215f"
	})
	if sha != bsnesSPCTimerTraceSHA256 {
		t.Fatalf("SPCTimer trace sha256 = %s, want %s", sha, bsnesSPCTimerTraceSHA256)
	}
	if !ok {
		t.Fatalf("SPCTimer trace %s lacks the frame 13 PC $0749 $FD resolved downstream anchor", tracePath)
	}
	if ev.T0.Stage0 != 38 || ev.T0.Stage1 != 0 || ev.T0.Stage2 != 0 || ev.T0.Stage3 != 2 || ev.T0.Target != 2 {
		t.Fatalf("SPCTimer $0749 next-divergence t0 = stage0/%d stage1/%d stage2/%d stage3/%d target/%d, want 38/0/0/2/2",
			ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
	}
	t.Logf("SPCTimer resolved downstream reference anchor: frame=%d cpu_cycle=%d spc_pc=%s addr=%s data=%s apuram_dc_df=%s t0=%d/%d/%d/%d/%d",
		ev.Frame, ev.CPUAPUCycle, ev.SPCPC, ev.Addr, ev.Data, ev.APURAMDCDF,
		ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
}

func TestSPCTimerDelayHelperReferenceAnchor(t *testing.T) {
	tracePath := os.Getenv("BSNES_SPCTIMER_TRACE")
	if tracePath == "" {
		tracePath = "/tmp/spctimer-bsnes.jsonl"
	}
	if _, err := os.Stat(tracePath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("SPCTimer bsnes trace %s is missing; run with BSNES_SPCTIMER_TRACE=/tmp/spctimer-bsnes.jsonl", tracePath)
		}
		t.Fatal(err)
	}
	ev, sha, ok := findSPCTimerTraceEvent(t, tracePath, func(ev spcTimerTraceEvent) bool {
		return ev.Frame == 13 &&
			ev.CPUAPUCycle == 4830146 &&
			ev.Event == "smp-read" &&
			ev.SPCPC == "04c5" &&
			ev.Addr == "00fd" &&
			ev.Data == "03" &&
			ev.APURAMDCDF == "3f8e215f"
	})
	if sha != bsnesSPCTimerTraceSHA256 {
		t.Fatalf("SPCTimer trace sha256 = %s, want %s", sha, bsnesSPCTimerTraceSHA256)
	}
	if !ok {
		t.Fatalf("SPCTimer trace %s lacks the frame 13 PC $04C5 $FD delay-helper anchor", tracePath)
	}
	if ev.T0.Stage0 != 2 || ev.T0.Stage1 != 0 || ev.T0.Stage2 != 0 || ev.T0.Stage3 != 3 || ev.T0.Target != 1 {
		t.Fatalf("SPCTimer $04C5 delay-helper t0 = stage0/%d stage1/%d stage2/%d stage3/%d target/%d, want 2/0/0/3/1",
			ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
	}
	t.Logf("SPCTimer delay-helper reference anchor: frame=%d cpu_cycle=%d spc_pc=%s addr=%s data=%s apuram_dc_df=%s t0=%d/%d/%d/%d/%d",
		ev.Frame, ev.CPUAPUCycle, ev.SPCPC, ev.Addr, ev.Data, ev.APURAMDCDF,
		ev.T0.Stage0, ev.T0.Stage1, ev.T0.Stage2, ev.T0.Stage3, ev.T0.Target)
}

func TestSPCTimerNextGoPublicProbeAnchor(t *testing.T) {
	tc, ok := higanManifestCase(t, "SPCTimer")
	if !ok {
		t.Fatalf("%s has no SPCTimer row", higanTestROMManifestPath)
	}
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()

	const cyclesPerFrame = uint64(357366)
	var hits []spcTimerGoProbeHit
	for frame := 0; frame < 8 && len(hits) < 2; frame++ {
		frameEnd := sys.CPU.Cycles + cyclesPerFrame
		for sys.CPU.Cycles < frameEnd && len(hits) < 2 {
			start := sys.CPU.Cycles
			sys.CPU.Run()
			if sys.CPU.Cycles == start {
				sys.Scheduler.AddCycles(2)
			}
			sys.Scheduler.SyncTo(sys.APU, sys.CPU.Cycles)
			if sys.APU.Processor.PC != 0x0749 {
				continue
			}
			if sys.APU.RAM[0x00dc] != 0x08 || sys.APU.RAM[0x00dd] != 0x37 || sys.APU.RAM[0x00de] != 0x06 || sys.APU.RAM[0x00df] != 0x24 {
				continue
			}
			state := sys.APU.SaveState()
			hit := spcTimerGoProbeHit{
				Frame:      frame,
				CPUCycle:   sys.CPU.Cycles,
				APUCycle:   sys.APU.GetCycles(),
				A:          sys.APU.Processor.A,
				Timer0Div:  state.Timers[0].Divider,
				Timer0St2:  state.Timers[0].Stage2,
				Timer0:     state.Timers[0].Counter,
				Timer0Goal: state.Timers[0].Target,
				Out0:       sys.APU.OutPorts[0],
				Out1:       sys.APU.OutPorts[1],
				Out2:       sys.APU.OutPorts[2],
				Out3:       sys.APU.OutPorts[3],
			}
			if len(hits) == 0 || hits[len(hits)-1].CPUCycle != hit.CPUCycle || hits[len(hits)-1].A != hit.A {
				hits = append(hits, hit)
			}
		}
	}
	if len(hits) < 2 {
		t.Fatalf("SPCTimer Go probe did not reach two PC $0749 APURAM 08370624 observations; got %d", len(hits))
	}
	if hits[0].A != 0x05 || hits[1].A != 0x01 {
		t.Fatalf("SPCTimer Go PC $0749 APURAM 08370624 A sequence = %02X,%02X, want 05,01", hits[0].A, hits[1].A)
	}
	if hits[0].Timer0Div != 18 || hits[0].Timer0St2 != 1 || hits[0].Timer0 != 1 || hits[0].Timer0Goal != 2 {
		t.Fatalf("SPCTimer Go PC $0749 APURAM 08370624 timer0 = divider/%d stage2/%d counter/%d target/%d, want 18/1/1/2",
			hits[0].Timer0Div, hits[0].Timer0St2, hits[0].Timer0, hits[0].Timer0Goal)
	}
	t.Logf("SPCTimer Go post-fix public probe: first hit frame=%d cpu=%d apu=%d A=%02x t0=%d/%d/%d/%02x out=%02x%02x%02x%02x; second A=%02x cpu=%d",
		hits[0].Frame, hits[0].CPUCycle, hits[0].APUCycle, hits[0].A,
		hits[0].Timer0Div, hits[0].Timer0St2, hits[0].Timer0, hits[0].Timer0Goal,
		hits[0].Out0, hits[0].Out1, hits[0].Out2, hits[0].Out3, hits[1].A, hits[1].CPUCycle)
}

func TestSPCTimerResolvedDownstreamGoPublicProbeAnchor(t *testing.T) {
	tc, ok := higanManifestCase(t, "SPCTimer")
	if !ok {
		t.Fatalf("%s has no SPCTimer row", higanTestROMManifestPath)
	}
	rom, err := os.ReadFile(tc.Path)
	if err != nil {
		t.Fatal(err)
	}
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(rom); err != nil {
		t.Fatal(err)
	}
	sys.Power()

	const cyclesPerFrame = uint64(357366)
	var hit spcTimerGoProbeHit
	found := false
	for frame := 0; frame < 8 && !found; frame++ {
		frameEnd := sys.CPU.Cycles + cyclesPerFrame
		for sys.CPU.Cycles < frameEnd && !found {
			start := sys.CPU.Cycles
			sys.CPU.Run()
			if sys.CPU.Cycles == start {
				sys.Scheduler.AddCycles(2)
			}
			sys.Scheduler.SyncTo(sys.APU, sys.CPU.Cycles)
			if sys.APU.Processor.PC != 0x0749 {
				continue
			}
			if sys.APU.RAM[0x00dc] != 0x3f || sys.APU.RAM[0x00dd] != 0x8e || sys.APU.RAM[0x00de] != 0x21 || sys.APU.RAM[0x00df] != 0x5f {
				continue
			}
			state := sys.APU.SaveState()
			hit = spcTimerGoProbeHit{
				Frame:      frame,
				CPUCycle:   sys.CPU.Cycles,
				APUCycle:   sys.APU.GetCycles(),
				A:          sys.APU.Processor.A,
				Timer0Div:  state.Timers[0].Divider,
				Timer0St2:  state.Timers[0].Stage2,
				Timer0:     state.Timers[0].Counter,
				Timer0Goal: state.Timers[0].Target,
				Out0:       sys.APU.OutPorts[0],
				Out1:       sys.APU.OutPorts[1],
				Out2:       sys.APU.OutPorts[2],
				Out3:       sys.APU.OutPorts[3],
			}
			found = true
		}
	}
	if !found {
		t.Fatal("SPCTimer Go probe did not reach PC $0749 APURAM 3f8e215f")
	}
	if hit.A != 0x02 {
		t.Fatalf("SPCTimer Go PC $0749 APURAM 3f8e215f A = %02X, want fixed value 02", hit.A)
	}
	if hit.Timer0Div != 20 || hit.Timer0St2 != 0 || hit.Timer0 != 0 || hit.Timer0Goal != 2 {
		t.Fatalf("SPCTimer Go PC $0749 APURAM 3f8e215f timer0 = divider/%d stage2/%d counter/%d target/%d, want 20/0/0/2",
			hit.Timer0Div, hit.Timer0St2, hit.Timer0, hit.Timer0Goal)
	}
	t.Logf("SPCTimer Go resolved downstream public probe: frame=%d cpu=%d apu=%d A=%02x t0=%d/%d/%d/%02x out=%02x%02x%02x%02x",
		hit.Frame, hit.CPUCycle, hit.APUCycle, hit.A, hit.Timer0Div, hit.Timer0St2, hit.Timer0, hit.Timer0Goal,
		hit.Out0, hit.Out1, hit.Out2, hit.Out3)
}

type spcTimerGoProbeHit struct {
	Frame      int
	CPUCycle   uint64
	APUCycle   uint64
	A          byte
	Timer0Div  uint16
	Timer0St2  byte
	Timer0     byte
	Timer0Goal byte
	Out0       byte
	Out1       byte
	Out2       byte
	Out3       byte
}
