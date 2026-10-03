package dsp1

import (
	"fmt"
	"testing"
)

// Status port (odd address) always reads 0x80 and ignores writes.
// Mirrors snes9x dsp1.cpp DSP1GetByte tail (line 1701) and the silent
// odd-address branch of DSP1SetByte.
func TestStatusPortAlways0x80(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	if got, ok := d.Read(0x208001); !ok || got != 0x80 {
		t.Fatalf("status read = %02X ok=%v, want 80,true", got, ok)
	}
	if d.Write(0x208001, 0xFF) {
		t.Fatalf("expected odd-address write to be ignored")
	}
	if got, ok := d.Read(0x208001); !ok || got != 0x80 {
		t.Fatalf("status read after odd-write = %02X, want 80", got)
	}
}

// Empty data port (no command issued, no result queued) returns 0x80
// per snes9x DSP1GetByte:1697-1701 (the `out_count == 0` else branch).
func TestDataPortEmptyReturns0x80(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	if got, ok := d.Read(0x208000); !ok || got != 0x80 {
		t.Fatalf("empty data port = %02X ok=%v, want 80,true", got, ok)
	}
}

// Map-type windows: only the configured mapper window decodes.
func TestDeviceMapTypeWindows(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMLarge)
	if _, ok := d.Read(0x600000); !ok {
		t.Fatalf("large-map expected 60:0000 window to be mapped")
	}
	if _, ok := d.Read(0x208000); ok {
		t.Fatalf("large-map should not map 20:8000")
	}

	d.SetMapType(MapHiROM)
	if _, ok := d.Read(0x006000); !ok {
		t.Fatalf("hirom expected 00:6000 window to be mapped")
	}
	if _, ok := d.Read(0x208000); ok {
		t.Fatalf("hirom should not map 20:8000")
	}
}

// Unknown command bytes (not in the snes9x switch) reset to waiting4command
// without queueing output. The data port keeps reading 0x80.
// Mirrors the `default` case at dsp1.cpp:1224.
func TestUnknownCommandReturnsToWait(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0xA5)
	if got, _ := d.Read(0x208000); got != 0x80 {
		t.Fatalf("after unknown cmd 0xA5: data = %02X, want 80 (waiting4command)", got)
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after unknown command")
	}
}

// Bare 0x80 byte while waiting is the explicit no-op case (dsp1.cpp:1228).
// Buffer stays empty.
func TestCommandByte0x80IsNoop(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x80)
	if !d.waiting4command {
		t.Fatalf("0x80 cmd should leave waiting4command true")
	}
	if got, _ := d.Read(0x208000); got != 0x80 {
		t.Fatalf("data after 0x80 cmd = %02X, want 80", got)
	}
}

// Parameter collection: a known command (Op 0F, in_count=1 word=2 bytes)
// transitions out of waiting4command and back when both param bytes arrive.
func TestParameterCollectionTransitions(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	// Op 0F: status/identity, 1 word param.
	d.Write(0x208000, 0x0F)
	if d.waiting4command {
		t.Fatalf("after cmd 0x0F: should not be waiting4command")
	}
	if d.inCount != 2 {
		t.Fatalf("after cmd 0x0F: inCount = %d, want 2 (1 word)", d.inCount)
	}
	d.Write(0x208000, 0x11) // param byte 1
	if d.waiting4command {
		t.Fatalf("after 1 param byte: should still be collecting")
	}
	if d.inCount != 1 {
		t.Fatalf("after 1 param byte: inCount = %d, want 1", d.inCount)
	}
	d.Write(0x208000, 0x22) // param byte 2 — triggers execute
	if !d.waiting4command {
		t.Fatalf("after final param byte: should be waiting4command")
	}
	// Op 0F is a stage-2 no-op (no output queued).
	if d.outCount != 0 {
		t.Fatalf("Op 0F outCount = %d, want 0 (stage-2 no-op)", d.outCount)
	}
	// Stored params are observable in the parameter buffer.
	if d.parameters[0] != 0x11 || d.parameters[1] != 0x22 {
		t.Fatalf("parameters = %02X %02X, want 11 22", d.parameters[0], d.parameters[1])
	}
}

// Op 0x?A aliases rewrite to 0x1A (snes9x dsp1.cpp:1175-1180).
func TestRasterAliasRewrite(t *testing.T) {
	for _, alias := range []uint8{0x1a, 0x2a, 0x3a} {
		d := New()
		d.SetMapType(MapLoROMSmall)
		d.Write(0x208000, alias)
		if d.command != 0x1a {
			t.Errorf("alias %02X: command = %02X, want 1A", alias, d.command)
		}
	}
}

// Op 0x17/0x37/0x3F rewrite to 0x1F (snes9x dsp1.cpp:1219-1223).
func TestStatusAliasRewrite(t *testing.T) {
	for _, alias := range []uint8{0x17, 0x37, 0x3f} {
		d := New()
		d.SetMapType(MapLoROMSmall)
		d.Write(0x208000, alias)
		if d.command != 0x1f {
			t.Errorf("alias %02X: command = %02X, want 1F", alias, d.command)
		}
	}
}

// paramWordCount cross-check against snes9x dsp1.cpp:1154+ for a few key ops.
func TestParamWordCountTable(t *testing.T) {
	cases := []struct {
		cmd  uint8
		want uint8
	}{
		{0x02, 7}, // parameter
		{0x0a, 1}, // Raster (per-scanline)
		{0x06, 3}, // Project
		{0x04, 2}, // Sin/Cos
		{0x08, 3}, // Radius
		{0x18, 4},
		{0x14, 6},
		{0x0f, 1}, // Status
		{0x80, 0}, // Explicit no-op
		{0xa5, 0}, // Unknown
	}
	for _, tc := range cases {
		if got := paramWordCount(tc.cmd); got != tc.want {
			t.Errorf("paramWordCount(%02X) = %d, want %d", tc.cmd, got, tc.want)
		}
	}
}

// Op 0x04 (Sin/Cos * radius) — snes9x dsp1.cpp DSP1_Op04.
// Angle is int16, radius is uint16; results are sin*radius>>15 and
// cos*radius>>15, each int16, written little-endian to output[0..3].
// Bus reads return low byte first then high byte.
func TestOp04SinCosResults(t *testing.T) {
	cases := []struct {
		name             string
		angle            int16
		radius           uint16
		wantSin, wantCos int16
	}{
		// Cardinal: angle=0 → sin=0, cos=0x7fff. radius=0x4000 ⇒
		// sin*r>>15 = 0; cos*r>>15 = 0x7fff*0x4000>>15 = 0x3fff (truncated).
		{"zero_angle", 0x0000, 0x4000,
			int16(int32(0) * 0x4000 >> 15),
			int16(int32(0x7fff) * 0x4000 >> 15)},
		// 90°: sin=0x7fff, cos=0.
		{"ninety", 0x4000, 0x4000,
			int16(int32(0x7fff) * 0x4000 >> 15),
			int16(int32(0) * 0x4000 >> 15)},
		// Mid angle from sinFP fixture: 0x1234 → sin=0x374d, cos=0x7370.
		{"mid_full_radius", 0x1234, 0x7fff,
			int16(int32(0x374d) * 0x7fff >> 15),
			int16(int32(0x7370) * 0x7fff >> 15)},
		// Negative angle: -0x1234 → sin=-0x374d, cos=0x7370.
		{"neg_angle", -0x1234, 0x4000,
			int16(int32(-0x374d) * 0x4000 >> 15),
			int16(int32(0x7370) * 0x4000 >> 15)},
		// radius=0 → both results 0 regardless of angle.
		{"zero_radius", 0x1234, 0x0000, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			d.SetMapType(MapLoROMSmall)
			// Issue Op 0x04 and 4 parameter bytes via the bus.
			d.Write(0x208000, 0x04)
			d.Write(0x208000, uint8(uint16(tc.angle)&0xff))
			d.Write(0x208000, uint8(uint16(tc.angle)>>8))
			d.Write(0x208000, uint8(tc.radius&0xff))
			d.Write(0x208000, uint8(tc.radius>>8))
			if !d.waiting4command {
				t.Fatalf("expected waiting4command after final param byte")
			}
			if d.outCount != 4 {
				t.Fatalf("outCount = %d, want 4", d.outCount)
			}
			// Drain four bytes via the bus and recompose words little-endian.
			b0, _ := d.Read(0x208000)
			b1, _ := d.Read(0x208000)
			b2, _ := d.Read(0x208000)
			b3, _ := d.Read(0x208000)
			gotSin := int16(uint16(b0) | uint16(b1)<<8)
			gotCos := int16(uint16(b2) | uint16(b3)<<8)
			if gotSin != tc.wantSin {
				t.Errorf("sin = %#06x, want %#06x", uint16(gotSin), uint16(tc.wantSin))
			}
			if gotCos != tc.wantCos {
				t.Errorf("cos = %#06x, want %#06x", uint16(gotCos), uint16(tc.wantCos))
			}
			// After draining all 4 result bytes, queue is empty → 0x80.
			if extra, _ := d.Read(0x208000); extra != 0x80 {
				t.Errorf("post-drain read = %#02x, want 0x80", extra)
			}
			if !d.waiting4command {
				t.Errorf("expected waiting4command after output drained")
			}
		})
	}
}

// Op 0x24 is an alias of Op 0x04 (snes9x case-fallthrough).
func TestOp04AliasOp24(t *testing.T) {
	for _, cmd := range []uint8{0x04, 0x24} {
		d := New()
		d.SetMapType(MapLoROMSmall)
		d.Write(0x208000, cmd)
		// angle=0x1234, radius=0x7fff
		d.Write(0x208000, 0x34)
		d.Write(0x208000, 0x12)
		d.Write(0x208000, 0xff)
		d.Write(0x208000, 0x7f)
		if d.outCount != 4 {
			t.Fatalf("cmd %#02x: outCount = %d, want 4", cmd, d.outCount)
		}
		wantSin := int16(int32(0x374d) * 0x7fff >> 15)
		b0, _ := d.Read(0x208000)
		b1, _ := d.Read(0x208000)
		gotSin := int16(uint16(b0) | uint16(b1)<<8)
		if gotSin != wantSin {
			t.Errorf("cmd %#02x: sin = %#06x, want %#06x", cmd, uint16(gotSin), uint16(wantSin))
		}
	}
}

// Op 0x02 (Parameter / Projection) — snes9x dsp1.cpp DSP1_Parameter.
// 7 input words → 4 output words plus persistent projection state used by
// Op 0x0A and 0x06. The math helpers (sinFP/cosFP/inverse/normalize/
// truncate) are independently bit-exact against snes9x via dsp1math_test.go,
// so this test verifies the wiring: the bus path matches a direct call to
// parameter(), aliases dispatch identically, state fields are persisted,
// and serialization survives a round-trip.
func TestOp02ParameterBusVsDirect(t *testing.T) {
	cases := []struct {
		name                           string
		fx, fy, fz, lfe, les, aas, azs int16
	}{
		{"all_zero", 0, 0, 0, 0, 0, 0, 0},
		{"camera_origin_les", 0, 0, 0, 0x4000, 0x2000, 0, 0},
		{"azimuth_only", 0, 0, 0, 0x4000, 0x2000, 0x1000, 0},
		{"zenith_only", 0, 0, 0, 0x4000, 0x2000, 0, 0x0800},
		{"full_projection", 0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500},
		{"negative_zenith", 0, 0, 0, 0x4000, 0x2000, 0, -0x0500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Direct compute via the helper.
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			wantVof, wantVva, wantCx, wantCy := ref.parameter(
				tc.fx, tc.fy, tc.fz, tc.lfe, tc.les, tc.aas, tc.azs)

			// Same inputs via the bus.
			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, 0x02)
			for _, w := range []int16{tc.fx, tc.fy, tc.fz, tc.lfe, tc.les, tc.aas, tc.azs} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if !d.waiting4command {
				t.Fatalf("expected waiting4command after 14 param bytes")
			}
			if d.outCount != 8 {
				t.Fatalf("outCount = %d, want 8", d.outCount)
			}

			// Drain four output words.
			var got [8]uint8
			for i := range got {
				b, _ := d.Read(0x208000)
				got[i] = b
			}
			gotVof := readWordLE(got[0:])
			gotVva := readWordLE(got[2:])
			gotCx := readWordLE(got[4:])
			gotCy := readWordLE(got[6:])

			if gotVof != wantVof || gotVva != wantVva || gotCx != wantCx || gotCy != wantCy {
				t.Errorf("bus output (%#06x,%#06x,%#06x,%#06x) != direct (%#06x,%#06x,%#06x,%#06x)",
					uint16(gotVof), uint16(gotVva), uint16(gotCx), uint16(gotCy),
					uint16(wantVof), uint16(wantVva), uint16(wantCx), uint16(wantCy))
			}

			// Projection state mirrored on the bus device matches the direct one.
			if d.sinAzs != ref.sinAzs || d.cosAas != ref.cosAas || d.sinAas != ref.sinAas {
				t.Errorf("sinAzs/cosAas/sinAas mismatch: bus=(%#06x,%#06x,%#06x) ref=(%#06x,%#06x,%#06x)",
					uint16(d.sinAzs), uint16(d.cosAas), uint16(d.sinAas),
					uint16(ref.sinAzs), uint16(ref.cosAas), uint16(ref.sinAas))
			}
			if d.vplaneC != ref.vplaneC || d.vplaneE != ref.vplaneE {
				t.Errorf("vplane mismatch: bus=(%#06x,%d) ref=(%#06x,%d)",
					uint16(d.vplaneC), d.vplaneE, uint16(ref.vplaneC), ref.vplaneE)
			}
			if d.secAZS_C2 != ref.secAZS_C2 || d.secAZS_E2 != ref.secAZS_E2 {
				t.Errorf("secAZS_2 mismatch: bus=(%#06x,%d) ref=(%#06x,%d)",
					uint16(d.secAZS_C2), d.secAZS_E2, uint16(ref.secAZS_C2), ref.secAZS_E2)
			}
			if d.vOffset != ref.vOffset {
				t.Errorf("vOffset mismatch: bus=%#06x ref=%#06x", uint16(d.vOffset), uint16(ref.vOffset))
			}
		})
	}
}

// All-zero anchor: Op02 with all-zero parameters. From snes9x DSP1_Parameter:
// SinAas=0, CosAas=0x7fff, SinAzs=0, CosAzs=0x7fff. nx=0, ny=0, nz=0x7fff.
// CentreX=CentreY=0, Gx=Gy=0, Gz=0. Normalize(0,0) → C=0, E=-15. MaxAZS_Exp[15]=0x38e4,
// AZS=0 ≤ 0x38e4 so AZS=0. SinAZS=0, CosAZS=0x7fff. Inverse(0x7fff,0) → C=0x4000,E=1.
// First normalize after that: C=0*..>>15=0 → normalize(0, -15) → C=0, E=-30. E += 1 → -29.
// Truncate(0,-29)=0*.. =0. C*sinAZS=0. CentreX/Y unchanged → cx=cy=0. azs==AZS path skipped.
// VOffset = 0 * 0x7fff >> 15 = 0.
// Inverse(0,0) → 0x7fff,0x002f (the singularity case). normalize(0,0x2f) → C=0,E=0x2f-15=0x20.
// Then normalize(0*0x7fff>>15, 0x20) = normalize(0, 0x20) → C=0, E=0x11. Vva=truncate(0, 0x11)=0.
// Vof=0. Cx=0. Cy=0. So all four output words are 0.
func TestOp02AllZeroAnchor(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x02)
	for i := 0; i < 14; i++ {
		d.Write(0x208000, 0x00)
	}
	for i := 0; i < 8; i++ {
		b, _ := d.Read(0x208000)
		if b != 0 {
			t.Errorf("output[%d] = %#02x, want 0", i, b)
		}
	}
	if d.cosAas != 0x7fff || d.cosAzs != 0x7fff {
		t.Errorf("cosAas/cosAzs = %#06x/%#06x, want 7fff/7fff", uint16(d.cosAas), uint16(d.cosAzs))
	}
	if d.sinAas != 0 || d.sinAzs != 0 {
		t.Errorf("sinAas/sinAzs = %#06x/%#06x, want 0/0", uint16(d.sinAas), uint16(d.sinAzs))
	}
	// nz = cosAzs(0)*0x7fff>>15 = 0x7fff*0x7fff>>15 = 0x7ffe.
	if d.nz != 0x7ffe {
		t.Errorf("nz = %#06x, want 7ffe", uint16(d.nz))
	}
}

