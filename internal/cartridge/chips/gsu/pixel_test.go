package gsu

import "testing"

// TestPixelCacheDeferredCommit exercises the design_doc.md §5 quirk:
// PLOTting eight pixels into the same tile row must NOT commit; plotting
// into a different tile row commits the first row exactly once.
func TestPixelCacheDeferredCommit(t *testing.T) {
	d := New(nil, nil)
	d.COLR = 0x01

	// Plot 8 pixels at y=0 across x=0..7. These all land in the same
	// CBR-derived row, so no commit should fire.
	for x := uint16(0); x < 8; x++ {
		d.plot(x, 0, d.COLR)
	}
	if got := d.Commits(); got != 0 {
		t.Fatalf("unexpected commits during same-row plots: %d", got)
	}

	// Plot one pixel on the next tile row (y=8). That triggers the flush
	// of the first row; the new plot is now cached but not yet committed.
	d.plot(0, 8, d.COLR)
	if got := d.Commits(); got != 1 {
		t.Fatalf("row change should flush once, got %d commits", got)
	}

	// Flush one more time via Stop to confirm the second row's content.
	d.Stop()
	if got := d.Commits(); got != 2 {
		t.Fatalf("after Stop commits=%d, want 2", got)
	}

	// Shadow commits include both rows with the colour we drew.
	commits := d.ShadowCommits()
	if len(commits) != 2 {
		t.Fatalf("shadow commits = %d, want 2", len(commits))
	}
}

// TestPixelCacheNoCommitWithoutPlot — Stop on an empty cache must not
// record a bogus commit.
func TestPixelCacheNoCommitWithoutPlot(t *testing.T) {
	d := New(nil, nil)
	d.Go()
	d.Stop()
	if d.Commits() != 0 {
		t.Fatalf("empty cache committed: %d", d.Commits())
	}
}

// TestRpixReturnsCacheValue — RPIX must return the live cache value, not
// RAM, for a pixel that hasn't been flushed yet. This is the "PLOT then
// RPIX without flush returns cache value" case from Phase 10 acceptance.
func TestRpixReturnsCacheValue(t *testing.T) {
	d := New(nil, nil)
	d.COLR = 0x42
	d.plot(3, 0, d.COLR)
	got := d.rpix(3, 0)
	if got != 0x42 {
		t.Fatalf("RPIX cache miss: got %02X want 42", got)
	}
	// No commit occurred because we stayed inside the same row.
	if d.Commits() != 0 {
		t.Fatalf("RPIX unexpectedly flushed: commits=%d", d.Commits())
	}
}

// TestRpixFlushesOnRowChange — RPIX on a different row flushes first and
// returns zero when that row has not been committed.
func TestRpixFlushesOnRowChange(t *testing.T) {
	d := New(nil, nil)
	d.COLR = 0x11
	d.plot(0, 0, d.COLR)
	got := d.rpix(0, 8) // different row
	if got != 0 {
		t.Errorf("RPIX cross-row got %02X want 00", got)
	}
	if d.Commits() != 1 {
		t.Fatalf("cross-row RPIX must flush: commits=%d", d.Commits())
	}
}

func TestRpixReadsCommittedRow(t *testing.T) {
	d := New(nil, nil)
	d.SCMR = 0x03
	d.plot(2, 0, 0x0c)
	d.flushPixelCache()

	got := d.rpix(2, 0)
	if got != 0x0c {
		t.Fatalf("RPIX committed row got %02X want 0C", got)
	}
	if d.Commits() != 1 {
		t.Fatalf("RPIX committed row flushed again: commits=%d", d.Commits())
	}
}

func TestRpixCommittedRowSurvivesSerialize(t *testing.T) {
	d := New(nil, nil)
	d.SCMR = 0x03
	d.plot(5, 0, 0x0e)
	d.flushPixelCache()

	state, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(nil, nil)
	if err := restored.Unserialize(state); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if got := restored.rpix(5, 0); got != 0x0e {
		t.Fatalf("restored RPIX committed row got %02X want 0E", got)
	}
}

func TestRpixOpcodeReadsCachedPixelAfterSBK(t *testing.T) {
	d := New([]byte{0x90, 0x3d, 0x4c, 0x00}, nil) // SBK; ALT1; RPIX; STOP
	d.SCMR = 0x03
	d.R[1] = 4
	d.R[2] = 0
	d.plot(4, 0, 0x0d)
	d.Go()
	d.Run(3)

	if d.R[0] != 0x000d {
		t.Fatalf("RPIX after SBK R0=%04X want 000D", d.R[0])
	}
	if d.R[1] != 4 {
		t.Fatalf("RPIX after SBK incremented R1: got %d want 4", d.R[1])
	}
	if d.Commits() != 0 {
		t.Fatalf("SBK/RPIX commits=%d want 0", d.Commits())
	}
	if !d.Running() {
		t.Fatalf("SBK stopped GSU before RPIX")
	}
}

