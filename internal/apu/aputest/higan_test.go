package aputest

import "testing"

func TestHiganSPC700ROMMetadata(t *testing.T) {
	roms := HiganSPC700ROMs()
	if got := len(roms); got != 7 {
		t.Fatalf("ROM count = %d, want 7", got)
	}

	seen := make(map[string]bool)
	for _, rom := range roms {
		if seen[rom.Name] {
			t.Fatalf("duplicate ROM name %q", rom.Name)
		}
		seen[rom.Name] = true
		if rom.ROMFilename == "" {
			t.Fatalf("%s has empty ROM filename", rom.Name)
		}
		if rom.SourcePathHint == "" {
			t.Fatalf("%s has empty source path hint", rom.Name)
		}
		if len(rom.SHA256) != 64 {
			t.Fatalf("%s SHA-256 length = %d, want 64", rom.Name, len(rom.SHA256))
		}
		if rom.PassCount == 0 {
			t.Fatalf("%s pass count = 0", rom.Name)
		}
		if int(rom.FinalPassPort0) != rom.PassCount {
			t.Fatalf("%s final CPUIO0 pass marker = %02X, want %02X", rom.Name, rom.FinalPassPort0, rom.PassCount)
		}
	}
}