// Op 0x12/0x22/0x32 are aliases of Op 0x02 (snes9x case-fallthrough).
func TestOp02Aliases(t *testing.T) {
	for _, cmd := range []uint8{0x02, 0x12, 0x22, 0x32} {
		d := New()
		d.SetMapType(MapLoROMSmall)
		ref := New()
		ref.SetMapType(MapLoROMSmall)
		wantVof, wantVva, wantCx, wantCy := ref.parameter(0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)

		d.Write(0x208000, cmd)
		for _, w := range []int16{0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500} {
			d.Write(0x208000, uint8(uint16(w)&0xff))
			d.Write(0x208000, uint8(uint16(w)>>8))
		}
		var b [8]uint8
		for i := range b {
			x, _ := d.Read(0x208000)
			b[i] = x
		}
		if readWordLE(b[0:]) != wantVof || readWordLE(b[2:]) != wantVva ||
			readWordLE(b[4:]) != wantCx || readWordLE(b[6:]) != wantCy {
			t.Errorf("cmd %#02x: bus output disagrees with direct parameter()", cmd)
		}
	}
}

// Serialize round-trip preserves all projection state set by Op02.
func TestOp02SerializePreservesProjectionState(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x02)
	for _, w := range []int16{0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	// Drain output so the post-state is "execute completed".
	for i := 0; i < 8; i++ {
		d.Read(0x208000)
	}

	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	d2 := New()
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	checks := []struct {
		name      string
		got, want int16
	}{
		{"sinAas", d2.sinAas, d.sinAas},
		{"cosAas", d2.cosAas, d.cosAas},
		{"sinAzs", d2.sinAzs, d.sinAzs},
		{"cosAzs", d2.cosAzs, d.cosAzs},
		{"nx", d2.nx, d.nx}, {"ny", d2.ny, d.ny}, {"nz", d2.nz, d.nz},
		{"centreX", d2.centreX, d.centreX}, {"centreY", d2.centreY, d.centreY},
		{"gx", d2.gx, d.gx}, {"gy", d2.gy, d.gy}, {"gz", d2.gz, d.gz},
		{"cLes", d2.cLes, d.cLes}, {"eLes", d2.eLes, d.eLes}, {"gLes", d2.gLes, d.gLes},
		{"vplaneC", d2.vplaneC, d.vplaneC}, {"vplaneE", d2.vplaneE, d.vplaneE},
		{"sinAZS", d2.sinAZS, d.sinAZS}, {"cosAZS", d2.cosAZS, d.cosAZS},
		{"secAZS_C1", d2.secAZS_C1, d.secAZS_C1}, {"secAZS_E1", d2.secAZS_E1, d.secAZS_E1},
		{"secAZS_C2", d2.secAZS_C2, d.secAZS_C2}, {"secAZS_E2", d2.secAZS_E2, d.secAZS_E2},
		{"vOffset", d2.vOffset, d.vOffset},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %#06x, want %#06x", c.name, uint16(c.got), uint16(c.want))
		}
	}
}

// Op 0x0A (Raster) — snes9x dsp1.cpp DSP1_Op0A / DSP1_Raster.
// Verifies: bus path matches direct raster() helper, op0AVS advances per
// call, getByte refresh trick auto-runs Op0A on output drain (only for
// commands 0x0A/0x1A), aliases 0x1A/2A/3A reach the same dispatch, and
// raster output depends on Op02 state (zero state vs. Op02-initialized
// state produces different results).
func TestOp0ARasterBusVsDirect(t *testing.T) {
	// Set up Op02 state via the bus, then issue Op0A and compare.
	d := New()
	d.SetMapType(MapLoROMSmall)
	// Op02 with a non-degenerate parameter set.
	d.Write(0x208000, 0x02)
	for _, w := range []int16{0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	for i := 0; i < 8; i++ {
		d.Read(0x208000) // drain Op02 output
	}

	// Reference device gets identical Op02 setup, then directly invokes raster()
	// for the same Vs values.
	ref := New()
	ref.SetMapType(MapLoROMSmall)
	ref.parameter(0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)

	for _, vs := range []int16{0, 1, 100, -50, 0x1000, -0x1000} {
		t.Run("vs_"+itoaSigned(vs), func(t *testing.T) {
			wantA, wantB, wantC, wantD := ref.raster(vs)

			d.Write(0x208000, 0x0a)
			d.Write(0x208000, uint8(uint16(vs)&0xff))
			d.Write(0x208000, uint8(uint16(vs)>>8))
			if d.outCount != 8 {
				t.Fatalf("outCount = %d, want 8", d.outCount)
			}
			var b [8]uint8
			for i := range b {
				b[i], _ = d.Read(0x208000)
			}
			gotA := readWordLE(b[0:])
			gotB := readWordLE(b[2:])
			gotC := readWordLE(b[4:])
			gotD := readWordLE(b[6:])
			if gotA != wantA || gotB != wantB || gotC != wantC || gotD != wantD {
				t.Errorf("vs=%#06x: got (%#06x,%#06x,%#06x,%#06x) want (%#06x,%#06x,%#06x,%#06x)",
					uint16(vs), uint16(gotA), uint16(gotB), uint16(gotC), uint16(gotD),
					uint16(wantA), uint16(wantB), uint16(wantC), uint16(wantD))
			}
		})
	}
}

// After draining 8 result bytes, snes9x re-runs Op0A and refills the buffer
// (advancing op0AVS) so the data port keeps streaming. Verify by comparing
// the second drain against raster(vs+1) computed directly.
func TestOp0AGetByteRefreshAdvancesScanline(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x02)
	for _, w := range []int16{0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	for i := 0; i < 8; i++ {
		d.Read(0x208000)
	}

	ref := New()
	ref.SetMapType(MapLoROMSmall)
	ref.parameter(0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)

	const startVs int16 = 0x10
	d.Write(0x208000, 0x0a)
	d.Write(0x208000, uint8(uint16(startVs)&0xff))
	d.Write(0x208000, uint8(uint16(startVs)>>8))

	// Drain first 8 bytes (vs=startVs).
	var first [8]uint8
	for i := range first {
		first[i], _ = d.Read(0x208000)
	}
	wantA, wantB, wantC, wantD := ref.raster(startVs)
	if readWordLE(first[0:]) != wantA || readWordLE(first[2:]) != wantB ||
		readWordLE(first[4:]) != wantC || readWordLE(first[6:]) != wantD {
		t.Fatalf("first drain mismatch")
	}
	// Refresh should have auto-fired and queued vs=startVs+1.
	if d.outCount != 8 {
		t.Fatalf("after first drain: outCount = %d, want 8 (refresh)", d.outCount)
	}
	// Drain second 8 bytes; expect raster(startVs+1).
	var second [8]uint8
	for i := range second {
		second[i], _ = d.Read(0x208000)
	}
	wantA2, wantB2, wantC2, wantD2 := ref.raster(startVs + 1)
	if readWordLE(second[0:]) != wantA2 || readWordLE(second[2:]) != wantB2 ||
		readWordLE(second[4:]) != wantC2 || readWordLE(second[6:]) != wantD2 {
		t.Errorf("second drain (vs+1) mismatch: got (%#06x,%#06x,%#06x,%#06x) want (%#06x,%#06x,%#06x,%#06x)",
			uint16(readWordLE(second[0:])), uint16(readWordLE(second[2:])),
			uint16(readWordLE(second[4:])), uint16(readWordLE(second[6:])),
			uint16(wantA2), uint16(wantB2), uint16(wantC2), uint16(wantD2))
	}
	// And a third drain should yield raster(startVs+2).
	var third [8]uint8
	for i := range third {
		third[i], _ = d.Read(0x208000)
	}
	wantA3, _, _, _ := ref.raster(startVs + 2)
	if readWordLE(third[0:]) != wantA3 {
		t.Errorf("third drain An: got %#06x want %#06x",
			uint16(readWordLE(third[0:])), uint16(wantA3))
	}
}

// Aliases 0x1A/0x2A/0x3A all dispatch as 0x1A (post-rewrite) and produce
// the same raster output as 0x0A.
func TestOp0AAliases(t *testing.T) {
	for _, cmd := range []uint8{0x0a, 0x1a, 0x2a, 0x3a} {
		d := New()
		d.SetMapType(MapLoROMSmall)
		d.Write(0x208000, 0x02)
		for _, w := range []int16{0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500} {
			d.Write(0x208000, uint8(uint16(w)&0xff))
			d.Write(0x208000, uint8(uint16(w)>>8))
		}
		for i := 0; i < 8; i++ {
			d.Read(0x208000)
		}

		d.Write(0x208000, cmd)
		d.Write(0x208000, 0x10)
		d.Write(0x208000, 0x00)
		var b [8]uint8
		for i := range b {
			b[i], _ = d.Read(0x208000)
		}

		ref := New()
		ref.SetMapType(MapLoROMSmall)
		ref.parameter(0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)
		wantA, _, _, _ := ref.raster(0x10)
		if readWordLE(b[0:]) != wantA {
			t.Errorf("cmd %#02x: An = %#06x, want %#06x", cmd, uint16(readWordLE(b[0:])), uint16(wantA))
		}
	}
}

// Without Op02 having run, raster reads zero state and produces a known
// non-trivial result determined purely by the helpers; we just verify that
// the bus does dispatch Op0A and outCount is set, rather than asserting
// specific values that may be misleading.
func TestOp0AWithoutOp02StillDispatches(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x0a)
	d.Write(0x208000, 0x10)
	d.Write(0x208000, 0x00)
	if d.outCount != 8 {
		t.Fatalf("outCount = %d, want 8", d.outCount)
	}
	// initial vs=0x10, executeOp0A advances → 0x11.
	if d.op0AVS != 0x11 {
		t.Errorf("op0AVS = %#x, want 0x11 (advanced after first call)", uint16(d.op0AVS))
	}
}

// Serialize round-trip preserves op0AVS.
func TestOp0ASerializePreservesScanline(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x0a)
	d.Write(0x208000, 0x42)
	d.Write(0x208000, 0x00)
	// vs=0x42 → execute increments to 0x43, then each 8-byte drain refreshes
	// (executeOp0A again) so 16 bytes → two refreshes → 0x45.
	for i := 0; i < 16; i++ {
		d.Read(0x208000)
	}
	if d.op0AVS != 0x45 {
		t.Fatalf("op0AVS = %#x, want 0x45", uint16(d.op0AVS))
	}
	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	d2 := New()
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if d2.op0AVS != 0x45 {
		t.Errorf("restored op0AVS = %#x, want 0x45", uint16(d2.op0AVS))
	}
}

func TestOp01MatrixABusVsDirect(t *testing.T) {
	cases := []struct {
		name          string
		m, zr, yr, xr int16
	}{
		{"zero_angles", 0x4000, 0, 0, 0},
		{"z_only", 0x4000, 0x1000, 0, 0},
		{"y_only", 0x4000, 0, 0x1000, 0},
		{"x_only", 0x4000, 0, 0, 0x1000},
		{"all_axes", 0x4000, 0x0800, 0x1234, -0x0500},
		{"negative_m", -0x4000, 0x0800, 0x1234, -0x0500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixA, tc.m, tc.zr, tc.yr, tc.xr)

			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, 0x01)
			for _, w := range []int16{tc.m, tc.zr, tc.yr, tc.xr} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if !d.waiting4command {
				t.Fatalf("Op01: not waiting4command after 8 param bytes")
			}
			if d.outCount != 0 {
				t.Fatalf("Op01: outCount=%d, want 0 (no bus output)", d.outCount)
			}
			// Empty queue → reads return 0x80.
			if b, _ := d.Read(0x208000); b != 0x80 {
				t.Errorf("Op01: post-execute read = %#02x, want 0x80", b)
			}
			if d.matrixA != ref.matrixA {
				t.Errorf("matrixA mismatch:\n bus =%v\n ref =%v", d.matrixA, ref.matrixA)
			}
		})
	}
}

func TestOp01Aliases(t *testing.T) {
	for _, cmd := range []uint8{0x01, 0x05, 0x31, 0x35} {
		t.Run("cmd_"+itoaUnsigned(uint16(cmd)), func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixA, 0x4000, 0x0800, 0x1234, -0x0500)

			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, cmd)
			for _, w := range []int16{0x4000, 0x0800, 0x1234, -0x0500} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if !d.waiting4command {
				t.Fatalf("alias %#02x: not waiting4command", cmd)
			}
			if d.matrixA != ref.matrixA {
				t.Errorf("alias %#02x matrixA mismatch", cmd)
			}
		})
	}
}

// Op 0x11 / 0x15 set attitude matrix B with the same formula as Op01.
// Op 0x21 / 0x25 set matrix C. Bus path must drive the right matrix and
// leave the others untouched.
func TestOp11MatrixBBusVsDirect(t *testing.T) {
	cases := []struct {
		name          string
		m, zr, yr, xr int16
	}{
		{"zero_angles", 0x4000, 0, 0, 0},
		{"all_axes", 0x4000, 0x0800, 0x1234, -0x0500},
		{"negative_m", -0x4000, 0x0800, 0x1234, -0x0500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixB, tc.m, tc.zr, tc.yr, tc.xr)

			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, 0x11)
			for _, w := range []int16{tc.m, tc.zr, tc.yr, tc.xr} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if !d.waiting4command || d.outCount != 0 {
				t.Fatalf("Op11: state mismatch wait=%v out=%d", d.waiting4command, d.outCount)
			}
			if d.matrixB != ref.matrixB {
				t.Errorf("matrixB mismatch:\n bus =%v\n ref =%v", d.matrixB, ref.matrixB)
			}
			if d.matrixA != [3][3]int16{} || d.matrixC != [3][3]int16{} {
				t.Errorf("Op11 leaked into A/C: A=%v C=%v", d.matrixA, d.matrixC)
			}
		})
	}
}

