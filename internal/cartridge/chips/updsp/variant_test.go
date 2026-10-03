package updsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVariantString(t *testing.T) {
	cases := []struct {
		v    Variant
		want string
	}{
		{VariantUnknown, "unknown"},
		{VariantDSP1, "DSP-1"},
		{VariantDSP1A, "DSP-1A"},
		{VariantDSP1B, "DSP-1B"},
		{VariantDSP2, "DSP-2"},
		{VariantDSP3, "DSP-3"},
		{VariantDSP4, "DSP-4"},
	}
	for _, tc := range cases {
		if got := tc.v.String(); got != tc.want {
			t.Errorf("Variant(%d).String()=%q, want %q", tc.v, got, tc.want)
		}
	}
}

// TestLoad_Passthrough: nil ROMs yield a loader with the core operational
// but LoadedOK=false, so the CPU bus still sees deterministic RQM behaviour.
func TestLoad_Passthrough(t *testing.T) {
	l, err := Load(VariantDSP1, nil, nil)
	if err != nil {
		t.Fatalf("Load(nil,nil): %v", err)
	}
	if l.LoadedOK {
		t.Errorf("LoadedOK should be false for passthrough")
	}
	if l.Core == nil || l.IO == nil {
		t.Errorf("Core/IO must be non-nil even in passthrough")
	}
	if l.IO.ReadSR()&0x80 == 0 {
		t.Errorf("passthrough: RQM should be asserted so CPU doesn't deadlock")
	}
}

// TestLoad_WrongSize: a wrong-sized ROM yields a clear error.
func TestLoad_WrongSize(t *testing.T) {
	if _, err := Load(VariantDSP1, make([]byte, 100), nil); err == nil {
		t.Errorf("Load with wrong-size prog ROM: expected error")
	}
	if _, err := Load(VariantDSP1, nil, make([]byte, 100)); err == nil {
		t.Errorf("Load with wrong-size data ROM: expected error")
	}
}

// TestLoad_CorrectSize: correctly sized buffers load without error.
func TestLoad_CorrectSize(t *testing.T) {
	wantPrg, wantData := VariantDSP1.ROMSize()
	prg := make([]byte, wantPrg)
	drom := make([]byte, wantData)
	// Poison a few words so we can verify the loader actually wrote them in.
	prg[0], prg[1], prg[2] = 0xAB, 0xCD, 0xEF
	drom[0], drom[1] = 0x12, 0x34

	l, err := Load(VariantDSP1, prg, drom)
	if err != nil {
		t.Fatalf("Load(prg,drom): %v", err)
	}
	if !l.LoadedOK {
		t.Errorf("LoadedOK should be true")
	}
	if got := l.Core.PRG[0]; got != 0xABCDEF {
		t.Errorf("PRG[0]=%#06x, want 0xABCDEF", got)
	}
	if got := l.Core.DROM[0]; got != 0x1234 {
		t.Errorf("DROM[0]=%#04x, want 0x1234", got)
	}
}

func TestLoadWithEnvCombinedROM(t *testing.T) {
	wantPrg, wantData := VariantDSP1.ROMSize()
	rom := make([]byte, wantPrg+wantData)
	rom[0], rom[1], rom[2] = 0x12, 0x34, 0x56
	rom[wantPrg], rom[wantPrg+1] = 0xab, 0xcd
	path := filepath.Join(t.TempDir(), "dsp1.rom")
	if err := os.WriteFile(path, rom, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SNES_DSP1_ROM", path)
	t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
	t.Setenv("SNES_DSP1_DATA_ROM", "")

	l, err := LoadWithEnv(VariantDSP1)
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	if !l.LoadedOK {
		t.Fatal("LoadedOK = false, want true")
	}
	if l.Core.PRG[0] != 0x123456 {
		t.Fatalf("PRG[0] = %06X, want 123456", l.Core.PRG[0])
	}
	if l.Core.DROM[0] != 0xabcd {
		t.Fatalf("DROM[0] = %04X, want ABCD", l.Core.DROM[0])
	}
}

func TestLoadWithEnvMissingROM(t *testing.T) {
	t.Setenv("SNES_DSP1_ROM", "")
	t.Setenv("SNES_DSP1_PROGRAM_ROM", "")
	t.Setenv("SNES_DSP1_DATA_ROM", "")

	if _, err := LoadWithEnv(VariantDSP1); err != ErrROMMissing {
		t.Fatalf("LoadWithEnv missing ROM error = %v, want %v", err, ErrROMMissing)
	}
}

// TestMultiplyVector: DSP-1 matrix math relies on the K/L multiplier sampling
// at instruction commit. A MOV-then-OP sequence that loads K=100, L=50 must
// yield M/N equal to (100*50)*2 = 10000 shifted into the 31-bit product.
//
// This is the "one known DSP-1 matrix-math vector" smoke test from the Phase
// 11 acceptance criteria. It runs against a synthetic micro-program, not the
// proprietary DSP-1 ROM, so it is safe to commit.
func TestMultiplyVector(t *testing.T) {
	c := NewCore()
	// LD K, 100
	c.PRG[0] = (uint32(classLD) << 22) | (uint32(100) << 6) | uint32(regK)
	// LD L, 50
	c.PRG[1] = (uint32(classLD) << 22) | (uint32(50) << 6) | uint32(regL)
	// OP NOP: sampling point for the multiplier.
	c.PRG[2] = 0
	c.Step(3)

	wantProd := int32(100) * int32(50) * 2 // 10000
	got := (uint32(c.M) << 16) | uint32(c.N)
	if int32(got) != wantProd {
		t.Errorf("K*L*2: M:N=%#08x (%d), want %#08x (%d)", got, int32(got), uint32(wantProd), wantProd)
	}
}