func TestFlushPixelCacheEncodesBitplanes(t *testing.T) {
	for _, tt := range []struct {
		name string
		scmr uint8
		row  [8]byte
		want map[uint16]uint8
	}{
		{
			name: "2bpp",
			scmr: 0x00,
			row:  [8]byte{0, 1, 2, 3, 0, 1, 2, 3},
			want: map[uint16]uint8{0: 0x55, 1: 0x33},
		},
		{
			name: "4bpp",
			scmr: 0x01,
			row:  [8]byte{0, 1, 2, 3, 4, 5, 6, 7},
			want: map[uint16]uint8{0: 0x55, 1: 0x33, 16: 0x0f, 17: 0x00},
		},
		{
			name: "8bpp",
			scmr: 0x03,
			row:  [8]byte{0, 1, 2, 3, 4, 5, 6, 7},
			want: map[uint16]uint8{0: 0x55, 1: 0x33, 16: 0x0f, 17: 0x00, 32: 0x00, 33: 0x00, 48: 0x00, 49: 0x00},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := New(nil, nil)
			d.SCMR = tt.scmr
			for x, color := range tt.row {
				d.plot(uint16(x), 0, color)
			}
			d.flushPixelCache()

			for off, want := range tt.want {
				if got := d.ramRead(0x700000 + uint32(off)); got != want {
					t.Fatalf("RAM bitplane[%02X]=%02X want %02X", off, got, want)
				}
			}
			for x, want := range tt.row {
				if got := d.rpix(uint16(x), 0); got != want {
					t.Fatalf("RPIX x=%d got %02X want %02X", x, got, want)
				}
			}
		})
	}
}

func TestFlushPixelCacheMergesPartialBitplaneRow(t *testing.T) {
	d := New(nil, nil)
	d.SCMR = 0x00
	d.ramWrite(0x700000, 0xaa)
	d.ramWrite(0x700001, 0xcc)
	d.plot(1, 0, 0x03)
	d.flushPixelCache()

	if got := d.ramRead(0x700000); got != 0xea {
		t.Fatalf("partial plane0=%02X want EA", got)
	}
	if got := d.ramRead(0x700001); got != 0xcc {
		t.Fatalf("partial plane1=%02X want CC", got)
	}
	if got := d.rpix(1, 0); got != 0x03 {
		t.Fatalf("partial RPIX plotted pixel=%02X want 03", got)
	}
	if got := d.rpix(0, 0); got != 0x03 {
		t.Fatalf("partial RPIX preserved pixel=%02X want 03", got)
	}
}

func TestPlotRowUsesScreenModeRegisters(t *testing.T) {
	d := New(nil, nil)
	d.SCBR = 2
	d.SCMR = 0x00 // HT=0, 2bpp.
	if got := d.plotRow(8, 8); got != 0x0910 {
		t.Fatalf("HT0 plot row = %04X want 0910", got)
	}

	d.SCMR = 0x04 // HT=1, 2bpp.
	if got := d.plotRow(8, 8); got != 0x0950 {
		t.Fatalf("HT1 plot row = %04X want 0950", got)
	}

	d.SCMR = 0x20 // HT=2, 2bpp.
	if got := d.plotRow(8, 8); got != 0x0990 {
		t.Fatalf("HT2 plot row = %04X want 0990", got)
	}

	d.SCMR = 0x03 // HT=0, 8bpp.
	if got := d.plotRow(8, 8); got != 0x0c40 {
		t.Fatalf("8bpp plot row = %04X want 0C40", got)
	}
}

func TestPlotOptions(t *testing.T) {
	t.Run("transparent zero skips plot", func(t *testing.T) {
		d := New(nil, nil)
		d.POR = 0
		d.plot(3, 0, 0x00)

		if got := d.rpix(3, 0); got != 0 {
			t.Fatalf("transparent zero plotted %02X, want 00", got)
		}
		if d.cacheHasRow {
			t.Fatalf("transparent zero should not populate cache")
		}
	})

	t.Run("transparent bit allows zero plot", func(t *testing.T) {
		d := New(nil, nil)
		d.POR = porTransparent
		d.plot(3, 0, 0x00)

		if !d.cacheHasRow || d.validMask&(1<<3) == 0 {
			t.Fatalf("transparent-enabled zero did not populate cache")
		}
	})

	t.Run("dither selects high nibble on odd parity", func(t *testing.T) {
		d := New(nil, nil)
		d.POR = porDither
		d.plot(1, 0, 0xAB)

		if got := d.rpix(1, 0); got != 0x0A {
			t.Fatalf("dithered pixel=%02X want 0A", got)
		}
	})
}

// stubVRAMWriter records every WriteTileRow call.
type stubVRAMWriter struct {
	rows []struct {
		Addr uint16
		Row  [8]byte
	}
}

func (s *stubVRAMWriter) WriteTileRow(addr uint16, row [8]byte) {
	s.rows = append(s.rows, struct {
		Addr uint16
		Row  [8]byte
	}{Addr: addr, Row: row})
}