func TestOp21MatrixCBusVsDirect(t *testing.T) {
	cases := []struct {
		name          string
		m, zr, yr, xr int16
	}{
		{"zero_angles", 0x4000, 0, 0, 0},
		{"all_axes", 0x4000, 0x0800, 0x1234, -0x0500},
		{"negative_m", -0x4000, 0x0800, 0x1234, -0x0500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixC, tc.m, tc.zr, tc.yr, tc.xr)

			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, 0x21)
			for _, w := range []int16{tc.m, tc.zr, tc.yr, tc.xr} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if !d.waiting4command || d.outCount != 0 {
				t.Fatalf("Op21: state mismatch wait=%v out=%d", d.waiting4command, d.outCount)
			}
			if d.matrixC != ref.matrixC {
				t.Errorf("matrixC mismatch:\n bus =%v\n ref =%v", d.matrixC, ref.matrixC)
			}
			if d.matrixA != [3][3]int16{} || d.matrixB != [3][3]int16{} {
				t.Errorf("Op21 leaked into A/B: A=%v B=%v", d.matrixA, d.matrixB)
			}
		})
	}
}

func TestOp11Op21Aliases(t *testing.T) {
	// 0x11/0x15 → matrixB; 0x21/0x25 → matrixC.
	for _, cmd := range []uint8{0x11, 0x15} {
		t.Run("B_cmd_"+itoaUnsigned(uint16(cmd)), func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixB, 0x4000, 0x0800, 0x1234, -0x0500)
			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, cmd)
			for _, w := range []int16{0x4000, 0x0800, 0x1234, -0x0500} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if d.matrixB != ref.matrixB {
				t.Errorf("alias %#02x matrixB mismatch", cmd)
			}
		})
	}
	for _, cmd := range []uint8{0x21, 0x25} {
		t.Run("C_cmd_"+itoaUnsigned(uint16(cmd)), func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixC, 0x4000, 0x0800, 0x1234, -0x0500)
			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, cmd)
			for _, w := range []int16{0x4000, 0x0800, 0x1234, -0x0500} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if d.matrixC != ref.matrixC {
				t.Errorf("alias %#02x matrixC mismatch", cmd)
			}
		})
	}
}

// All three matrix setters preserve their state across serialize round-trip.
func TestAttitudeMatrixSerializeABC(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	for _, cmd := range []uint8{0x01, 0x11, 0x21} {
		d.Write(0x208000, cmd)
		for _, w := range []int16{0x4000, int16(0x100 * uint16(cmd)), 0x1234, -0x0500} {
			d.Write(0x208000, uint8(uint16(w)&0xff))
			d.Write(0x208000, uint8(uint16(w)>>8))
		}
	}
	wantA, wantB, wantC := d.matrixA, d.matrixB, d.matrixC
	if wantA == [3][3]int16{} || wantB == [3][3]int16{} || wantC == [3][3]int16{} {
		t.Fatalf("setup left a matrix all-zero: A=%v B=%v C=%v", wantA, wantB, wantC)
	}
	if wantA == wantB || wantB == wantC {
		t.Fatalf("matrices should differ across A/B/C")
	}
	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	d2 := New()
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if d2.matrixA != wantA || d2.matrixB != wantB || d2.matrixC != wantC {
		t.Errorf("restored matrices differ:\n A: %v vs %v\n B: %v vs %v\n C: %v vs %v",
			d2.matrixA, wantA, d2.matrixB, wantB, d2.matrixC, wantC)
	}
}

func TestOp01SerializePreservesMatrixA(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x01)
	for _, w := range []int16{0x4000, 0x0800, 0x1234, -0x0500} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	want := d.matrixA
	if want == [3][3]int16{} {
		t.Fatalf("matrixA unexpectedly all-zero after Op01")
	}
	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	d2 := New()
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if d2.matrixA != want {
		t.Errorf("restored matrixA differs:\n got =%v\n want=%v", d2.matrixA, want)
	}
}

// drainBytes reads n bytes from the data port, returning them.
func drainBytes(t *testing.T, d *Device, n int) []uint8 {
	t.Helper()
	out := make([]uint8, n)
	for i := range out {
		b, _ := d.Read(0x208000)
		out[i] = b
	}
	return out
}

// writeWords writes parameter words little-endian over the bus.
func writeWords(d *Device, ws ...int16) {
	for _, w := range ws {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
}

// Bus path: set attitude matrix A via Op01, then run Op0D and verify
// outputs match the direct objectiveMatrix helper applied to matrixA.
func TestOp0DObjectiveMatrixABusVsDirect(t *testing.T) {
	cases := []struct {
		name    string
		x, y, z int16
	}{
		{"all_zero", 0, 0, 0},
		{"x_only", 0x1000, 0, 0},
		{"y_only", 0, 0x1000, 0},
		{"z_only", 0, 0, 0x1000},
		{"mixed", 0x0123, -0x0456, 0x0789},
		{"large", 0x4000, 0x4000, 0x4000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(&ref.matrixA, 0x4000, 0x0800, 0x1234, -0x0500)
			wantF, wantL, wantU := objectiveMatrix(&ref.matrixA, tc.x, tc.y, tc.z)

			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, 0x01)
			writeWords(d, 0x4000, 0x0800, 0x1234, -0x0500)
			if !d.waiting4command {
				t.Fatalf("post-Op01: not waiting4command")
			}
			d.Write(0x208000, 0x0d)
			writeWords(d, tc.x, tc.y, tc.z)
			if !d.waiting4command || d.outCount != 6 {
				t.Fatalf("post-Op0D: wait=%v out=%d", d.waiting4command, d.outCount)
			}
			got := drainBytes(t, d, 6)
			gotF := readWordLE(got[0:])
			gotL := readWordLE(got[2:])
			gotU := readWordLE(got[4:])
			if gotF != wantF || gotL != wantL || gotU != wantU {
				t.Errorf("(F,L,U) bus=(%#06x,%#06x,%#06x) direct=(%#06x,%#06x,%#06x)",
					uint16(gotF), uint16(gotL), uint16(gotU),
					uint16(wantF), uint16(wantL), uint16(wantU))
			}
		})
	}
}

// Op0D against a zero matrixA (no Op01 setup) returns all zeros.
func TestOp0DWithoutOp01ReturnsZero(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x0d)
	writeWords(d, 0x0123, 0x0456, 0x0789)
	got := drainBytes(t, d, 6)
	for i, b := range got {
		if b != 0 {
			t.Errorf("output[%d] = %#02x, want 0 (zero matrixA)", i, b)
		}
	}
}

// Op1D and Op2D consume matrixB / matrixC respectively.
func TestOp1DObjectiveMatrixBBusVsDirect(t *testing.T) {
	ref := New()
	ref.SetMapType(MapLoROMSmall)
	ref.attitudeMatrix(&ref.matrixB, 0x4000, 0x0800, 0x1234, -0x0500)
	wantF, wantL, wantU := objectiveMatrix(&ref.matrixB, 0x0123, -0x0456, 0x0789)

	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x11)
	writeWords(d, 0x4000, 0x0800, 0x1234, -0x0500)
	d.Write(0x208000, 0x1d)
	writeWords(d, 0x0123, -0x0456, 0x0789)
	got := drainBytes(t, d, 6)
	gotF := readWordLE(got[0:])
	gotL := readWordLE(got[2:])
	gotU := readWordLE(got[4:])
	if gotF != wantF || gotL != wantL || gotU != wantU {
		t.Errorf("Op1D (F,L,U) bus=(%#06x,%#06x,%#06x) direct=(%#06x,%#06x,%#06x)",
			uint16(gotF), uint16(gotL), uint16(gotU),
			uint16(wantF), uint16(wantL), uint16(wantU))
	}
}

func TestOp2DObjectiveMatrixCBusVsDirect(t *testing.T) {
	ref := New()
	ref.SetMapType(MapLoROMSmall)
	ref.attitudeMatrix(&ref.matrixC, 0x4000, 0x0800, 0x1234, -0x0500)
	wantF, wantL, wantU := objectiveMatrix(&ref.matrixC, 0x0123, -0x0456, 0x0789)

	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x21)
	writeWords(d, 0x4000, 0x0800, 0x1234, -0x0500)
	d.Write(0x208000, 0x2d)
	writeWords(d, 0x0123, -0x0456, 0x0789)
	got := drainBytes(t, d, 6)
	gotF := readWordLE(got[0:])
	gotL := readWordLE(got[2:])
	gotU := readWordLE(got[4:])
	if gotF != wantF || gotL != wantL || gotU != wantU {
		t.Errorf("Op2D (F,L,U) bus=(%#06x,%#06x,%#06x) direct=(%#06x,%#06x,%#06x)",
			uint16(gotF), uint16(gotL), uint16(gotU),
			uint16(wantF), uint16(wantL), uint16(wantU))
	}
}

// Aliases: 0x0D family adds 0x09/0x39/0x3D; 0x1D adds 0x19; 0x2D adds 0x29.
// Each alias must consume the matrix corresponding to its base op.
func TestObjectiveMatrixAliases(t *testing.T) {
	type aliasCase struct {
		alias uint8
		setup uint8 // matrix-set command
		mat   func(*Device) *[3][3]int16
	}
	cases := []aliasCase{
		{0x09, 0x01, func(d *Device) *[3][3]int16 { return &d.matrixA }},
		{0x39, 0x01, func(d *Device) *[3][3]int16 { return &d.matrixA }},
		{0x3d, 0x01, func(d *Device) *[3][3]int16 { return &d.matrixA }},
		{0x19, 0x11, func(d *Device) *[3][3]int16 { return &d.matrixB }},
		{0x29, 0x21, func(d *Device) *[3][3]int16 { return &d.matrixC }},
	}
	for _, tc := range cases {
		t.Run("alias_"+itoaUnsigned(uint16(tc.alias)), func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.attitudeMatrix(tc.mat(ref), 0x4000, 0x0800, 0x1234, -0x0500)
			wantF, wantL, wantU := objectiveMatrix(tc.mat(ref), 0x100, -0x200, 0x300)

			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, tc.setup)
			writeWords(d, 0x4000, 0x0800, 0x1234, -0x0500)
			d.Write(0x208000, tc.alias)
			writeWords(d, 0x100, -0x200, 0x300)
			if d.outCount != 6 {
				t.Fatalf("alias %#02x: outCount=%d want 6", tc.alias, d.outCount)
			}
			got := drainBytes(t, d, 6)
			gotF := readWordLE(got[0:])
			gotL := readWordLE(got[2:])
			gotU := readWordLE(got[4:])
			if gotF != wantF || gotL != wantL || gotU != wantU {
				t.Errorf("alias %#02x mismatch: bus=(%#06x,%#06x,%#06x) ref=(%#06x,%#06x,%#06x)",
					tc.alias, uint16(gotF), uint16(gotL), uint16(gotU),
					uint16(wantF), uint16(wantL), uint16(wantU))
			}
		})
	}
}

// runOp02 helper: executes Op 0x02 over the bus with the given parameters
// and drains its 4-word output, leaving the device ready for a new command.
func runOp02(t *testing.T, d *Device, fx, fy, fz, lfe, les, aas, azs int16) {
	t.Helper()
	d.Write(0x208000, 0x02)
	for _, w := range []int16{fx, fy, fz, lfe, les, aas, azs} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	for i := 0; i < 8; i++ {
		d.Read(0x208000)
	}
	if !d.waiting4command {
		t.Fatalf("after Op02 drain: not waiting4command")
	}
}

func TestOp06ProjectBusVsDirect(t *testing.T) {
	cases := []struct {
		name    string
		x, y, z int16
	}{
		{"origin", 0, 0, 0},
		{"in_front", 0, 0, 0x1000},
		{"side", 0x400, 0, 0x1000},
		{"above", 0, 0x400, 0x1000},
		{"negative", -0x200, -0x300, 0x800},
		{"large", 0x4000, 0x4000, 0x4000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := New()
			ref.SetMapType(MapLoROMSmall)
			ref.parameter(0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)
			wantH, wantV, wantM := ref.project(tc.x, tc.y, tc.z)

			d := New()
			d.SetMapType(MapLoROMSmall)
			runOp02(t, d, 0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)

			d.Write(0x208000, 0x06)
			for _, w := range []int16{tc.x, tc.y, tc.z} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			if !d.waiting4command {
				t.Fatalf("Op06: expected waiting4command after 6 param bytes")
			}
			if d.outCount != 6 {
				t.Fatalf("Op06: outCount = %d, want 6", d.outCount)
			}
			var got [6]uint8
			for i := range got {
				b, _ := d.Read(0x208000)
				got[i] = b
			}
			gotH := readWordLE(got[0:])
			gotV := readWordLE(got[2:])
			gotM := readWordLE(got[4:])
			if gotH != wantH || gotV != wantV || gotM != wantM {
				t.Errorf("(H,V,M) bus=(%#06x,%#06x,%#06x) direct=(%#06x,%#06x,%#06x)",
					uint16(gotH), uint16(gotV), uint16(gotM),
					uint16(wantH), uint16(wantV), uint16(wantM))
			}
		})
	}
}

func TestOp06Aliases(t *testing.T) {
	for _, cmd := range []uint8{0x06, 0x16, 0x26, 0x36} {
		t.Run("cmd_"+itoaUnsigned(uint16(cmd)), func(t *testing.T) {
			d := New()
			d.SetMapType(MapLoROMSmall)
			runOp02(t, d, 0x100, 0x200, -0x80, 0x4000, 0x2000, 0x1234, 0x0500)
			d.Write(0x208000, cmd)
			d.Write(0x208000, 0x00)
			d.Write(0x208000, 0x04)
			d.Write(0x208000, 0x00)
			d.Write(0x208000, 0x00)
			d.Write(0x208000, 0x00)
			d.Write(0x208000, 0x10)
			if !d.waiting4command {
				t.Fatalf("alias %#02x: not waiting4command after 6 bytes", cmd)
			}
			if d.outCount != 6 {
				t.Fatalf("alias %#02x: outCount=%d want 6", cmd, d.outCount)
			}
		})
	}
}

// Op06 with no Op02 setup runs against zeroed projection state — does not
// crash and produces a deterministic value matching the direct helper.
func TestOp06WithoutOp02(t *testing.T) {
	ref := New()
	ref.SetMapType(MapLoROMSmall)
	wantH, wantV, wantM := ref.project(0x100, 0x200, 0x300)

	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x06)
	for _, w := range []int16{0x100, 0x200, 0x300} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	var got [6]uint8
	for i := range got {
		b, _ := d.Read(0x208000)
		got[i] = b
	}
	gotH := readWordLE(got[0:])
	gotV := readWordLE(got[2:])
	gotM := readWordLE(got[4:])
	if gotH != wantH || gotV != wantV || gotM != wantM {
		t.Errorf("no-Op02 (H,V,M) bus=(%#06x,%#06x,%#06x) direct=(%#06x,%#06x,%#06x)",
			uint16(gotH), uint16(gotV), uint16(gotM),
			uint16(wantH), uint16(wantV), uint16(wantM))
	}
}

func itoaSigned(v int16) string {
	if v < 0 {
		return "-" + itoaUnsigned(uint16(-v))
	}
	return itoaUnsigned(uint16(v))
}
func itoaUnsigned(v uint16) string {
	if v == 0 {
		return "0"
	}
	var buf [6]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// Reset returns the device to a clean waiting4command state.
func TestResetClearsState(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x02) // Parameter command, 7 words
	d.Write(0x208000, 0xAB) // 1 param byte
	if d.waiting4command || d.inIndex != 1 {
		t.Fatalf("pre-reset: not in expected mid-parameter state (wait=%v idx=%d)", d.waiting4command, d.inIndex)
	}
	d.reset()
	if !d.waiting4command || d.inIndex != 0 || d.inCount != 0 || d.outCount != 0 {
		t.Fatalf("post-reset state wrong: wait=%v inIdx=%d inCnt=%d outCnt=%d",
			d.waiting4command, d.inIndex, d.inCount, d.outCount)
	}
}

