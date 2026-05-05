package dsp

import "testing"

func TestDSPStatePreservesModulationNoiseAndEchoState(t *testing.T) {
	d := New()
	d.Write(0x2D, 0xFF)
	d.Write(0x3D, 0xA5)
	d.Write(0x0F, 0x7F)
	d.Write(0x1F, 0x80)
	d.noise = 0x1234
	d.noiseCounter = 17
	d.echoHist[3][0] = 0x1111
	d.echoHist[3][1] = -0x2222
	d.echoHistPos = 3

	state := d.SaveState()
	restored := New()
	restored.LoadState(state)

	if got := restored.PMON; got != 0xFE {
		t.Fatalf("PMON after LoadState = %02X, want FE", got)
	}
	if got := restored.NON; got != 0xA5 {
		t.Fatalf("NON after LoadState = %02X, want A5", got)
	}
	if !restored.Voices[0].useNoise || !restored.Voices[2].useNoise || restored.Voices[1].useNoise {
		t.Fatalf("voice noise flags not preserved: v0=%v v1=%v v2=%v",
			restored.Voices[0].useNoise, restored.Voices[1].useNoise, restored.Voices[2].useNoise)
	}
	if restored.FIR[0] != 0x7F || restored.FIR[1] != -0x80 {
		t.Fatalf("FIR after LoadState = %d,%d, want 127,-128", restored.FIR[0], restored.FIR[1])
	}
	if restored.noise != 0x1234 || restored.noiseCounter != 17 {
		t.Fatalf("noise state after LoadState = %04X/%d, want 1234/17", restored.noise, restored.noiseCounter)
	}
	if restored.echoHistPos != 3 || restored.echoHist[3][0] != 0x1111 || restored.echoHist[3][1] != -0x2222 {
		t.Fatalf("echo history after LoadState = pos %d sample %d,%d",
			restored.echoHistPos, restored.echoHist[3][0], restored.echoHist[3][1])
	}
}