// TestVRAMWriterHookReceivesCommit — when a VRAMWriter is bound, commits
// go to it instead of the shadow buffer. This is the commit-to-RAM
// timing test from Phase 10 acceptance: flush must route through the hook
// exactly once per row transition.
func TestVRAMWriterHookReceivesCommit(t *testing.T) {
	d := New(nil, nil)
	w := &stubVRAMWriter{}
	d.SetVRAMWriter(w)
	d.COLR = 0xAB
	for x := uint16(0); x < 4; x++ {
		d.plot(x, 0, d.COLR)
	}
	d.plot(0, 8, d.COLR)
	if len(w.rows) != 1 {
		t.Fatalf("hook received %d commits, want 1", len(w.rows))
	}
	// The flushed row must contain the four plotted colour indices at
	// their cache positions.
	for x := 0; x < 4; x++ {
		if w.rows[0].Row[x] != 0xAB {
			t.Errorf("row[%d]=%02X want AB", x, w.rows[0].Row[x])
		}
	}
	for x := 4; x < 8; x++ {
		if w.rows[0].Row[x] != 0 {
			t.Errorf("row[%d]=%02X want 00 (unplotted)", x, w.rows[0].Row[x])
		}
	}
}

// TestPlotOpcode pins the executable PLOT opcode path. It must draw and
// increment X without clobbering the accumulator.
func TestPlotOpcode(t *testing.T) {
	d := New([]byte{0x4C, 0x00}, nil)
	d.R[0] = 0x1234
	d.R[1] = 3
	d.R[2] = 0
	d.R[12] = 0x0100
	d.COLR = 0x07
	d.Go()
	d.Run(1)

	if d.R[0] != 0x1234 {
		t.Fatalf("PLOT clobbered R0: got %04X want 1234", d.R[0])
	}
	if d.R[1] != 4 {
		t.Fatalf("PLOT R1 increment = %d, want 4", d.R[1])
	}
	got := d.rpix(3, 0)
	if got != 0x07 {
		t.Fatalf("PLOT wrote color %02X, want 07", got)
	}
}

// TestRpixOpcode pins the ALT1 executable RPIX opcode path. ALT1;0x4C must
// read the cached pixel into R0.
func TestRpixOpcode(t *testing.T) {
	d := New([]byte{0x3D, 0x4C, 0x00}, nil)
	d.R[0] = 0x1234
	d.R[1] = 3
	d.R[2] = 0
	d.R[12] = 0x0100
	d.SFR |= SFRCY
	d.COLR = 0x09
	d.plot(3, 0, d.COLR)
	d.Go()
	d.Run(2)

	if d.R[0] != 0x0009 {
		t.Fatalf("RPIX result R0=%04X, want cached pixel 0009", d.R[0])
	}
	if d.R[1] != 3 {
		t.Fatalf("RPIX incremented R1: got %d want 3", d.R[1])
	}
}

// TestColorOpcode pins the executable COLOR/CMODE opcode path. It must
// update COLR or POR without clobbering the accumulator.
func TestColorOpcode(t *testing.T) {
	t.Run("COLOR", func(t *testing.T) {
		d := New([]byte{0x4E, 0x00}, nil)
		d.R[0] = 0x1234
		d.R[14] = 0x0100
		d.Go()
		d.Run(1)

		if d.R[0] != 0x1234 {
			t.Fatalf("COLOR clobbered R0: got %04X want 1234", d.R[0])
		}
		if d.COLR != 0x34 {
			t.Fatalf("COLOR set COLR=%02X, want 34", d.COLR)
		}
	})

	t.Run("COLOR freeze high", func(t *testing.T) {
		d := New([]byte{0x4E, 0x00}, nil)
		d.R[0] = 0x0034
		d.COLR = 0xA0
		d.POR = porFreezeHigh
		d.Go()
		d.Run(1)

		if d.COLR != 0xA4 {
			t.Fatalf("COLOR freeze-high COLR=%02X want A4", d.COLR)
		}
	})

	t.Run("COLOR high nibble", func(t *testing.T) {
		d := New([]byte{0x4E, 0x00}, nil)
		d.R[0] = 0x00B4
		d.COLR = 0xA0
		d.POR = porHighNibble
		d.Go()
		d.Run(1)

		if d.COLR != 0xAB {
			t.Fatalf("COLOR high-nibble COLR=%02X want AB", d.COLR)
		}
	})

	t.Run("CMODE", func(t *testing.T) {
		d := New([]byte{0x3D, 0x4E, 0x00}, nil)
		d.R[0] = 0x1256
		d.R[14] = 0x0100
		d.Go()
		d.Run(2)

		if d.R[0] != 0x1256 {
			t.Fatalf("CMODE clobbered R0: got %04X want 1256", d.R[0])
		}
		if d.POR != 0x56 {
			t.Fatalf("CMODE set POR=%02X, want 56", d.POR)
		}
	})
}