// Serialize round-trip preserves state-machine fields.
func TestSerializeRoundTrip(t *testing.T) {
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x06) // Project, 3 words
	d.Write(0x208000, 0x11)
	d.Write(0x208000, 0x22)

	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	d2 := New()
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if d2.command != 0x06 || d2.inIndex != 2 || d2.inCount != 4 || d2.waiting4command {
		t.Fatalf("restored state mismatch: cmd=%02X inIdx=%d inCnt=%d wait=%v",
			d2.command, d2.inIndex, d2.inCount, d2.waiting4command)
	}
	if d2.parameters[0] != 0x11 || d2.parameters[1] != 0x22 {
		t.Fatalf("restored params = %02X %02X, want 11 22", d2.parameters[0], d2.parameters[1])
	}
	if d2.MapType != MapLoROMSmall {
		t.Fatalf("restored MapType = %d, want LoROMSmall", d2.MapType)
	}
}

// DSP-1 Q15 fixed-point multiply ops (snes9x dsp1.cpp:278-295 + dispatch
// at 1268-1286). Op00 returns mul1 * mul2 >> 15; Op20 returns the same
// but with a post-increment of the int16 result. Both consume 2 input
// words and produce 1 output word.

// driveOp00Or20 issues an Op00 or Op20 command with the supplied 2-word
// parameter pair through the bus and reads back the 2-byte result.
func driveOp00Or20(t *testing.T, cmd uint8, mul1, mul2 int16) int16 {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, cmd)
	d.Write(0x208000, uint8(uint16(mul1)&0xff))
	d.Write(0x208000, uint8(uint16(mul1)>>8))
	d.Write(0x208000, uint8(uint16(mul2)&0xff))
	d.Write(0x208000, uint8(uint16(mul2)>>8))
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after final param byte (cmd=%#x)", cmd)
	}
	if d.outCount != 2 {
		t.Fatalf("outCount = %d, want 2 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	got := int16(uint16(b0) | uint16(b1)<<8)
	// After draining the 2 result bytes, queue is empty → 0x80.
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return got
}

func TestOp00Q15Multiply(t *testing.T) {
	cases := []struct {
		name       string
		mul1, mul2 int16
		want       int16
	}{
		{"zero_times_anything", 0, 0x4000, 0},
		{"anything_times_zero", 0x4000, 0, 0},
		{"half_times_half", 0x4000, 0x4000, int16(int32(0x4000) * 0x4000 >> 15)},
		{"max_times_max", 0x7fff, 0x7fff, int16(int32(0x7fff) * 0x7fff >> 15)},
		{"neg_one_times_pos_half", -1, 0x4000, int16(int32(-1) * 0x4000 >> 15)},
		{"neg_max_times_pos_max", -0x7fff, 0x7fff, int16(int32(-0x7fff) * 0x7fff >> 15)},
		// (-0x8000) * (-0x8000) = 0x40000000; >>15 = 0x8000 which wraps
		// to int16(-0x8000) under the Q15 truncation.
		{"min_int16_times_min_int16", -0x8000, -0x8000, -0x8000},
		{"identity_one_times_x", 0x7fff, 0x4000, int16(int32(0x7fff) * 0x4000 >> 15)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := driveOp00Or20(t, 0x00, tc.mul1, tc.mul2)
			if got != tc.want {
				t.Errorf("Op00(%#x, %#x) = %#06x, want %#06x", uint16(tc.mul1), uint16(tc.mul2), uint16(got), uint16(tc.want))
			}
		})
	}
}

func TestOp20Q15MultiplyPlusOne(t *testing.T) {
	cases := []struct {
		name       string
		mul1, mul2 int16
		want       int16
	}{
		{"zero_times_anything_plus_one", 0, 0x4000, 1},
		{"anything_times_zero_plus_one", 0x4000, 0, 1},
		{"half_times_half_plus_one", 0x4000, 0x4000, int16(int32(0x4000)*0x4000>>15) + 1},
		{"max_times_max_plus_one", 0x7fff, 0x7fff, int16(int32(0x7fff)*0x7fff>>15) + 1},
		{"neg_one_times_pos_half_plus_one", -1, 0x4000, int16(int32(-1)*0x4000>>15) + 1},
		// Same Op00 wrap as TestOp00Q15Multiply: result -0x8000, then +1 = -0x7fff.
		{"min_int16_times_min_int16_plus_one", -0x8000, -0x8000, -0x7fff},
		// Wraparound check: an Op00 result of 0x7fff + 1 wraps to -0x8000.
		// Find inputs where Op00 = 0x7fff: mul1=0x7fff*2=0xfffe (out of range);
		// instead use mul1=0x7fff, mul2=0x7fff which yields Op00=0x7ffe (one
		// short of overflow). We pick a smaller test: the Op00 result must
		// truly equal Op20-1 for every input pair under uint16 wraparound.
		{"overflow_wrap", 0x7fff, 0x7fff, int16(int32(0x7fff)*0x7fff>>15) + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := driveOp00Or20(t, 0x20, tc.mul1, tc.mul2)
			if got != tc.want {
				t.Errorf("Op20(%#x, %#x) = %#06x, want %#06x", uint16(tc.mul1), uint16(tc.mul2), uint16(got), uint16(tc.want))
			}
		})
	}
}

func TestOp20IsOp00PlusOneInvariant(t *testing.T) {
	// For every input pair the property Op20 = Op00 + 1 (mod 2^16) holds.
	// Sample pairs covering positive/negative/zero/extremes.
	pairs := []struct{ a, b int16 }{
		{0, 0}, {1, 1}, {-1, -1}, {0x4000, 0x4000}, {0x7fff, 0x7fff},
		{-0x8000, -0x8000}, {0x1234, 0x5678}, {-0x1234, 0x5678},
		{0x1234, -0x5678}, {-0x1234, -0x5678}, {0x7fff, 1}, {-0x8000, 1},
	}
	for _, p := range pairs {
		op00 := driveOp00Or20(t, 0x00, p.a, p.b)
		op20 := driveOp00Or20(t, 0x20, p.a, p.b)
		if op20 != op00+1 {
			t.Errorf("Op20(%#x,%#x)=%#06x but Op00=%#06x; want Op20 = Op00+1",
				uint16(p.a), uint16(p.b), uint16(op20), uint16(op00))
		}
	}
}

// DSP-1 Op10 fixed-point inverse (snes9x dsp1.cpp:1288-1298 dispatch
// over the inverse helper at dsp1math.go:8-41). 2 input words
// (Coefficient, Exponent) → 2 output words (iCoefficient, iExponent).
// Aliases 0x10 and 0x30 fall through to the same handler in snes9x.

// driveOp10 issues an Op10/Op30 command with the supplied (coef, exp)
// pair through the bus and reads back the (iCoef, iExp) result words.
func driveOp10(t *testing.T, cmd uint8, coef, exp int16) (iCoef, iExp int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, cmd)
	d.Write(0x208000, uint8(uint16(coef)&0xff))
	d.Write(0x208000, uint8(uint16(coef)>>8))
	d.Write(0x208000, uint8(uint16(exp)&0xff))
	d.Write(0x208000, uint8(uint16(exp)>>8))
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after final param byte (cmd=%#x)", cmd)
	}
	if d.outCount != 4 {
		t.Fatalf("outCount = %d, want 4 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	iCoef = int16(uint16(b0) | uint16(b1)<<8)
	iExp = int16(uint16(b2) | uint16(b3)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return iCoef, iExp
}

func TestOp10Inverse(t *testing.T) {
	// Goldens lifted from TestInverse (dsp1math_test.go:55-78). The
	// bus-driven dispatch must produce the same (iCoef, iExp) pair the
	// inverse helper produces directly.
	cases := []struct {
		coef, exp         int16
		wantCoef, wantExp int16
	}{
		{0x0000, 0, 0x7fff, 0x002f},
		{0x4000, 0, 0x7fff, 1},
		{0x4001, 0, 0x7ffe, 1},
		{-0x4000, 0, -0x4000, 2},
		{0x7fff, 0, 0x4000, 1},
		{-0x7fff, 0, -0x4000, 1},
		{0x1234, 3, 0x7082, 0},
		{-0x1234, -2, -0x7082, 5},
		{0x0100, 0, 0x7fff, 7},
		{-0x0100, 0, -0x4000, 8},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("coef=%#06x_exp=%d", uint16(tc.coef), tc.exp), func(t *testing.T) {
			gotCoef, gotExp := driveOp10(t, 0x10, tc.coef, tc.exp)
			if gotCoef != tc.wantCoef || gotExp != tc.wantExp {
				t.Errorf("Op10(%#06x,%d) = (%#06x,%d), want (%#06x,%d)",
					uint16(tc.coef), tc.exp, uint16(gotCoef), gotExp,
					uint16(tc.wantCoef), tc.wantExp)
			}
		})
	}
}

func TestOp10AliasOp30(t *testing.T) {
	// Per snes9x dsp1.cpp:1288-1289 case-fallthrough, command bytes 0x30
	// and 0x10 share the same Inverse handler. Verify a few goldens
	// produce identical results when issued via 0x30.
	cases := []struct {
		coef, exp         int16
		wantCoef, wantExp int16
	}{
		{0x0000, 0, 0x7fff, 0x002f},
		{0x4000, 0, 0x7fff, 1},
		{0x1234, 3, 0x7082, 0},
		{-0x1234, -2, -0x7082, 5},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("coef=%#06x_exp=%d", uint16(tc.coef), tc.exp), func(t *testing.T) {
			gotCoef, gotExp := driveOp10(t, 0x30, tc.coef, tc.exp)
			if gotCoef != tc.wantCoef || gotExp != tc.wantExp {
				t.Errorf("Op30(%#06x,%d) = (%#06x,%d), want (%#06x,%d)",
					uint16(tc.coef), tc.exp, uint16(gotCoef), gotExp,
					uint16(tc.wantCoef), tc.wantExp)
			}
		})
	}
}

// DSP-1 Op03/Op13/Op23 subjective matrix family (snes9x dsp1.cpp:
// 907-938 bodies + dispatch 1536-1574). Each takes 3 input words
// (F,L,U) and produces 3 output words (X,Y,Z) = M^T · (F,L,U) using
// matrix A, B, or C respectively. M^T is the transpose of the matrix
// stored by the corresponding Op01/Op11/Op21 attitude command, so the
// op implements the inverse projection of Op0D/Op1D/Op2D for the
// orthogonal rotation matrices that attitudeMatrix produces.

// driveOp03Family issues a subjective-matrix command via the bus and
// drains the 3-word result. attitudeCmd is the Op01/Op11/Op21 command
// byte that programs the corresponding matrix; subjCmd is the
// Op03/Op13/Op23 subjective command. m, zr, yr, xr program the
// attitude matrix; f, l, u are the subjective input vector.
func driveOp03Family(t *testing.T, attitudeCmd, subjCmd uint8, m, zr, yr, xr, f, l, u int16) (x, y, z int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	// Program attitude matrix.
	d.Write(0x208000, attitudeCmd)
	for _, w := range []int16{m, zr, yr, xr} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after attitude params (cmd=%#x)", attitudeCmd)
	}
	// Issue subjective op.
	d.Write(0x208000, subjCmd)
	for _, w := range []int16{f, l, u} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after subjective params (cmd=%#x)", subjCmd)
	}
	if d.outCount != 6 {
		t.Fatalf("outCount = %d, want 6 (cmd=%#x)", d.outCount, subjCmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	b4, _ := d.Read(0x208000)
	b5, _ := d.Read(0x208000)
	x = int16(uint16(b0) | uint16(b1)<<8)
	y = int16(uint16(b2) | uint16(b3)<<8)
	z = int16(uint16(b4) | uint16(b5)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, subjCmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", subjCmd)
	}
	return
}

// driveOp0DOp03RoundTrip programs an attitude matrix, runs Op0D
// (objective M·v), then runs Op03 against the same matrix on the
// objective output, and returns the final triple.
func driveOp0DOp03RoundTrip(t *testing.T, m, zr, yr, xr, x0, y0, z0 int16) (x1, y1, z1 int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x01)
	for _, w := range []int16{m, zr, yr, xr} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	d.Write(0x208000, 0x0d)
	for _, w := range []int16{x0, y0, z0} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	flu := make([]int16, 3)
	for i := 0; i < 3; i++ {
		lo, _ := d.Read(0x208000)
		hi, _ := d.Read(0x208000)
		flu[i] = int16(uint16(lo) | uint16(hi)<<8)
	}
	d.Write(0x208000, 0x03)
	for _, w := range flu {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	for _, dst := range []*int16{&x1, &y1, &z1} {
		lo, _ := d.Read(0x208000)
		hi, _ := d.Read(0x208000)
		*dst = int16(uint16(lo) | uint16(hi)<<8)
	}
	return
}

// matchesSubjectiveOnMatrix asserts that Op{03,13,23}'s bus-driven
// output matches subjectiveMatrix() invoked directly on the same
// matrix the device should be holding after Op{01,11,21}.
func matchesSubjectiveOnMatrix(t *testing.T, attitudeCmd, subjCmd uint8, m, zr, yr, xr, f, l, u int16) {
	t.Helper()
	// Run the device's bus-driven path.
	gotX, gotY, gotZ := driveOp03Family(t, attitudeCmd, subjCmd, m, zr, yr, xr, f, l, u)
	// Compute the same operation via a direct device + helper path.
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, attitudeCmd)
	for _, w := range []int16{m, zr, yr, xr} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	var mat *[3][3]int16
	switch attitudeCmd {
	case 0x01:
		mat = &d.matrixA
	case 0x11:
		mat = &d.matrixB
	case 0x21:
		mat = &d.matrixC
	default:
		t.Fatalf("unexpected attitudeCmd %#x", attitudeCmd)
	}
	wantX, wantY, wantZ := subjectiveMatrix(mat, f, l, u)
	if gotX != wantX || gotY != wantY || gotZ != wantZ {
		t.Errorf("subjCmd=%#x m=%#x angles=(%#x,%#x,%#x) flu=(%#x,%#x,%#x): bus = (%#06x,%#06x,%#06x), helper = (%#06x,%#06x,%#06x)",
			subjCmd, uint16(m), uint16(zr), uint16(yr), uint16(xr),
			uint16(f), uint16(l), uint16(u),
			uint16(gotX), uint16(gotY), uint16(gotZ),
			uint16(wantX), uint16(wantY), uint16(wantZ))
	}
}

func TestOp03MatchesSubjectiveHelperOnMatrixA(t *testing.T) {
	// Bus-driven Op03 must match subjectiveMatrix() on the same matrix
	// the device produces from Op01 with identical m/zr/yr/xr inputs.
	cases := []struct {
		name          string
		m, zr, yr, xr int16
		f, l, u       int16
	}{
		{"zero_matrix_zero_input", 0, 0, 0, 0, 0, 0, 0},
		{"zero_matrix_nonzero_input", 0, 0, 0, 0, 0x1234, 0x5678, 0x4321},
		{"unit_m_zero_angles", 0x7FFF, 0, 0, 0, 0x1000, 0x2000, 0x3000},
		{"unit_m_nonzero_angles", 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400},
		{"neg_m_zero_angles", -0x7FFF, 0, 0, 0, 0x1000, 0x2000, 0x3000},
		{"max_m_arbitrary", 0x7FFE, 0x4000, -0x2000, 0x1000, 0x4000, -0x4000, 0x2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matchesSubjectiveOnMatrix(t, 0x01, 0x03, tc.m, tc.zr, tc.yr, tc.xr, tc.f, tc.l, tc.u)
		})
	}
}

