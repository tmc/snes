package framecap_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/framecap"
	"github.com/tmc/snes/internal/ppu"
	"github.com/tmc/snes/internal/trace"
)

func TestSidecar_OverlapSceneCaptureAndGates(t *testing.T) {
	sceneRoot := "/Users/tmc/tmp/snes-auto-jpdasm/20261003-direction-review/overlap-scene"
	variants := []string{"baseline", "no_op", "intervention", "restored"}

	for _, v := range variants {
		romPath := filepath.Join(sceneRoot, v, "fixture.sfc")
		rom, err := os.ReadFile(romPath)
		if err != nil {
			t.Skipf("overlap-scene fixture %s not available: %v", romPath, err)
			return
		}

		// Run once with layer tracing enabled, and once disabled
		runCapture := func(enabled bool) (receipt framecap.Receipt, records []framecap.Record, sidecars []framecap.Sidecar, err error) {
			dir := t.TempDir()
			sys := snes.NewSystem(nil)
			if err := sys.LoadROM(rom); err != nil {
				return receipt, nil, nil, err
			}
			sys.Power()
			if enabled {
				sys.PPU.EnableLayerTrace(true)
			}
			romSum := sha256.Sum256(rom)
			fw, err := framecap.Create(framecap.Options{
				Dir:        dir,
				LayerTrace: enabled,
				Run: &trace.RunInfo{
					ROMSHA256:      hex.EncodeToString(romSum[:]),
					EngineRevision: "cf54e3d5b03f0b2f567bf63297a7d4db026eebe6",
					Mapper:         "lorom",
				},
			})
			if err != nil {
				return receipt, nil, nil, err
			}
			stop, err := sys.CaptureFrames(fw.Keep, fw.Frame)
			if err != nil {
				return receipt, nil, nil, err
			}
			defer stop()

			for i := 0; i < 3; i++ {
				if err := sys.Run(); err != nil {
					return receipt, nil, nil, err
				}
			}
			rc, err := fw.Close("complete")
			if err != nil {
				return receipt, nil, nil, err
			}

			// Read manifest
			mBytes, err := os.ReadFile(filepath.Join(dir, framecap.ManifestName))
			if err != nil {
				return receipt, nil, nil, err
			}
			lines := []string{}
			for _, l := range splitLines(string(mBytes)) {
				if l != "" {
					lines = append(lines, l)
				}
			}
			var recs []framecap.Record
			var scs []framecap.Sidecar
			for _, l := range lines[1:] {
				var r framecap.Record
				if err := json.Unmarshal([]byte(l), &r); err != nil {
					return receipt, nil, nil, err
				}
				recs = append(recs, r)
				if r.Sidecar != "" {
					scBytes, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(r.Sidecar)))
					if err != nil {
						return receipt, nil, nil, err
					}
					var sc framecap.Sidecar
					if err := json.Unmarshal(scBytes, &sc); err != nil {
						return receipt, nil, nil, err
					}
					scs = append(scs, sc)
				}
			}
			return rc, recs, scs, nil
		}

		rcTraced, recsTraced, scs, err := runCapture(true)
		if err != nil {
			t.Fatalf("traced capture %s: %v", v, err)
		}
		rcPlain, recsPlain, _, err := runCapture(false)
		if err != nil {
			t.Fatalf("plain capture %s: %v", v, err)
		}

		if len(recsTraced) != 3 || len(recsPlain) != 3 {
			t.Fatalf("variant %s: got %d traced frames and %d plain frames, want 3", v, len(recsTraced), len(recsPlain))
		}
		if len(scs) != 3 {
			t.Fatalf("variant %s: got %d sidecars, want 3", v, len(scs))
		}

		// Gate 1: Trace-on vs trace-off equality of pixels, Number, Start, VBlank, ContentID
		for i := 0; i < 3; i++ {
			rt := recsTraced[i]
			rp := recsPlain[i]
			if rt.Number != rp.Number || rt.Start != rp.Start || rt.VBlank != rp.VBlank {
				t.Errorf("frame %d boundary mismatch: traced=(%d, %d, %d), plain=(%d, %d, %d)",
					i, rt.Number, rt.Start, rt.VBlank, rp.Number, rp.Start, rp.VBlank)
			}
			if rt.ContentID != rp.ContentID {
				t.Errorf("frame %d ContentID mismatch: traced=%s, plain=%s", i, rt.ContentID, rp.ContentID)
			}
		}

		// Gate 2: Frame 1 diagnostics at (101, 51), (105, 51), (109, 51)
		f1 := scs[1]
		if !f1.Supported {
			t.Fatalf("variant %s frame 1 marked unsupported: %s", v, f1.UnsupportedReason)
		}
		sampleAt := func(x, y int) (uint8, uint8, bool) {
			idx := y*f1.Width + x
			return f1.Sources[idx], f1.Palettes[idx], f1.KnownMask[idx]
		}

		s101, p101, k101 := sampleAt(101, 51)
		if s101 != ppu.SourceOBJ1 || p101 != 129 || !k101 {
			t.Errorf("variant %s (101, 51): got source=%d pal=%d known=%v; want OBJ1(64), pal=129, known=true", v, s101, p101, k101)
		}

		s105, p105, k105 := sampleAt(105, 51)
		if s105 != ppu.SourceOBJ1 || p105 != 145 || !k105 {
			t.Errorf("variant %s (105, 51): got source=%d pal=%d known=%v; want OBJ1(64), pal=145, known=true", v, s105, p105, k105)
		}

		s109, p109, k109 := sampleAt(109, 51)
		if s109 != ppu.SourceBackdrop || p109 != 0 || !k109 {
			t.Errorf("variant %s (109, 51): got source=%d pal=%d known=%v; want Backdrop(32), pal=0, known=true", v, s109, p109, k109)
		}

		// Gate 5: Frame 0 zero cells remain unknown
		f0 := scs[0]
		unknownCells := 0
		for idx, src := range f0.Sources {
			if src == 0 && !f0.KnownMask[idx] {
				unknownCells++
			}
		}
		if unknownCells != 7424 {
			t.Errorf("variant %s frame 0: got %d unknown forced blank cells, want 7424", v, unknownCells)
		}

		_ = rcTraced
		_ = rcPlain
	}
}

func splitLines(s string) []string {
	var lines []string
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, cur)
			cur = ""
		} else {
			cur += string(s[i])
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