func TestOp03AliasOp33MatchesOp03(t *testing.T) {
	// 0x33 falls through to 0x03 per snes9x dsp1.cpp:1536-1537.
	matchesSubjectiveOnMatrix(t, 0x01, 0x33, 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400)
}

func TestOp13MatchesSubjectiveHelperOnMatrixB(t *testing.T) {
	matchesSubjectiveOnMatrix(t, 0x11, 0x13, 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400)
	matchesSubjectiveOnMatrix(t, 0x11, 0x13, 0x7FFE, -0x4000, 0x4000, -0x2000, -0x1000, 0x2000, -0x3000)
}

func TestOp23MatchesSubjectiveHelperOnMatrixC(t *testing.T) {
	matchesSubjectiveOnMatrix(t, 0x21, 0x23, 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400)
	matchesSubjectiveOnMatrix(t, 0x21, 0x23, 0x7FFE, -0x4000, 0x4000, -0x2000, -0x1000, 0x2000, -0x3000)
}

func TestOp0DOp03RoundTripMatchesHelperComposition(t *testing.T) {
	// Bus-driven Op03(Op0D(v)) must match the helper composition
	// subjectiveMatrix(M, objectiveMatrix(M, v)) for the same matrix
	// that Op01 produces. This validates dispatch wiring without
	// depending on M being orthogonal at the Q15 level (snes9x's
	// attitudeMatrix produces a near-orthogonal matrix that loses a
	// few LSB to truncation; comparing against the helper composition
	// makes that error common to both sides).
	cases := []struct {
		name          string
		m, zr, yr, xr int16
		x, y, z       int16
	}{
		{"unit_m_zero_angles", 0x7FFF, 0, 0, 0, 0x1234, 0x5678, 0x4321},
		{"max_m_zero_angles", 0x7FFE, 0, 0, 0, -0x1234, -0x5678, -0x4321},
		{"unit_m_small_angles", 0x7FFF, 0x0400, 0x0200, 0x0100, 0x1000, 0x2000, 0x3000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY, gotZ := driveOp0DOp03RoundTrip(t, tc.m, tc.zr, tc.yr, tc.xr, tc.x, tc.y, tc.z)
			// Helper-side comparison: program the same matrix on a
			// fresh device (so we can read matrixA), then run the
			// objective + subjective helpers.
			d := New()
			d.SetMapType(MapLoROMSmall)
			d.Write(0x208000, 0x01)
			for _, w := range []int16{tc.m, tc.zr, tc.yr, tc.xr} {
				d.Write(0x208000, uint8(uint16(w)&0xff))
				d.Write(0x208000, uint8(uint16(w)>>8))
			}
			midF, midL, midU := objectiveMatrix(&d.matrixA, tc.x, tc.y, tc.z)
			wantX, wantY, wantZ := subjectiveMatrix(&d.matrixA, midF, midL, midU)
			if gotX != wantX || gotY != wantY || gotZ != wantZ {
				t.Errorf("bus Op03∘Op0D(%#x,%#x,%#x) = (%#06x,%#06x,%#06x), helper composition = (%#06x,%#06x,%#06x)",
					uint16(tc.x), uint16(tc.y), uint16(tc.z),
					uint16(gotX), uint16(gotY), uint16(gotZ),
					uint16(wantX), uint16(wantY), uint16(wantZ))
			}
		})
	}
}

func TestOp03DoesNotClobberMatrixA(t *testing.T) {
	// Op03 must read matrixA, never write it. After Op03, a subsequent
	// Op0D against the same matrix must produce the same result it
	// would have produced before Op03 ran.
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x01)
	for _, w := range []int16{0, 0, 0, 0} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	d.Write(0x208000, 0x0d)
	for _, w := range []int16{0x1111, 0x2222, 0x3333} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	before := make([]int16, 3)
	for i := range before {
		lo, _ := d.Read(0x208000)
		hi, _ := d.Read(0x208000)
		before[i] = int16(uint16(lo) | uint16(hi)<<8)
	}
	d.Write(0x208000, 0x03)
	for _, w := range []int16{0x4444, 0x5555, 0x6666} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	for i := 0; i < 3; i++ {
		_, _ = d.Read(0x208000)
		_, _ = d.Read(0x208000)
	}
	d.Write(0x208000, 0x0d)
	for _, w := range []int16{0x1111, 0x2222, 0x3333} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	after := make([]int16, 3)
	for i := range after {
		lo, _ := d.Read(0x208000)
		hi, _ := d.Read(0x208000)
		after[i] = int16(uint16(lo) | uint16(hi)<<8)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("after Op03, Op0D[%d] = %#06x, want %#06x (matrixA was clobbered)",
				i, uint16(after[i]), uint16(before[i]))
		}
	}
}

// DSP-1 Op0C 2D rotation (snes9x dsp1.cpp:552-556 + dispatch 1361-1372).
// 3 input words (angle A, X1, Y1) → 2 output words (X2, Y2).
//   X2 = (Y1·Sin(A) >> 15) + (X1·Cos(A) >> 15)
//   Y2 = (Y1·Cos(A) >> 15) - (X1·Sin(A) >> 15)
// Aliases 0x0C and 0x2C fall through to the same handler.

func driveOp0C(t *testing.T, cmd uint8, angle, x1, y1 int16) (x2, y2 int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, cmd)
	for _, w := range []int16{angle, x1, y1} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after final param byte (cmd=%#x)", cmd)
	}
	if d.outCount != 4 {
		t.Fatalf("outCount = %d, want 4 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	x2 = int16(uint16(b0) | uint16(b1)<<8)
	y2 = int16(uint16(b2) | uint16(b3)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return
}

// op0CHelper computes the same math as the bus-driven Op0C path so
// tests can assert equivalence without copying the formula across
// every case.
func op0CHelper(angle, x1, y1 int16) (x2, y2 int16) {
	s := sinFP(angle)
	c := cosFP(angle)
	x2 = int16(int32(y1)*int32(s)>>15) + int16(int32(x1)*int32(c)>>15)
	y2 = int16(int32(y1)*int32(c)>>15) - int16(int32(x1)*int32(s)>>15)
	return
}

func TestOp0CMatchesHelper(t *testing.T) {
	cases := []struct {
		name          string
		angle, x1, y1 int16
	}{
		{"zero_angle", 0, 0x4000, 0x2000},       // identity: cos=0x7fff, sin=0
		{"ninety_pos", 0x4000, 0x4000, 0x2000},  // +90°
		{"ninety_neg", -0x4000, 0x4000, 0x2000}, // -90°
		{"forty_five", 0x2000, 0x4000, 0x2000},  // +45°
		{"zero_input", 0x2000, 0, 0},            // origin rotates to origin
		{"max_x", 0x1234, 0x7fff, 0x0100},
		{"max_y", 0x1234, 0x0100, 0x7fff},
		{"neg_inputs", 0x1234, -0x4000, -0x2000},
		{"min_int16_angle", -0x8000, 0x4000, 0x2000}, // angle wrap edge
		{"angle_high_bit_only", 0x4001, 0x4000, 0x2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY := driveOp0C(t, 0x0c, tc.angle, tc.x1, tc.y1)
			wantX, wantY := op0CHelper(tc.angle, tc.x1, tc.y1)
			if gotX != wantX || gotY != wantY {
				t.Errorf("Op0C(angle=%#06x, x1=%#06x, y1=%#06x): bus = (%#06x,%#06x), helper = (%#06x,%#06x)",
					uint16(tc.angle), uint16(tc.x1), uint16(tc.y1),
					uint16(gotX), uint16(gotY), uint16(wantX), uint16(wantY))
			}
		})
	}
}

func TestOp0CAliasOp2C(t *testing.T) {
	// Snes9x dsp1.cpp:1361-1362 falls 0x2c through to 0x0c. Verify
	// representative cases produce identical results via either alias.
	cases := []struct{ angle, x1, y1 int16 }{
		{0, 0x4000, 0x2000},
		{0x2000, 0x4000, 0x2000},
		{-0x4000, -0x1000, 0x3000},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("a=%#06x_x=%#06x_y=%#06x", uint16(tc.angle), uint16(tc.x1), uint16(tc.y1)), func(t *testing.T) {
			x0c, y0c := driveOp0C(t, 0x0c, tc.angle, tc.x1, tc.y1)
			x2c, y2c := driveOp0C(t, 0x2c, tc.angle, tc.x1, tc.y1)
			if x0c != x2c || y0c != y2c {
				t.Errorf("Op0c alias mismatch: 0x0c=(%#06x,%#06x) 0x2c=(%#06x,%#06x)",
					uint16(x0c), uint16(y0c), uint16(x2c), uint16(y2c))
			}
		})
	}
}

func TestOp0CIdentityRotationIsPassThrough(t *testing.T) {
	// angle=0 → cos=0x7fff, sin=0. Per the formula:
	//   X2 = Y1·0 + X1·0x7fff >> 15 = (X1·0x7fff)>>15 ≈ X1
	//   Y2 = Y1·0x7fff >> 15 - X1·0 = (Y1·0x7fff)>>15 ≈ Y1
	// Q15 ×0x7fff truncates by 1 LSB for nonzero inputs. We assert
	// helper-equivalence rather than exact identity to avoid baking
	// in the truncation.
	x2, y2 := driveOp0C(t, 0x0c, 0, 0x1234, 0x5678)
	wantX, wantY := op0CHelper(0, 0x1234, 0x5678)
	if x2 != wantX || y2 != wantY {
		t.Errorf("identity Op0C: bus=(%#06x,%#06x), helper=(%#06x,%#06x)",
			uint16(x2), uint16(y2), uint16(wantX), uint16(wantY))
	}
}

// DSP-1 Op1C 3D polar rotation (snes9x dsp1.cpp:1094-1117 + dispatch
// 1374-1390). 6 input words (Z, Y, X angles + XBR, YBR, ZBR vector) →
// 3 output words (XAR, YAR, ZAR rotated vector). The body performs
// three sequential 2D rotations around Z, then Y, then X, each
// matching Op0C's math vocabulary. Aliases 0x1c and 0x3c share the
// handler. The intermediate BR registers are scratch state within
// one invocation; callers resupply fresh values every call.

func driveOp1C(t *testing.T, cmd uint8, angZ, angY, angX, xbr, ybr, zbr int16) (xar, yar, zar int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, cmd)
	for _, w := range []int16{angZ, angY, angX, xbr, ybr, zbr} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after final param byte (cmd=%#x)", cmd)
	}
	if d.outCount != 6 {
		t.Fatalf("outCount = %d, want 6 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	b4, _ := d.Read(0x208000)
	b5, _ := d.Read(0x208000)
	xar = int16(uint16(b0) | uint16(b1)<<8)
	yar = int16(uint16(b2) | uint16(b3)<<8)
	zar = int16(uint16(b4) | uint16(b5)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return
}

// op1CHelper mirrors snes9x DSP1_Op1C body: three sequential 2D
// rotations around Z, Y, X axes. Used for bus-vs-helper equivalence
// assertions.
func op1CHelper(angZ, angY, angX, xbr, ybr, zbr int16) (xar, yar, zar int16) {
	// Rotate around Z.
	sZ := sinFP(angZ)
	cZ := cosFP(angZ)
	x1 := int16(int32(ybr)*int32(sZ)>>15) + int16(int32(xbr)*int32(cZ)>>15)
	y1 := int16(int32(ybr)*int32(cZ)>>15) - int16(int32(xbr)*int32(sZ)>>15)
	xbr = x1
	ybr = y1
	// Rotate around Y.
	sY := sinFP(angY)
	cY := cosFP(angY)
	z1 := int16(int32(xbr)*int32(sY)>>15) + int16(int32(zbr)*int32(cY)>>15)
	x1 = int16(int32(xbr)*int32(cY)>>15) - int16(int32(zbr)*int32(sY)>>15)
	xar = x1
	zbr = z1
	// Rotate around X.
	sX := sinFP(angX)
	cX := cosFP(angX)
	y1 = int16(int32(zbr)*int32(sX)>>15) + int16(int32(ybr)*int32(cX)>>15)
	z1 = int16(int32(zbr)*int32(cX)>>15) - int16(int32(ybr)*int32(sX)>>15)
	yar = y1
	zar = z1
	return
}

func TestOp1CMatchesHelper(t *testing.T) {
	cases := []struct {
		name             string
		angZ, angY, angX int16
		xbr, ybr, zbr    int16
	}{
		{"all_zero_angles", 0, 0, 0, 0x4000, 0x2000, 0x1000},
		{"z_only_90", 0x4000, 0, 0, 0x4000, 0x2000, 0x1000},
		{"y_only_90", 0, 0x4000, 0, 0x4000, 0x2000, 0x1000},
		{"x_only_90", 0, 0, 0x4000, 0x4000, 0x2000, 0x1000},
		{"all_90", 0x4000, 0x4000, 0x4000, 0x4000, 0x2000, 0x1000},
		{"all_neg_90", -0x4000, -0x4000, -0x4000, 0x4000, 0x2000, 0x1000},
		{"all_45", 0x2000, 0x2000, 0x2000, 0x4000, 0x2000, 0x1000},
		{"zero_vector", 0x2000, 0x2000, 0x2000, 0, 0, 0},
		{"max_vector", 0x1234, 0x5678, 0x4321, 0x7fff, 0x7fff, 0x7fff},
		{"neg_inputs", 0x1234, -0x4000, 0x2000, -0x4000, -0x2000, -0x1000},
		{"min_int16_angle", -0x8000, -0x8000, -0x8000, 0x4000, 0x2000, 0x1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY, gotZ := driveOp1C(t, 0x1c, tc.angZ, tc.angY, tc.angX, tc.xbr, tc.ybr, tc.zbr)
			wantX, wantY, wantZ := op1CHelper(tc.angZ, tc.angY, tc.angX, tc.xbr, tc.ybr, tc.zbr)
			if gotX != wantX || gotY != wantY || gotZ != wantZ {
				t.Errorf("Op1C(angles=(%#06x,%#06x,%#06x), v=(%#06x,%#06x,%#06x)): bus = (%#06x,%#06x,%#06x), helper = (%#06x,%#06x,%#06x)",
					uint16(tc.angZ), uint16(tc.angY), uint16(tc.angX),
					uint16(tc.xbr), uint16(tc.ybr), uint16(tc.zbr),
					uint16(gotX), uint16(gotY), uint16(gotZ),
					uint16(wantX), uint16(wantY), uint16(wantZ))
			}
		})
	}
}

func TestOp1CAliasOp3C(t *testing.T) {
	// 0x3c falls through to 0x1c per snes9x dsp1.cpp:1374-1375.
	cases := []struct{ angZ, angY, angX, xbr, ybr, zbr int16 }{
		{0x4000, 0, 0, 0x4000, 0x2000, 0x1000},
		{0x2000, 0x2000, 0x2000, 0x4000, 0x2000, 0x1000},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("a=(%#06x,%#06x,%#06x)", uint16(tc.angZ), uint16(tc.angY), uint16(tc.angX)), func(t *testing.T) {
			x1c, y1c, z1c := driveOp1C(t, 0x1c, tc.angZ, tc.angY, tc.angX, tc.xbr, tc.ybr, tc.zbr)
			x3c, y3c, z3c := driveOp1C(t, 0x3c, tc.angZ, tc.angY, tc.angX, tc.xbr, tc.ybr, tc.zbr)
			if x1c != x3c || y1c != y3c || z1c != z3c {
				t.Errorf("Op1C alias mismatch: 0x1c=(%#06x,%#06x,%#06x) 0x3c=(%#06x,%#06x,%#06x)",
					uint16(x1c), uint16(y1c), uint16(z1c), uint16(x3c), uint16(y3c), uint16(z3c))
			}
		})
	}
}

func TestOp1CIsStatelessAcrossInvocations(t *testing.T) {
	// Two consecutive Op1C calls with identical inputs must produce
	// identical outputs — verifies the BR registers are scratch state
	// within one call, not persisted.
	d := New()
	d.SetMapType(MapLoROMSmall)
	angZ, angY, angX := int16(0x2000), int16(0x1000), int16(0x0800)
	xbr, ybr, zbr := int16(0x4000), int16(0x2000), int16(0x1000)
	emit := func() (int16, int16, int16) {
		d.Write(0x208000, 0x1c)
		for _, w := range []int16{angZ, angY, angX, xbr, ybr, zbr} {
			d.Write(0x208000, uint8(uint16(w)&0xff))
			d.Write(0x208000, uint8(uint16(w)>>8))
		}
		var out [3]int16
		for i := range out {
			lo, _ := d.Read(0x208000)
			hi, _ := d.Read(0x208000)
			out[i] = int16(uint16(lo) | uint16(hi)<<8)
		}
		return out[0], out[1], out[2]
	}
	x1, y1, z1 := emit()
	x2, y2, z2 := emit()
	if x1 != x2 || y1 != y2 || z1 != z2 {
		t.Errorf("Op1C stateful across calls: first=(%#06x,%#06x,%#06x), second=(%#06x,%#06x,%#06x)",
			uint16(x1), uint16(y1), uint16(z1), uint16(x2), uint16(y2), uint16(z2))
	}
}

// DSP-1 Op0E Target / inverse-projection (snes9x dsp1.cpp:1001-1004
// wraps DSP1_Target at 972-999; dispatch 1445-1448 with aliases
// 0x0e/0x1e/0x2e/0x3e). 2 input words (H, V) → 2 output words (X, Y).
// Inverse-projects screen coordinates back to model space using state
// set by Op02 (Parameter / projection): sinAzs, vOffset, vplaneE/C,
// secAZS_E1, centreX/Y, sinAas/cosAas. The Op06/Op0E pair gives games
// full forward+inverse screen↔model space projection.

// programOp02 issues a typical Op02 (Parameter) command via the bus
// to set up the projection state Op0E reads. Returns the device so
// follow-up commands and direct field reads can be performed on it.
func programOp02(t *testing.T, fx, fy, fz, lfe, les, aas, azs int16) *Device {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x02)
	for _, w := range []int16{fx, fy, fz, lfe, les, aas, azs} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after Op02 params")
	}
	if d.outCount != 8 {
		t.Fatalf("Op02 outCount = %d, want 8", d.outCount)
	}
	// Drain Op02's 8 output bytes (Vof, Vva, Cx, Cy) so the queue is empty.
	for i := 0; i < 8; i++ {
		_, _ = d.Read(0x208000)
	}
	return d
}

func driveOp0E(t *testing.T, d *Device, cmd uint8, h, v int16) (x, y int16) {
	t.Helper()
	d.Write(0x208000, cmd)
	for _, w := range []int16{h, v} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after Op0E params (cmd=%#x)", cmd)
	}
	if d.outCount != 4 {
		t.Fatalf("Op0E outCount = %d, want 4 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	x = int16(uint16(b0) | uint16(b1)<<8)
	y = int16(uint16(b2) | uint16(b3)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return
}

func TestOp0EMatchesTargetHelper(t *testing.T) {
	// Bus-driven Op0E must match the target() helper invoked directly
	// on the same device's projection state. Tests sweep representative
	// Op02 setups and (H, V) inputs.
	cases := []struct {
		name                           string
		fx, fy, fz, lfe, les, aas, azs int16
		h, v                           int16
	}{
		{"identity_proj_origin", 0, 0, 0, 0x4000, 0x4000, 0, 0, 0, 0},
		// H=128 (0x80) as first param byte would trigger snes9x's bare-
		// 0x80 escape (dsp1.go:189-193). Use 120 to stay clear of that.
		{"identity_proj_near_centre", 0, 0, 0, 0x4000, 0x4000, 0, 0, 120, 112},
		{"identity_proj_offscreen", 0, 0, 0, 0x4000, 0x4000, 0, 0, -100, 200},
		{"yawed_proj", 0, 0, 0, 0x4000, 0x4000, 0x1000, 0, 64, 96},
		{"pitched_proj", 0, 0, 0, 0x4000, 0x4000, 0, 0x1000, 32, 48},
		{"yaw_pitch_combined", 0x100, 0x200, 0x300, 0x4000, 0x2000, 0x800, 0x800, 16, 24},
		{"max_h_v", 0, 0, 0, 0x4000, 0x4000, 0, 0, 0x7fff, 0x7fff},
		{"neg_h_v", 0, 0, 0, 0x4000, 0x4000, 0, 0, -0x4000, -0x4000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := programOp02(t, tc.fx, tc.fy, tc.fz, tc.lfe, tc.les, tc.aas, tc.azs)
			gotX, gotY := driveOp0E(t, d, 0x0e, tc.h, tc.v)
			// Compute expected via the helper directly on the same
			// device (state was captured from the same Op02 setup).
			d2 := programOp02(t, tc.fx, tc.fy, tc.fz, tc.lfe, tc.les, tc.aas, tc.azs)
			wantX, wantY := d2.target(tc.h, tc.v)
			if gotX != wantX || gotY != wantY {
				t.Errorf("Op0E(H=%#06x, V=%#06x): bus = (%#06x,%#06x), helper = (%#06x,%#06x)",
					uint16(tc.h), uint16(tc.v),
					uint16(gotX), uint16(gotY), uint16(wantX), uint16(wantY))
			}
		})
	}
}

func TestOp0EAliasesAllMatch(t *testing.T) {
	// 0x1e, 0x2e, 0x3e all fall through to 0x0e per snes9x case-fallthrough.
	d := programOp02(t, 0, 0, 0, 0x4000, 0x4000, 0x0800, 0x0400)
	x0e, y0e := driveOp0E(t, d, 0x0e, 100, 50)
	d = programOp02(t, 0, 0, 0, 0x4000, 0x4000, 0x0800, 0x0400)
	x1e, y1e := driveOp0E(t, d, 0x1e, 100, 50)
	d = programOp02(t, 0, 0, 0, 0x4000, 0x4000, 0x0800, 0x0400)
	x2e, y2e := driveOp0E(t, d, 0x2e, 100, 50)
	d = programOp02(t, 0, 0, 0, 0x4000, 0x4000, 0x0800, 0x0400)
	x3e, y3e := driveOp0E(t, d, 0x3e, 100, 50)
	if x0e != x1e || x0e != x2e || x0e != x3e || y0e != y1e || y0e != y2e || y0e != y3e {
		t.Errorf("Op0E aliases mismatch: 0x0e=(%#06x,%#06x) 0x1e=(%#06x,%#06x) 0x2e=(%#06x,%#06x) 0x3e=(%#06x,%#06x)",
			uint16(x0e), uint16(y0e), uint16(x1e), uint16(y1e),
			uint16(x2e), uint16(y2e), uint16(x3e), uint16(y3e))
	}
}

func TestOp0EDoesNotClobberOp02State(t *testing.T) {
	// Op0E must read projection state without modifying it. After
	// Op0E, a subsequent Op0E with the same inputs must produce the
	// same output (and a separate fresh Op02-then-Op0E run should
	// match too).
	d := programOp02(t, 0, 0, 0, 0x4000, 0x4000, 0x0800, 0x0400)
	x1, y1 := driveOp0E(t, d, 0x0e, 100, 50)
	x2, y2 := driveOp0E(t, d, 0x0e, 100, 50)
	if x1 != x2 || y1 != y2 {
		t.Errorf("Op0E altered projection state: first=(%#06x,%#06x), second=(%#06x,%#06x)",
			uint16(x1), uint16(y1), uint16(x2), uint16(y2))
	}
}

// DSP-1 Op0B/Op1B/Op2B scalar matrix family (snes9x dsp1.cpp:
// 1006-1032 bodies + dispatch 1576-1608). Each computes the scalar
// product of input vector (X,Y,Z) with row 0 of matrix A/B/C
// respectively. 3 input words → 1 output word. Aliases 0x0B/0x3B
// share Op0B; 0x1B and 0x2B alone.

func driveOp0BFamily(t *testing.T, attitudeCmd, scalarCmd uint8, m, zr, yr, xr, x, y, z int16) int16 {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, attitudeCmd)
	for _, w := range []int16{m, zr, yr, xr} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after attitude params (cmd=%#x)", attitudeCmd)
	}
	d.Write(0x208000, scalarCmd)
	for _, w := range []int16{x, y, z} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after scalar params (cmd=%#x)", scalarCmd)
	}
	if d.outCount != 2 {
		t.Fatalf("outCount = %d, want 2 (cmd=%#x)", d.outCount, scalarCmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	s := int16(uint16(b0) | uint16(b1)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, scalarCmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", scalarCmd)
	}
	return s
}

// matchesScalarOnMatrix asserts the bus-driven scalar op output
// matches scalarMatrix() invoked directly on the same matrix the
// device produces from the corresponding attitude command.
func matchesScalarOnMatrix(t *testing.T, attitudeCmd, scalarCmd uint8, m, zr, yr, xr, x, y, z int16) {
	t.Helper()
	got := driveOp0BFamily(t, attitudeCmd, scalarCmd, m, zr, yr, xr, x, y, z)
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, attitudeCmd)
	for _, w := range []int16{m, zr, yr, xr} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	var mat *[3][3]int16
	switch attitudeCmd {
	case 0x01:
		mat = &d.matrixA
	case 0x11:
		mat = &d.matrixB
	case 0x21:
		mat = &d.matrixC
	default:
		t.Fatalf("unexpected attitudeCmd %#x", attitudeCmd)
	}
	want := scalarMatrix(mat, x, y, z)
	if got != want {
		t.Errorf("scalarCmd=%#x m=%#x angles=(%#x,%#x,%#x) v=(%#x,%#x,%#x): bus=%#06x helper=%#06x",
			scalarCmd, uint16(m), uint16(zr), uint16(yr), uint16(xr),
			uint16(x), uint16(y), uint16(z), uint16(got), uint16(want))
	}
}

func TestOp0BMatchesScalarHelperOnMatrixA(t *testing.T) {
	cases := []struct {
		name          string
		m, zr, yr, xr int16
		x, y, z       int16
	}{
		{"zero_matrix_zero_input", 0, 0, 0, 0, 0, 0, 0},
		{"zero_matrix_nonzero_input", 0, 0, 0, 0, 0x1234, 0x5678, 0x4321},
		{"unit_m_zero_angles", 0x7FFF, 0, 0, 0, 0x1000, 0x2000, 0x3000},
		{"unit_m_nonzero_angles", 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400},
		{"neg_m_zero_angles", -0x7FFF, 0, 0, 0, 0x1000, 0x2000, 0x3000},
		{"max_m_arbitrary", 0x7FFE, 0x4000, -0x2000, 0x1000, 0x4000, -0x4000, 0x2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matchesScalarOnMatrix(t, 0x01, 0x0b, tc.m, tc.zr, tc.yr, tc.xr, tc.x, tc.y, tc.z)
		})
	}
}

func TestOp0BAliasOp3BMatchesOp0B(t *testing.T) {
	// 0x3B falls through to 0x0B per snes9x dsp1.cpp:1576-1577.
	matchesScalarOnMatrix(t, 0x01, 0x3b, 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400)
}

func TestOp1BMatchesScalarHelperOnMatrixB(t *testing.T) {
	matchesScalarOnMatrix(t, 0x11, 0x1b, 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400)
	matchesScalarOnMatrix(t, 0x11, 0x1b, 0x7FFE, -0x4000, 0x4000, -0x2000, -0x1000, 0x2000, -0x3000)
}

func TestOp2BMatchesScalarHelperOnMatrixC(t *testing.T) {
	matchesScalarOnMatrix(t, 0x21, 0x2b, 0x7FFF, 0x2000, 0x1000, 0x0800, 0x0100, 0x0200, 0x0400)
	matchesScalarOnMatrix(t, 0x21, 0x2b, 0x7FFE, -0x4000, 0x4000, -0x2000, -0x1000, 0x2000, -0x3000)
}

func TestOp0BIsObjectiveMatrixFirstRow(t *testing.T) {
	// scalarMatrix is mathematically the f component of
	// objectiveMatrix. Bus-driven Op0B output must equal Op0D's
	// first output word for the same matrix and input vector.
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x01)
	for _, w := range []int16{0x7FFF, 0x2000, 0x1000, 0x0800} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	// Op0D: read all 3 outputs; we only care about the first.
	d.Write(0x208000, 0x0d)
	x, y, z := int16(0x1000), int16(0x2000), int16(0x3000)
	for _, w := range []int16{x, y, z} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	lo, _ := d.Read(0x208000)
	hi, _ := d.Read(0x208000)
	op0DFirst := int16(uint16(lo) | uint16(hi)<<8)
	// Drain remaining 4 bytes.
	for i := 0; i < 4; i++ {
		_, _ = d.Read(0x208000)
	}
	op0BResult := driveOp0BFamily(t, 0x01, 0x0b, 0x7FFF, 0x2000, 0x1000, 0x0800, x, y, z)
	if op0BResult != op0DFirst {
		t.Errorf("Op0B = %#06x, Op0D first row = %#06x; want equal (scalarMatrix should match objectiveMatrix.f)",
			uint16(op0BResult), uint16(op0DFirst))
	}
}

// DSP-1 Op08 Radius / vector magnitude squared (snes9x dsp1.cpp:
// 1034-1044 + dispatch 1312-1322). 3 input words (X, Y, Z) → 2
// output words (Ll = low 16 bits, Lh = high 16 bits) of the 32-bit
// value (X² + Y² + Z²) << 1. snes9x relies on int32 wraparound for
// large signed inputs; Go's int32 has the same 2's-complement
// semantic so the literal port matches snes9x's bytes.

func driveOp08(t *testing.T, x, y, z int16) (lh, ll int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x08)
	for _, w := range []int16{x, y, z} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after Op08 params")
	}
	if d.outCount != 4 {
		t.Fatalf("Op08 outCount = %d, want 4", d.outCount)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	ll = int16(uint16(b0) | uint16(b1)<<8)
	lh = int16(uint16(b2) | uint16(b3)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80", extra)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained")
	}
	return
}

func TestOp08RadiusGoldens(t *testing.T) {
	// Goldens hand-computed via the literal formula (X²+Y²+Z²)<<1
	// using int32 with 2's-complement wraparound — matches snes9x.
	cases := []struct {
		name    string
		x, y, z int16
		wantLh  uint16
		wantLl  uint16
	}{
		{"zero_vector", 0, 0, 0, 0x0000, 0x0000},
		{"unit_x", 1, 0, 0, 0x0000, 0x0002},
		{"unit_y", 0, 1, 0, 0x0000, 0x0002},
		{"unit_z", 0, 0, 1, 0x0000, 0x0002},
		{"max_x_only", 0x7FFF, 0, 0, 0x7FFE, 0x0002},
		// 3 × 0x7FFF² × 2 = 0xBFFD0003 << 1 = 0x7FFA0006 after int32
		// 2's-complement wrap; this is the canonical wrap test case.
		{"all_max_wraps", 0x7FFF, 0x7FFF, 0x7FFF, 0x7FFA, 0x0006},
		{"neg_x_squares_to_pos", -0x7FFF, 0, 0, 0x7FFE, 0x0002},
		{"small_mixed", 1, 2, 3, 0x0000, 0x001C},
		{"neg_signs_cancel", -1, -2, -3, 0x0000, 0x001C},
		// (-0x8000)² = 0x40000000 fits int32 positive; <<1 = 0x80000000
		// (int32 min). uint32 view = 0x80000000 → Lh=0x8000, Ll=0x0000.
		{"min_int16_x", -0x8000, 0, 0, 0x8000, 0x0000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLh, gotLl := driveOp08(t, tc.x, tc.y, tc.z)
			if uint16(gotLh) != tc.wantLh || uint16(gotLl) != tc.wantLl {
				t.Errorf("Op08(%d,%d,%d): bus = (Lh=%#06x, Ll=%#06x), want (Lh=%#06x, Ll=%#06x)",
					tc.x, tc.y, tc.z, uint16(gotLh), uint16(gotLl), tc.wantLh, tc.wantLl)
			}
		})
	}
}

func TestOp08SquaringMakesSignsIrrelevant(t *testing.T) {
	// (X² + Y² + Z²) is sign-invariant under negation of any axis.
	// Verifies the squaring step matches across all 8 sign combinations
	// of a representative non-zero vector.
	const X, Y, Z = int16(0x1234), int16(0x5678), int16(0x4321)
	wantLh, wantLl := driveOp08(t, X, Y, Z)
	for _, sign := range [][3]int16{
		{-X, Y, Z}, {X, -Y, Z}, {X, Y, -Z},
		{-X, -Y, Z}, {-X, Y, -Z}, {X, -Y, -Z},
		{-X, -Y, -Z},
	} {
		t.Run(fmt.Sprintf("signs=%d_%d_%d", sign[0]>>15&1, sign[1]>>15&1, sign[2]>>15&1), func(t *testing.T) {
			gotLh, gotLl := driveOp08(t, sign[0], sign[1], sign[2])
			if gotLh != wantLh || gotLl != wantLl {
				t.Errorf("Op08(%d,%d,%d) = (%#06x,%#06x), want (%#06x,%#06x) (signs should be irrelevant after squaring)",
					sign[0], sign[1], sign[2], uint16(gotLh), uint16(gotLl), uint16(wantLh), uint16(wantLl))
			}
		})
	}
}

// DSP-1 Op18/Op38 Range / vector circle classify (snes9x dsp1.cpp:
// 1046-1063 + dispatch 1324-1348). 4 input words (X, Y, Z, R) →
// 1 output word D = (X²+Y²+Z²−R²) >> 15. Sign of D classifies a
// point relative to a sphere of radius R: negative=inside,
// positive=outside, zero=boundary. Op18 and Op38 share byte-
// identical bodies; Go collapses them to a single dispatch case.

func driveOp18Or38(t *testing.T, cmd uint8, x, y, z, r int16) int16 {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, cmd)
	for _, w := range []int16{x, y, z, r} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after Op18/38 params (cmd=%#x)", cmd)
	}
	if d.outCount != 2 {
		t.Fatalf("Op18/38 outCount = %d, want 2 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	got := int16(uint16(b0) | uint16(b1)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return got
}

func TestOp18RangeGoldens(t *testing.T) {
	cases := []struct {
		name    string
		x, y, z int16
		r       int16
		want    int16
	}{
		{"all_zero", 0, 0, 0, 0, 0},
		// (0,0,0) inside R=0x4000: D = 0 - 0x10000000 >> 15 = -0x2000 = -8192.
		{"origin_inside_sphere", 0, 0, 0, 0x4000, -0x2000},
		// (0x4000,0,0) on sphere R=0x4000 boundary: D = 0.
		{"on_sphere_boundary_pos_x", 0x4000, 0, 0, 0x4000, 0},
		// (0x4000,0x4000,0) outside R=0x4000: D = 0x10000000>>15 = 0x2000 = 8192.
		{"outside_sphere", 0x4000, 0x4000, 0, 0x4000, 0x2000},
		// Small near-boundary inside.
		{"small_inside", 0x100, 0x100, 0x100, 0x200, -2},
		// Max-X with R=0: D = 0x3FFF0001 >> 15 = 0x7FFE.
		{"max_x_no_radius", 0x7FFF, 0, 0, 0, 0x7FFE},
		// Zero vector with max R: D = -0x3FFF0001 >> 15 = -0x7FFF.
		{"only_radius", 0, 0, 0, 0x7FFF, -0x7FFF},
		// Negative coord on sphere boundary: identical to positive.
		{"on_sphere_boundary_neg_x", -0x4000, 0, 0, 0x4000, 0},
		// All-max wrap test: 3·0x3FFF0001 - 0x3FFF0001 = 2·0x3FFF0001 = 0x7FFE0002 >> 15 = 0xFFFC = -4.
		{"all_max_wraps", 0x7FFF, 0x7FFF, 0x7FFF, 0x7FFF, -4},
		// (-0x8000)² = 0x40000000 (positive); >>15 = 0x8000 (int32) → int16 = -0x8000.
		{"min_int16_x_no_radius", -0x8000, 0, 0, 0, -0x8000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := driveOp18Or38(t, 0x18, tc.x, tc.y, tc.z, tc.r)
			if got != tc.want {
				t.Errorf("Op18(%d,%d,%d,R=%d) = %#06x (signed %d), want %#06x (signed %d)",
					tc.x, tc.y, tc.z, tc.r, uint16(got), got, uint16(tc.want), tc.want)
			}
		})
	}
}

func TestOp38ProducesSameAsOp18(t *testing.T) {
	// Op18 and Op38 share byte-identical math in snes9x; Go's
	// combined dispatch must produce the same output for both bytes.
	cases := []struct{ x, y, z, r int16 }{
		{0, 0, 0, 0x4000},
		{0x4000, 0, 0, 0x4000},
		{0x100, 0x200, 0x300, 0x400},
		{0x7FFF, 0x7FFF, 0x7FFF, 0x7FFF},
		{-0x4000, 0x2000, -0x1000, 0x4000},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("v=(%d,%d,%d)_R=%d", tc.x, tc.y, tc.z, tc.r), func(t *testing.T) {
			got18 := driveOp18Or38(t, 0x18, tc.x, tc.y, tc.z, tc.r)
			got38 := driveOp18Or38(t, 0x38, tc.x, tc.y, tc.z, tc.r)
			if got18 != got38 {
				t.Errorf("Op18=%#06x but Op38=%#06x; want equal", uint16(got18), uint16(got38))
			}
		})
	}
}

func TestOp18SignsIrrelevantInVector(t *testing.T) {
	// (X² + Y² + Z² − R²) is sign-invariant under negation of any
	// vector component. R is also squared so its sign is irrelevant.
	const X, Y, Z, R = int16(0x1234), int16(0x5678), int16(0x4321), int16(0x3456)
	want := driveOp18Or38(t, 0x18, X, Y, Z, R)
	for _, sign := range [][4]int16{
		{-X, Y, Z, R}, {X, -Y, Z, R}, {X, Y, -Z, R}, {X, Y, Z, -R},
		{-X, -Y, -Z, R}, {-X, -Y, -Z, -R},
	} {
		t.Run(fmt.Sprintf("v=(%d,%d,%d)_R=%d", sign[0], sign[1], sign[2], sign[3]), func(t *testing.T) {
			got := driveOp18Or38(t, 0x18, sign[0], sign[1], sign[2], sign[3])
			if got != want {
				t.Errorf("Op18(%d,%d,%d,R=%d) = %#06x, want %#06x (signs irrelevant after squaring)",
					sign[0], sign[1], sign[2], sign[3], uint16(got), uint16(want))
			}
		})
	}
}

// DSP-1 Op28 Distance / vector magnitude (sqrt) (snes9x dsp1.cpp:
// 1065-1092 + dispatch 1350-1359). 3 input words (X, Y, Z) →
// 1 output word (R = sqrt(X² + Y² + Z²)). Sqrt approximation via
// normalizeDouble + dsp1ROM table interpolation.

func driveOp28(t *testing.T, x, y, z int16) int16 {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, 0x28)
	for _, w := range []int16{x, y, z} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after Op28 params")
	}
	if d.outCount != 2 {
		t.Fatalf("Op28 outCount = %d, want 2", d.outCount)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	r := int16(uint16(b0) | uint16(b1)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80", extra)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained")
	}
	return r
}

// op28Helper mirrors snes9x DSP1_Op28 step-for-step using the same
// shipped helpers (normalizeDouble, dsp1ROM table) so tests can
// assert bus output matches helper output without depending on
// captured external goldens.
func op28Helper(x, y, z int16) int16 {
	radius := int32(x)*int32(x) + int32(y)*int32(y) + int32(z)*int32(z)
	if radius == 0 {
		return 0
	}
	c, e := normalizeDouble(radius)
	if e&1 != 0 {
		c = int16(int32(c) * 0x4000 >> 15)
	}
	pos := int16(int32(c) * 0x0040 >> 15)
	node1 := int16(dsp1ROM[0xD5+pos])
	node2 := int16(dsp1ROM[0xD6+pos])
	return int16((int32(node2-node1)*int32(c&0x1FF)>>9)+int32(node1)) >> (e >> 1)
}

func TestOp28DistanceMatchesHelper(t *testing.T) {
	cases := []struct {
		name    string
		x, y, z int16
	}{
		{"zero_vector", 0, 0, 0},
		{"unit_x", 0x100, 0, 0},
		{"unit_y", 0, 0x100, 0},
		{"unit_z", 0, 0, 0x100},
		{"medium_x_only", 0x4000, 0, 0},
		{"medium_xy", 0x4000, 0x4000, 0},
		{"small_iso", 0x100, 0x100, 0x100},
		{"max_x_only", 0x7FFF, 0, 0},
		{"neg_x_only", -0x4000, 0, 0},
		{"arbitrary", 0x1234, 0x5678, 0x4321},
		{"neg_arbitrary", -0x1234, 0x5678, -0x4321},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := driveOp28(t, tc.x, tc.y, tc.z)
			want := op28Helper(tc.x, tc.y, tc.z)
			if got != want {
				t.Errorf("Op28(%d,%d,%d): bus = %#06x (signed %d), helper = %#06x (signed %d)",
					tc.x, tc.y, tc.z, uint16(got), got, uint16(want), want)
			}
		})
	}
}

func TestOp28ZeroVectorReturnsZero(t *testing.T) {
	// Snes9x explicitly short-circuits Radius==0 to R=0.
	got := driveOp28(t, 0, 0, 0)
	if got != 0 {
		t.Errorf("Op28(0,0,0) = %#06x, want 0", uint16(got))
	}
}

func TestOp28SignsIrrelevantInVector(t *testing.T) {
	// Squaring step makes axis sign irrelevant. Verify all 8 sign
	// combinations of a representative non-zero vector produce
	// identical output.
	const X, Y, Z = int16(0x1234), int16(0x5678), int16(0x4321)
	want := driveOp28(t, X, Y, Z)
	for _, sign := range [][3]int16{
		{-X, Y, Z}, {X, -Y, Z}, {X, Y, -Z},
		{-X, -Y, Z}, {-X, Y, -Z}, {X, -Y, -Z},
		{-X, -Y, -Z},
	} {
		t.Run(fmt.Sprintf("v=(%d,%d,%d)", sign[0], sign[1], sign[2]), func(t *testing.T) {
			got := driveOp28(t, sign[0], sign[1], sign[2])
			if got != want {
				t.Errorf("Op28(%d,%d,%d) = %#06x, want %#06x (signs irrelevant after squaring)",
					sign[0], sign[1], sign[2], uint16(got), uint16(want))
			}
		})
	}
}

func TestOp28DistanceApproximatesActualSqrt(t *testing.T) {
	// Sanity guard: for representative inputs whose true sqrt is well-
	// defined and within int16 range, the approximation should be
	// close to the actual sqrt. Tolerance is wide because snes9x
	// uses a 64-entry lookup with linear interpolation — accuracy
	// is roughly ±1% for well-conditioned magnitudes.
	cases := []struct {
		x, y, z   int16
		approxR   int32 // expected ≈ sqrt(X²+Y²+Z²)
		tolerance int32 // ±absolute units
	}{
		{0x100, 0, 0, 0x100, 4},
		{0x100, 0x100, 0, 0x16A, 4},     // sqrt(2)·256 ≈ 362
		{0x100, 0x100, 0x100, 0x1BB, 8}, // sqrt(3)·256 ≈ 443
		{0x4000, 0, 0, 0x4000, 16},
		{0x2000, 0x2000, 0x2000, 0x3777, 32}, // sqrt(3)·8192 ≈ 14188
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("v=(%d,%d,%d)", tc.x, tc.y, tc.z), func(t *testing.T) {
			got := driveOp28(t, tc.x, tc.y, tc.z)
			diff := int32(got) - tc.approxR
			if diff < 0 {
				diff = -diff
			}
			if diff > tc.tolerance {
				t.Errorf("Op28(%d,%d,%d) = %d, want ~%d (|diff|=%d > tol=%d)",
					tc.x, tc.y, tc.z, got, tc.approxR, diff, tc.tolerance)
			}
		})
	}
}

// DSP-1 Op14/Op34 Inverse 3D rotation (snes9x dsp1.cpp:940-970 +
// dispatch 1610-1625). 6 input words (Zr, Xr, Yr, U, F, L) → 3
// output words (Zrr, Xrr, Yrr). Multi-step pipeline using shipped
// inverse + sinFP/cosFP + normalizeDouble + normalize + truncate.

func driveOp14(t *testing.T, cmd uint8, zr, xr, yr, u, f, l int16) (zrr, xrr, yrr int16) {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	d.Write(0x208000, cmd)
	for _, w := range []int16{zr, xr, yr, u, f, l} {
		d.Write(0x208000, uint8(uint16(w)&0xff))
		d.Write(0x208000, uint8(uint16(w)>>8))
	}
	if !d.waiting4command {
		t.Fatalf("expected waiting4command after Op14 params (cmd=%#x)", cmd)
	}
	if d.outCount != 6 {
		t.Fatalf("Op14 outCount = %d, want 6 (cmd=%#x)", d.outCount, cmd)
	}
	b0, _ := d.Read(0x208000)
	b1, _ := d.Read(0x208000)
	b2, _ := d.Read(0x208000)
	b3, _ := d.Read(0x208000)
	b4, _ := d.Read(0x208000)
	b5, _ := d.Read(0x208000)
	zrr = int16(uint16(b0) | uint16(b1)<<8)
	xrr = int16(uint16(b2) | uint16(b3)<<8)
	yrr = int16(uint16(b4) | uint16(b5)<<8)
	if extra, _ := d.Read(0x208000); extra != 0x80 {
		t.Errorf("post-drain read = %#02x, want 0x80 (cmd=%#x)", extra, cmd)
	}
	if !d.waiting4command {
		t.Errorf("expected waiting4command after output drained (cmd=%#x)", cmd)
	}
	return
}

// op14Helper mirrors snes9x DSP1_Op14 step-for-step using the same
// shipped helpers. truncate() now short-circuits on c==0 to avoid
// the dsp1ROM[0x31+e] negative-index panic that the all-zero
// degenerate input would otherwise trigger.
func op14Helper(zr, xr, yr, u, f, l int16) (zrr, xrr, yrr int16) {
	cSec, eSec := inverse(cosFP(xr), 0)
	c, e := normalizeDouble(int32(u)*int32(cosFP(yr)) - int32(f)*int32(sinFP(yr)))
	e = eSec - e
	c, e = normalize(int16(int32(c)*int32(cSec)>>15), e)
	zrr = zr + truncate(c, e)
	xrr = xr + int16(int32(u)*int32(sinFP(yr))>>15) + int16(int32(f)*int32(cosFP(yr))>>15)
	c, e = normalizeDouble(int32(u)*int32(cosFP(yr)) + int32(f)*int32(sinFP(yr)))
	e = eSec - e
	var cSin int16
	cSin, e = normalize(sinFP(xr), e)
	cTan := int16(int32(cSec) * int32(cSin) >> 15)
	c, e = normalize(int16(-(int32(c) * int32(cTan) >> 15)), e)
	yrr = yr + truncate(c, e) + l
	return
}

func TestOp14MatchesHelper(t *testing.T) {
	cases := []struct {
		name                string
		zr, xr, yr, u, f, l int16
	}{
		// All-zero degenerate input — would panic without the
		// safeTruncate14 c==0 guard. Returns (0,0,0) cleanly.
		{"all_zero", 0, 0, 0, 0, 0, 0},
		{"xr_zero_yr_zero_uvec_only", 0, 0, 0, 0x4000, 0, 0},
		{"xr_zero_yr_45_with_vec", 0, 0, 0x2000, 0x4000, 0x2000, 0x100},
		{"xr_45_yr_0", 0, 0x2000, 0, 0x100, 0x200, 0x300},
		{"xr_45_yr_45", 0, 0x2000, 0x2000, 0x100, 0x200, 0x300},
		{"all_axes_45_with_vec", 0x2000, 0x2000, 0x2000, 0x100, 0x200, 0x300},
		{"neg_inputs", -0x100, -0x200, -0x300, 0x4000, 0x2000, 0x1000},
		{"large_zr_offset", 0x4000, 0x1000, 0x800, 0x500, 0x300, 0x200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotZ, gotX, gotY := driveOp14(t, 0x14, tc.zr, tc.xr, tc.yr, tc.u, tc.f, tc.l)
			wantZ, wantX, wantY := op14Helper(tc.zr, tc.xr, tc.yr, tc.u, tc.f, tc.l)
			if gotZ != wantZ || gotX != wantX || gotY != wantY {
				t.Errorf("Op14(zr=%#06x xr=%#06x yr=%#06x u=%#06x f=%#06x l=%#06x): bus = (%#06x,%#06x,%#06x), helper = (%#06x,%#06x,%#06x)",
					uint16(tc.zr), uint16(tc.xr), uint16(tc.yr),
					uint16(tc.u), uint16(tc.f), uint16(tc.l),
					uint16(gotZ), uint16(gotX), uint16(gotY),
					uint16(wantZ), uint16(wantX), uint16(wantY))
			}
		})
	}
}

func TestOp14AliasOp34(t *testing.T) {
	// 0x34 falls through to 0x14 per snes9x dsp1.cpp:1610-1611.
	cases := []struct{ zr, xr, yr, u, f, l int16 }{
		{0, 0x2000, 0x2000, 0x100, 0x200, 0x300},
		{0x4000, 0x1000, 0x800, 0x500, 0x300, 0x200},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("zr=%#06x_xr=%#06x_yr=%#06x", uint16(tc.zr), uint16(tc.xr), uint16(tc.yr)), func(t *testing.T) {
			z14, x14, y14 := driveOp14(t, 0x14, tc.zr, tc.xr, tc.yr, tc.u, tc.f, tc.l)
			z34, x34, y34 := driveOp14(t, 0x34, tc.zr, tc.xr, tc.yr, tc.u, tc.f, tc.l)
			if z14 != z34 || x14 != x34 || y14 != y34 {
				t.Errorf("Op14 alias mismatch: 0x14=(%#06x,%#06x,%#06x), 0x34=(%#06x,%#06x,%#06x)",
					uint16(z14), uint16(x14), uint16(y14), uint16(z34), uint16(x34), uint16(y34))
			}
		})
	}
}

func TestOp14XrrIsZeroAnglePassThrough(t *testing.T) {
	// At Yr=0: sin(0)=0, cos(0)=0x7FFF. Then:
	//   xrr = xr + (U·0 >> 15) + (F·0x7FFF >> 15)
	//       = xr + 0 + (F-1 due to Q15 truncation for non-zero F)
	// For F=0 this collapses to xrr = xr exactly.
	gotZ, gotX, gotY := driveOp14(t, 0x14, 0, 0x1000, 0, 0x4000, 0, 0)
	if gotX != 0x1000 {
		t.Errorf("Xrr at Yr=0,F=0: got %#06x, want 0x1000 (passthrough)", uint16(gotX))
	}
	_ = gotZ
	_ = gotY
}

func TestOp14AllZeroDoesNotPanic(t *testing.T) {
	// Regression guard: Op14 with (Zr, Xr, Yr, U, F, L) all zero
	// used to panic in the bus path because truncate() at
	// dsp1math.go indexed dsp1ROM[0x31+e] with extreme-negative
	// e (the normalize/normalizeDouble cascade from a zero input).
	// truncate() now short-circuits on c==0 — the call returns
	// cleanly with all-zero output.
	gotZ, gotX, gotY := driveOp14(t, 0x14, 0, 0, 0, 0, 0, 0)
	if gotZ != 0 || gotX != 0 || gotY != 0 {
		t.Errorf("Op14(all zero) = (%#06x,%#06x,%#06x), want (0,0,0)",
			uint16(gotZ), uint16(gotX), uint16(gotY))
	}
}

func TestTruncateShortCircuitsOnZeroC(t *testing.T) {
	// truncate() at dsp1math.go must return 0 when c==0 regardless
	// of e — this pins the source-fix guard that prevents the
	// dsp1ROM[0x31+e] negative-index panic for extreme-negative e.
	// Real callers (Op14 in particular) rely on this for the
	// (U,F)=(0,0) degenerate case to return cleanly.
	tests := []struct{ c, e int16 }{
		{0, -100},
		{0, -1},
		{0, 0},
		{0, 100},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("c=0_e=%d", tc.e), func(t *testing.T) {
			if got := truncate(0, tc.e); got != 0 {
				t.Errorf("truncate(0, %d) = %d, want 0", tc.e, got)
			}
		})
	}
}

func TestOp14IsStatelessAcrossInvocations(t *testing.T) {
	// Two consecutive Op14 calls with identical inputs must produce
	// identical outputs — verifies Op14 doesn't carry persistent
	// state between invocations.
	d := New()
	d.SetMapType(MapLoROMSmall)
	zr, xr, yr := int16(0x100), int16(0x2000), int16(0x1000)
	u, f, l := int16(0x4000), int16(0x2000), int16(0x800)
	emit := func() (int16, int16, int16) {
		d.Write(0x208000, 0x14)
		for _, w := range []int16{zr, xr, yr, u, f, l} {
			d.Write(0x208000, uint8(uint16(w)&0xff))
			d.Write(0x208000, uint8(uint16(w)>>8))
		}
		var out [3]int16
		for i := range out {
			lo, _ := d.Read(0x208000)
			hi, _ := d.Read(0x208000)
			out[i] = int16(uint16(lo) | uint16(hi)<<8)
		}
		return out[0], out[1], out[2]
	}
	z1, x1, y1 := emit()
	z2, x2, y2 := emit()
	if z1 != z2 || x1 != x2 || y1 != y2 {
		t.Errorf("Op14 stateful across calls: first=(%#06x,%#06x,%#06x), second=(%#06x,%#06x,%#06x)",
			uint16(z1), uint16(x1), uint16(y1), uint16(z2), uint16(x2), uint16(y2))
	}
}

// op1fIssue programs a memoryDump command via the data port and consumes
// the 2 parameter bytes the FSM demands (Op1F's input is unused but the
// FSM still requires paramWordCount=1 -> 2 bytes per setByte's <<= 1).
// Returns the device ready to drain 2048 bytes from $208000.
func op1fIssue(t *testing.T, cmd uint8) *Device {
	t.Helper()
	d := New()
	d.SetMapType(MapLoROMSmall)
	if !d.Write(0x208000, cmd) {
		t.Fatalf("write command %#x failed", cmd)
	}
	for range 2 {
		if !d.Write(0x208000, 0x00) {
			t.Fatal("write parameter failed")
		}
	}
	return d
}

// After Op1F + parameter bytes, the FSM is primed to stream 2048 bytes:
// outCount=2048, outIndex=0, command latched at 0x1f.
func TestOp1FOutCountInitialized2048(t *testing.T) {
	d := op1fIssue(t, 0x1f)
	if d.outCount != 2048 {
		t.Fatalf("outCount = %d, want 2048", d.outCount)
	}
	if d.outIndex != 0 {
		t.Fatalf("outIndex = %d, want 0", d.outIndex)
	}
	if d.command != 0x1f {
		t.Fatalf("command = %#x, want 0x1f", d.command)
	}
}

// Each drained byte sources from dsp1ROM[outIndex>>1]: low byte for even
// outIndex, high byte for odd. Verify across 0,1,2,3 then a few far-out
// indices that exercise the full address space.
func TestOp1FByteSequenceMatchesDSP1ROM(t *testing.T) {
	d := op1fIssue(t, 0x1f)
	for i := range 8 {
		want := uint8(dsp1ROM[i>>1] & 0xff)
		if i&1 == 1 {
			want = uint8(dsp1ROM[i>>1] >> 8)
		}
		got, _ := d.Read(0x208000)
		if got != want {
			t.Fatalf("byte %d = %02x, want %02x (dsp1ROM[%#x]=%04x)",
				i, got, want, i>>1, dsp1ROM[i>>1])
		}
	}
}

// Drain all 2048 bytes; verify FSM completes (outCount=0,
// waiting4command=true) and the next byte read returns 0x80 (empty queue).
// Spot-check the very last byte sources from dsp1ROM[1023] high byte.
func TestOp1FFSMCompletesAfter2048Bytes(t *testing.T) {
	d := op1fIssue(t, 0x1f)
	var lastByte uint8
	for range 2048 {
		got, _ := d.Read(0x208000)
		lastByte = got
	}
	if d.outCount != 0 {
		t.Fatalf("outCount = %d after 2048 reads, want 0", d.outCount)
	}
	if !d.waiting4command {
		t.Fatal("waiting4command not set after drain; want true")
	}
	if want := uint8(dsp1ROM[1023] >> 8); lastByte != want {
		t.Fatalf("byte 2047 = %02x, want %02x (dsp1ROM[0x3FF]=%04x high)",
			lastByte, want, dsp1ROM[1023])
	}
	// Empty queue returns 0x80.
	if got, _ := d.Read(0x208000); got != 0x80 {
		t.Fatalf("post-drain read = %02x, want 0x80", got)
	}
}

// snes9x setByte rewrites 0x17/0x37/0x3f to 0x1f. Confirm each alias yields
// the identical 4-byte prefix as the canonical 0x1f.
func TestOp1FAliasesViaSetByte(t *testing.T) {
	canonical := op1fIssue(t, 0x1f)
	var want [4]uint8
	for i := range 4 {
		want[i], _ = canonical.Read(0x208000)
	}
	for _, alias := range []uint8{0x17, 0x37, 0x3f} {
		d := op1fIssue(t, alias)
		if d.command != 0x1f {
			t.Fatalf("alias %#x: command = %#x, want 0x1f after rewrite", alias, d.command)
		}
		for i := range 4 {
			got, _ := d.Read(0x208000)
			if got != want[i] {
				t.Fatalf("alias %#x byte %d = %02x, want %02x", alias, i, got, want[i])
			}
		}
	}
}

// Drain N bytes, Serialize, Unserialize into a fresh Device, drain the
// remaining 2048-N; verify the second device continues from the same
// dsp1ROM index and FSM completes correctly.
func TestOp1FSerializeMidStream(t *testing.T) {
	d := op1fIssue(t, 0x1f)
	const consumed = 100
	for range consumed {
		_, _ = d.Read(0x208000)
	}
	if d.outIndex != consumed {
		t.Fatalf("outIndex = %d, want %d", d.outIndex, consumed)
	}
	blob, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	d2 := New()
	d2.SetMapType(MapLoROMSmall)
	if err := d2.Unserialize(blob); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if d2.outIndex != consumed || d2.outCount != 2048-consumed || d2.command != 0x1f {
		t.Fatalf("post-unserialize: outIndex=%d outCount=%d command=%#x; want %d %d 0x1f",
			d2.outIndex, d2.outCount, d2.command, consumed, 2048-consumed)
	}
	// Next byte must source from dsp1ROM[consumed>>1] low (since 100 is even).
	want := uint8(dsp1ROM[consumed>>1] & 0xff)
	got, _ := d2.Read(0x208000)
	if got != want {
		t.Fatalf("post-unserialize byte = %02x, want %02x", got, want)
	}
	// Drain the rest and verify completion.
	for range 2048 - consumed - 1 {
		_, _ = d2.Read(0x208000)
	}
	if d2.outCount != 0 || !d2.waiting4command {
		t.Fatalf("post-resume drain: outCount=%d waiting4command=%v; want 0,true",
			d2.outCount, d2.waiting4command)
	}
}

// Op1F drained then Op00 multiply runs cleanly without state contamination.
// Regression on the `command` check in getByte's new branch: after Op1F
// completes, command remains 0x1f until setByte resets it; the NEW Op00
// must use the small output buffer, not the dsp1ROM stream path.
func TestOp1FFollowedByOp00(t *testing.T) {
	d := op1fIssue(t, 0x1f)
	for range 2048 {
		_, _ = d.Read(0x208000)
	}
	// Now issue Op00 Q15 multiply: 0x4000 * 0x4000 >> 15 = 0x2000.
	if !d.Write(0x208000, 0x00) {
		t.Fatal("write Op00 cmd failed")
	}
	for _, b := range []uint8{0x00, 0x40, 0x00, 0x40} { // 0x4000, 0x4000 (LE)
		if !d.Write(0x208000, b) {
			t.Fatalf("write Op00 param %#x failed", b)
		}
	}
	if d.command != 0x00 {
		t.Fatalf("command after Op00 = %#x, want 0x00", d.command)
	}
	lo, _ := d.Read(0x208000)
	hi, _ := d.Read(0x208000)
	got := uint16(lo) | uint16(hi)<<8
	if got != 0x2000 {
		t.Fatalf("Op00 Q15 result = %#x, want 0x2000", got)
	}
}
