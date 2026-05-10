package dsp1

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

type MapType uint8

const (
	MapLoROMSmall MapType = iota
	MapLoROMLarge
	MapHiROM
)

// Device emulates the DSP-1 coprocessor's bus-facing command/parameter/result
// state machine. Behaviour mirrors snes9x dsp1.cpp DSP1SetByte/DSP1GetByte:
// even addresses access the data port; odd addresses always read 0x80 and
// ignore writes. Op math (Op 02/0A/06/04 etc.) is not implemented in this
// slice; unknown commands fall through to the waiting-for-command state.
type Device struct {
	MapType MapType

	command         uint8
	waiting4command bool
	firstParameter  bool
	parameters      [16]uint8
	inIndex         uint8
	inCount         uint8
	output          [32]uint8
	outIndex        uint8
	outCount        uint16

	// Projection state populated by Op 0x02 (Parameter) and consumed by
	// downstream Op 0x06 / 0x0A. Names mirror snes9x DSP1 globals so the
	// port can be diffed line-for-line against dsp1.cpp.
	sinAas, cosAas, sinAzs, cosAzs int16
	nx, ny, nz                     int16
	centreX, centreY               int16
	gx, gy, gz                     int16
	cLes, eLes, gLes               int16
	vplaneC, vplaneE               int16
	sinAZS, cosAZS                 int16
	secAZS_C1, secAZS_E1           int16
	secAZS_C2, secAZS_E2           int16
	vOffset                        int16

	// Op 0x0A streaming state. snes9x increments DSP1.Op0AVS once per
	// raster output so successive drains advance the scan line.
	op0AVS int16

	// Attitude matrix A populated by Op 0x01. Consumed by Op 0x0D
	// (Objective matrix A) when that op is implemented; persisted across
	// commands so successive Op0D/0x1D calls reuse the matrix.
	matrixA [3][3]int16
	matrixB [3][3]int16
	matrixC [3][3]int16
}

func New() *Device {
	d := &Device{}
	d.reset()
	return d
}

func (d *Device) reset() {
	d.command = 0
	d.waiting4command = true
	d.firstParameter = true
	d.inIndex = 0
	d.inCount = 0
	d.outIndex = 0
	d.outCount = 0
	for i := range d.parameters {
		d.parameters[i] = 0
	}
	for i := range d.output {
		d.output[i] = 0
	}
}

func (d *Device) SetMapType(mapType MapType) {
	d.MapType = mapType
}

func (d *Device) mapped(addr uint32) bool {
	bank := (addr >> 16) & 0xFF
	off := uint16(addr & 0xFFFF)
	switch d.MapType {
	case MapLoROMLarge:
		if !((bank >= 0x60 && bank <= 0x6F) || (bank >= 0xE0 && bank <= 0xEF)) {
			return false
		}
		return off <= 0x7FFF
	case MapHiROM:
		if !((bank >= 0x00 && bank <= 0x1F) || (bank >= 0x80 && bank <= 0x9F)) {
			return false
		}
		return off >= 0x6000 && off <= 0x7FFF
	default:
		if !((bank >= 0x20 && bank <= 0x3F) || (bank >= 0xA0 && bank <= 0xBF)) {
			return false
		}
		return off >= 0x8000 && off <= 0xFFFF
	}
}

func (d *Device) Read(addr uint32) (uint8, bool) {
	if !d.mapped(addr) {
		return 0, false
	}
	if (addr & 1) != 0 {
		return 0x80, true
	}
	return d.getByte(), true
}

func (d *Device) Write(addr uint32, val uint8) bool {
	if !d.mapped(addr) {
		return false
	}
	if (addr & 1) == 0 {
		d.setByte(val)
		return true
	}
	return false
}

// getByte mirrors snes9x DSP1GetByte. Returns 0x80 when no result bytes are
// queued; otherwise drains the output buffer one byte at a time. Op 0A/1A
// re-loads its raster output and Op 1F re-fills from DSP1ROM, but those
// branches are stage-3 work; this slice keeps the empty-queue path only.
func (d *Device) getByte() uint8 {
	if d.outCount == 0 {
		return 0x80
	}
	t := d.output[d.outIndex]
	d.outIndex++
	d.outCount--
	if d.outCount == 0 {
		// snes9x dsp1.cpp DSP1GetByte: when the buffer drains and the active
		// command is 0x0A or 0x1A (Raster), automatically re-run Op0A,
		// advance Op0AVS, and refill the 8-byte output buffer for streaming
		// mode. All other commands return to waiting4command.
		if d.command == 0x0a || d.command == 0x1a {
			d.executeOp0A()
			d.outIndex = 0
			d.outCount = 8
		} else {
			d.waiting4command = true
		}
	}
	return t
}

// setByte mirrors snes9x DSP1SetByte. Either accepts a new command byte and
// programs the parameter byte count, or stores a parameter byte and triggers
// command execution when the parameter buffer fills.
func (d *Device) setByte(b uint8) {
	if d.waiting4command {
		d.command = b
		d.inIndex = 0
		d.waiting4command = false
		d.firstParameter = true
		d.inCount = paramWordCount(b)
		// snes9x rewrites aliases for 0x?A and 0x17/37/3F.
		switch b {
		case 0x1a, 0x2a, 0x3a:
			d.command = 0x1a
		case 0x17, 0x37, 0x3f:
			d.command = 0x1f
		}
		if d.inCount == 0 {
			// snes9x default + case 0x80: no-op, return to waiting4command.
			d.waiting4command = true
			d.firstParameter = true
		}
		d.inCount <<= 1 // word count -> byte count
		// Command byte itself does not consume a parameter slot; snes9x
		// passes the post-switch first_parameter && in_count!=0 silent
		// clause for this case.
		return
	}
	d.parameters[d.inIndex] = b
	wasFirst := d.firstParameter
	d.firstParameter = false
	d.inIndex++
	if wasFirst && b == 0x80 {
		// snes9x dsp1.cpp:1244 escape: bare 0x80 mid-stream returns to wait.
		d.waiting4command = true
		d.firstParameter = false
		return
	}
	if d.inCount > 0 {
		d.inCount--
		if d.inCount == 0 {
			d.waiting4command = true
			d.outIndex = 0
			d.execute()
		}
	}
}

// paramWordCount returns the parameter word count for a command byte, or 0
// for unknown / 0x80 / pure-status commands. Mirrors the switch in snes9x
// DSP1SetByte (dsp1.cpp:1154+). Word counts; setByte shifts to bytes.
func paramWordCount(b uint8) uint8 {
	switch b {
	case 0x00, 0x10, 0x20, 0x30, 0x04, 0x24, 0x0e, 0x1e, 0x2e, 0x3e:
		return 2
	case 0x08, 0x28, 0x06, 0x16, 0x26, 0x36, 0x0c, 0x2c, 0x0d, 0x09, 0x39, 0x3d,
		0x1d, 0x19, 0x2d, 0x29, 0x03, 0x33, 0x13, 0x23, 0x0b, 0x3b, 0x1b, 0x2b:
		return 3
	case 0x18, 0x38, 0x01, 0x05, 0x35, 0x31, 0x11, 0x15, 0x21, 0x25:
		return 4
	case 0x1c, 0x3c, 0x14, 0x34:
		return 6
	case 0x02, 0x12, 0x22, 0x32:
		return 7
	case 0x0a, 0x1a, 0x2a, 0x3a, 0x07, 0x0f, 0x17, 0x27, 0x2f, 0x37, 0x3f, 0x1f:
		return 1
	default:
		return 0
	}
}

// execute dispatches the completed command.
func (d *Device) execute() {
	switch d.command {
	case 0x02, 0x12, 0x22, 0x32:
		// snes9x dsp1.cpp DSP1_Op02 / DSP1_Parameter. Reads 7 input words
		// (Fx,Fy,Fz,Lfe,Les,Aas,Azs), writes 4 output words (Vof,Vva,Cx,Cy)
		// and persists the projection state used by Op 0x0A/0x06.
		fx := readWordLE(d.parameters[0:])
		fy := readWordLE(d.parameters[2:])
		fz := readWordLE(d.parameters[4:])
		lfe := readWordLE(d.parameters[6:])
		les := readWordLE(d.parameters[8:])
		aas := readWordLE(d.parameters[10:])
		azs := readWordLE(d.parameters[12:])
		vof, vva, cx, cy := d.parameter(fx, fy, fz, lfe, les, aas, azs)
		writeWordLE(d.output[0:], vof)
		writeWordLE(d.output[2:], vva)
		writeWordLE(d.output[4:], cx)
		writeWordLE(d.output[6:], cy)
		d.outCount = 8
	case 0x0a, 0x1a:
		// snes9x dsp1.cpp case 0x0a/0x1a/0x2a/0x3a (post-alias rewrite):
		//   Op0AVS = (int16) READ_WORD(&parameters[0]); DSP1_Op0A();
		//   out_count=8; output ← Op0AA..Op0AD little-endian; in_index=0.
		// Op0A reads projection state set by Op 0x02; if Op02 has not run,
		// the result is whatever zero-state produces (matches snes9x).
		d.op0AVS = readWordLE(d.parameters[0:])
		d.executeOp0A()
		d.outCount = 8
	case 0x01, 0x05, 0x31, 0x35:
		// snes9x dsp1.cpp DSP1_Op01: "Set attitude matrix A". 4 input words
		// (m, Zr, Yr, Xr); no output. Builds a 3x3 rotation matrix from
		// Z/Y/X Euler angles scaled by m, stored in matrixA for later
		// consumption by Op 0x0D (Objective matrix A).
		m := readWordLE(d.parameters[0:])
		zr := readWordLE(d.parameters[2:])
		yr := readWordLE(d.parameters[4:])
		xr := readWordLE(d.parameters[6:])
		d.attitudeMatrix(&d.matrixA, m, zr, yr, xr)
		d.outCount = 0
	case 0x11, 0x15:
		// snes9x dsp1.cpp DSP1_Op11: "Set attitude matrix B". Identical
		// structure to Op01 but writes matrixB.
		m := readWordLE(d.parameters[0:])
		zr := readWordLE(d.parameters[2:])
		yr := readWordLE(d.parameters[4:])
		xr := readWordLE(d.parameters[6:])
		d.attitudeMatrix(&d.matrixB, m, zr, yr, xr)
		d.outCount = 0
	case 0x21, 0x25:
		// snes9x dsp1.cpp DSP1_Op21: "Set attitude matrix C". Identical
		// structure to Op01 but writes matrixC.
		m := readWordLE(d.parameters[0:])
		zr := readWordLE(d.parameters[2:])
		yr := readWordLE(d.parameters[4:])
		xr := readWordLE(d.parameters[6:])
		d.attitudeMatrix(&d.matrixC, m, zr, yr, xr)
		d.outCount = 0
	case 0x0d, 0x09, 0x39, 0x3d:
		// snes9x dsp1.cpp DSP1_Op0D: "Objective matrix A". 3 input words
		// (X,Y,Z), 3 output words (F,L,U) = matrixA · (X,Y,Z) with each
		// row's products summed after >>15. Uses matrixA from Op 0x01.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		f, l, u := objectiveMatrix(&d.matrixA, x, y, z)
		writeWordLE(d.output[0:], f)
		writeWordLE(d.output[2:], l)
		writeWordLE(d.output[4:], u)
		d.outCount = 6
	case 0x1d, 0x19:
		// snes9x dsp1.cpp DSP1_Op1D: "Objective matrix B". Mirrors Op0D
		// against matrixB from Op 0x11.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		f, l, u := objectiveMatrix(&d.matrixB, x, y, z)
		writeWordLE(d.output[0:], f)
		writeWordLE(d.output[2:], l)
		writeWordLE(d.output[4:], u)
		d.outCount = 6
	case 0x2d, 0x29:
		// snes9x dsp1.cpp DSP1_Op2D: "Objective matrix C". Mirrors Op0D
		// against matrixC from Op 0x21.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		f, l, u := objectiveMatrix(&d.matrixC, x, y, z)
		writeWordLE(d.output[0:], f)
		writeWordLE(d.output[2:], l)
		writeWordLE(d.output[4:], u)
		d.outCount = 6
	case 0x03, 0x33:
		// snes9x dsp1.cpp DSP1_Op03 (lines 907-916) + dispatch at 1536-1548:
		// "Subjective matrix A". 3 input words (F,L,U), 3 output words
		// (X,Y,Z) = M^T_A · (F,L,U) — the transpose of Op0D, implementing
		// world→object inverse projection for the orthogonal rotation
		// matrix produced by Op01. Aliases 0x33 and 0x03 share the handler.
		f := readWordLE(d.parameters[0:])
		l := readWordLE(d.parameters[2:])
		u := readWordLE(d.parameters[4:])
		x, y, z := subjectiveMatrix(&d.matrixA, f, l, u)
		writeWordLE(d.output[0:], x)
		writeWordLE(d.output[2:], y)
		writeWordLE(d.output[4:], z)
		d.outCount = 6
	case 0x13:
		// snes9x dsp1.cpp DSP1_Op13 (lines 918-927) + dispatch at 1550-1561.
		// Mirrors Op03 against matrixB from Op 0x11.
		f := readWordLE(d.parameters[0:])
		l := readWordLE(d.parameters[2:])
		u := readWordLE(d.parameters[4:])
		x, y, z := subjectiveMatrix(&d.matrixB, f, l, u)
		writeWordLE(d.output[0:], x)
		writeWordLE(d.output[2:], y)
		writeWordLE(d.output[4:], z)
		d.outCount = 6
	case 0x23:
		// snes9x dsp1.cpp DSP1_Op23 (lines 929-938) + dispatch at 1563-1574.
		// Mirrors Op03 against matrixC from Op 0x21.
		f := readWordLE(d.parameters[0:])
		l := readWordLE(d.parameters[2:])
		u := readWordLE(d.parameters[4:])
		x, y, z := subjectiveMatrix(&d.matrixC, f, l, u)
		writeWordLE(d.output[0:], x)
		writeWordLE(d.output[2:], y)
		writeWordLE(d.output[4:], z)
		d.outCount = 6
	case 0x0b, 0x3b:
		// snes9x dsp1.cpp DSP1_Op0B (lines 1006-1013) + dispatch at
		// 1576-1586. Scalar matrix product against matrixA row 0:
		//   S = (X·m[0][0] + Y·m[0][1] + Z·m[0][2]) >> 15
		// 3 input words (X, Y, Z), 1 output word (S). Aliases 0x0B
		// and 0x3B share the handler.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		writeWordLE(d.output[0:], scalarMatrix(&d.matrixA, x, y, z))
		d.outCount = 2
	case 0x1b:
		// snes9x dsp1.cpp DSP1_Op1B (lines 1015-1023) + dispatch at
		// 1588-1597. Mirrors Op0B against matrixB from Op 0x11.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		writeWordLE(d.output[0:], scalarMatrix(&d.matrixB, x, y, z))
		d.outCount = 2
	case 0x2b:
		// snes9x dsp1.cpp DSP1_Op2B (lines 1025-1032) + dispatch at
		// 1599-1608. Mirrors Op0B against matrixC from Op 0x21.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		writeWordLE(d.output[0:], scalarMatrix(&d.matrixC, x, y, z))
		d.outCount = 2
	case 0x08:
		// snes9x dsp1.cpp DSP1_Op08 (lines 1034-1044) + dispatch at
		// 1312-1322. 3 input words (X, Y, Z) → 2 output words
		// (Ll = low 16 bits, Lh = high 16 bits) of the 32-bit value
		// (X² + Y² + Z²) << 1. Snes9x uses int32; the shift can
		// overflow for large signed inputs (e.g. all 0x7FFF) and the
		// 2's-complement wraparound is part of the contract — Go's
		// int32 has the same semantics so a literal port suffices.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		size := (int32(x)*int32(x) + int32(y)*int32(y) + int32(z)*int32(z)) << 1
		writeWordLE(d.output[0:], int16(size&0xffff))
		writeWordLE(d.output[2:], int16((size>>16)&0xffff))
		d.outCount = 4
	case 0x18, 0x38:
		// snes9x dsp1.cpp DSP1_Op18 (lines 1046-1054) + DSP1_Op38
		// (1055-1063, byte-identical body) + dispatch at 1324-1348.
		// 4 input words (X, Y, Z, R) → 1 output word:
		//   D = (X² + Y² + Z² − R²) >> 15
		// Sign of D classifies a point relative to a sphere of radius
		// R (negative = inside, positive = outside, zero = boundary).
		// Snes9x carries separate Op18/Op38 state structs but the math
		// is byte-identical; in Go we share the dispatch since we
		// don't model per-op state.
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		r := readWordLE(d.parameters[6:])
		// int32 multiply with snes9x's 2's-complement wraparound
		// semantic. The signed >>15 final shift propagates the sign.
		diff := int32(x)*int32(x) + int32(y)*int32(y) + int32(z)*int32(z) - int32(r)*int32(r)
		writeWordLE(d.output[0:], int16(diff>>15))
		d.outCount = 2
	case 0x28:
		// snes9x dsp1.cpp DSP1_Op28 (lines 1065-1092) + dispatch at
		// 1350-1359. 3 input words (X, Y, Z) → 1 output word
		// (R = sqrt(X² + Y² + Z²)). Sqrt approximation pipeline:
		//   Radius = X² + Y² + Z²              // int32 sum
		//   if Radius == 0: R = 0
		//   else:
		//     normalizeDouble(Radius) → (C, E)
		//     if E & 1: C = C * 0x4000 >> 15   // half-shift correction
		//     Pos = C * 0x0040 >> 15           // 0..63 table index
		//     Node1 = dsp1ROM[0xD5 + Pos]
		//     Node2 = dsp1ROM[0xD6 + Pos]
		//     R = ((Node2 - Node1) * (C & 0x1FF) >> 9) + Node1
		//     R >>= E >> 1                     // restore exponent
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		radius := int32(x)*int32(x) + int32(y)*int32(y) + int32(z)*int32(z)
		var r int16
		if radius != 0 {
			c, e := normalizeDouble(radius)
			if e&1 != 0 {
				c = int16(int32(c) * 0x4000 >> 15)
			}
			pos := int16(int32(c) * 0x0040 >> 15)
			node1 := int16(dsp1ROM[0xD5+pos])
			node2 := int16(dsp1ROM[0xD6+pos])
			r = int16((int32(node2-node1)*int32(c&0x1FF)>>9)+int32(node1)) >> (e >> 1)
		}
		writeWordLE(d.output[0:], r)
		d.outCount = 2
	case 0x14, 0x34:
		// snes9x dsp1.cpp DSP1_Op14 (lines 940-970) + dispatch at
		// 1610-1625. 6 input words (Zr, Xr, Yr, U, F, L) → 3 output
		// words (Zrr, Xrr, Yrr). Inverse 3D rotation: applies the
		// inverse of the rotation defined by Euler angles (Xr, Yr, Zr)
		// to the (U, F, L) input vector and returns the result added
		// to (Zr, Xr, L). Aliases 0x14/0x34 share the handler.
		//
		// Note on degenerate inputs: when (U, F) = (0, 0) the
		// normalize/normalizeDouble pipeline cascades to an extreme-
		// negative e at the truncate() call sites. snes9x indexes
		// dsp1ROM[0x31+e] without bounds checking, reading undefined
		// C memory; Go's runtime would panic. The shipped truncate()
		// in dsp1math.go now short-circuits on c==0 (the only case
		// where this cascade arises), preserving non-degenerate math
		// while making zero-input Op14 return cleanly.
		zr := readWordLE(d.parameters[0:])
		xr := readWordLE(d.parameters[2:])
		yr := readWordLE(d.parameters[4:])
		u := readWordLE(d.parameters[6:])
		f := readWordLE(d.parameters[8:])
		l := readWordLE(d.parameters[10:])

		// CSec / ESec hold sec(Xr) = 1/cos(Xr) in normalized form.
		cSec, eSec := inverse(cosFP(xr), 0)

		// Rotation Around Z.
		c, e := normalizeDouble(int32(u)*int32(cosFP(yr)) - int32(f)*int32(sinFP(yr)))
		e = eSec - e
		c, e = normalize(int16(int32(c)*int32(cSec)>>15), e)
		zrr := zr + truncate(c, e)

		// Rotation Around X.
		xrr := xr + int16(int32(u)*int32(sinFP(yr))>>15) + int16(int32(f)*int32(cosFP(yr))>>15)

		// Rotation Around Y.
		c, e = normalizeDouble(int32(u)*int32(cosFP(yr)) + int32(f)*int32(sinFP(yr)))
		e = eSec - e
		var cSin int16
		cSin, e = normalize(sinFP(xr), e)
		cTan := int16(int32(cSec) * int32(cSin) >> 15)
		c, e = normalize(int16(-(int32(c)*int32(cTan)>>15)), e)
		yrr := yr + truncate(c, e) + l

		writeWordLE(d.output[0:], zrr)
		writeWordLE(d.output[2:], xrr)
		writeWordLE(d.output[4:], yrr)
		d.outCount = 6
	case 0x06, 0x16, 0x26, 0x36:
		// snes9x dsp1.cpp DSP1_Op06 / DSP1_Project. Reads 3 input words
		// (X,Y,Z), writes 3 output words (H,V,M). Uses Op02 projection state
		// (Gx/Gy/Gz, Nx/Ny/Nz, G_Les, C_Les, E_Les, SinAas/CosAas,
		// SinAzs/CosAzs).
		x := readWordLE(d.parameters[0:])
		y := readWordLE(d.parameters[2:])
		z := readWordLE(d.parameters[4:])
		h, v, m := d.project(x, y, z)
		writeWordLE(d.output[0:], h)
		writeWordLE(d.output[2:], v)
		writeWordLE(d.output[4:], m)
		d.outCount = 6
	case 0x00:
		// snes9x dsp1.cpp DSP1_Op00 (lines 278-285) + dispatch at 1268-1276:
		//   Op00Multiplicand = (int16) READ_WORD(&parameters[0])
		//   Op00Multiplier   = (int16) READ_WORD(&parameters[2])
		//   Op00Result = Op00Multiplicand * Op00Multiplier >> 15
		//   out_count = 2; output[0..1] = Op00Result.
		// Q15 fixed-point multiply: int16 × int16 → int32 → >>15 → int16.
		mul1 := readWordLE(d.parameters[0:])
		mul2 := readWordLE(d.parameters[2:])
		result := int16(int32(mul1) * int32(mul2) >> 15)
		writeWordLE(d.output[0:], result)
		d.outCount = 2
	case 0x20:
		// snes9x dsp1.cpp DSP1_Op20 (lines 287-295) + dispatch at 1278-1286:
		//   Op20Multiplicand = (int16) READ_WORD(&parameters[0])
		//   Op20Multiplier   = (int16) READ_WORD(&parameters[2])
		//   Op20Result = Op20Multiplicand * Op20Multiplier >> 15
		//   Op20Result++
		//   out_count = 2; output[0..1] = Op20Result.
		// Same Q15 multiply as Op00, with a post-increment of the int16 result.
		mul1 := readWordLE(d.parameters[0:])
		mul2 := readWordLE(d.parameters[2:])
		result := int16(int32(mul1)*int32(mul2)>>15) + 1
		writeWordLE(d.output[0:], result)
		d.outCount = 2
	case 0x10, 0x30:
		// snes9x dsp1.cpp DSP1_Op10 (lines 358-370) + dispatch at 1288-1298:
		//   Op10Coefficient = (int16) READ_WORD(&parameters[0])
		//   Op10Exponent    = (int16) READ_WORD(&parameters[2])
		//   DSP1_Inverse(coef, exp, &iCoef, &iExp)
		//   out_count = 4; output[0..1]=iCoef, output[2..3]=iExp.
		// Aliases 0x10 and 0x30 (snes9x case-fallthrough at 1288-1289).
		// The math helper inverse() is already shipped in dsp1math.go and
		// covered by TestInverse with 10 captured goldens.
		coef := readWordLE(d.parameters[0:])
		exp := readWordLE(d.parameters[2:])
		iCoef, iExp := inverse(coef, exp)
		writeWordLE(d.output[0:], iCoef)
		writeWordLE(d.output[2:], iExp)
		d.outCount = 4
	case 0x0c, 0x2c:
		// snes9x dsp1.cpp DSP1_Op0C (lines 552-556) + dispatch at 1361-1372:
		//   Op0CA  = (int16) READ_WORD(&parameters[0])  // angle
		//   Op0CX1 = (int16) READ_WORD(&parameters[2])
		//   Op0CY1 = (int16) READ_WORD(&parameters[4])
		//   Op0CX2 = (Op0CY1 * Sin(A) >> 15) + (Op0CX1 * Cos(A) >> 15)
		//   Op0CY2 = (Op0CY1 * Cos(A) >> 15) - (Op0CX1 * Sin(A) >> 15)
		//   out_count = 4; output[0..1]=X2, output[2..3]=Y2.
		// Aliases 0x0c and 0x2c share the handler (snes9x case-fallthrough).
		angle := readWordLE(d.parameters[0:])
		x1 := readWordLE(d.parameters[2:])
		y1 := readWordLE(d.parameters[4:])
		s := sinFP(angle)
		c := cosFP(angle)
		x2 := int16(int32(y1)*int32(s)>>15) + int16(int32(x1)*int32(c)>>15)
		y2 := int16(int32(y1)*int32(c)>>15) - int16(int32(x1)*int32(s)>>15)
		writeWordLE(d.output[0:], x2)
		writeWordLE(d.output[2:], y2)
		d.outCount = 4
	case 0x1c, 0x3c:
		// snes9x dsp1.cpp DSP1_Op1C (lines 1094-1117) + dispatch at 1374-1390:
		// 6 input words in this order — Z, Y, X angles + XBR, YBR, ZBR
		// vector (note: snes9x dispatch reads parameters[2]→Y and
		// parameters[4]→X; comment at line 1377 cites neviksti/John).
		// 3 output words — XAR, YAR, ZAR.
		// The body performs three sequential 2D rotations:
		//   1) Around Z: produces (X1,Y1); updates XBR=X1, YBR=Y1.
		//   2) Around Y: produces (Z1,X1); XAR=X1; ZBR=Z1.
		//   3) Around X: produces (Y1,Z1); YAR=Y1; ZAR=Z1.
		// All three stages use the standard 2D rotation formula
		// (matching Op0C's math vocabulary). Aliases 0x1c and 0x3c
		// share the handler. The BR registers are scratch state within
		// this single invocation only — the caller resupplies fresh
		// values via parameters every call.
		angZ := readWordLE(d.parameters[0:])
		angY := readWordLE(d.parameters[2:])
		angX := readWordLE(d.parameters[4:])
		xbr := readWordLE(d.parameters[6:])
		ybr := readWordLE(d.parameters[8:])
		zbr := readWordLE(d.parameters[10:])

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
		xar := x1
		zbr = z1

		// Rotate around X.
		sX := sinFP(angX)
		cX := cosFP(angX)
		y1 = int16(int32(zbr)*int32(sX)>>15) + int16(int32(ybr)*int32(cX)>>15)
		z1 = int16(int32(zbr)*int32(cX)>>15) - int16(int32(ybr)*int32(sX)>>15)
		yar := y1
		zar := z1

		writeWordLE(d.output[0:], xar)
		writeWordLE(d.output[2:], yar)
		writeWordLE(d.output[4:], zar)
		d.outCount = 6
	case 0x0e, 0x1e, 0x2e, 0x3e:
		// snes9x dsp1.cpp DSP1_Op0E (lines 1001-1004) wraps DSP1_Target
		// (lines 972-999) + dispatch at 1445-1448. 2 input words (H, V) →
		// 2 output words (X, Y). Inverse-projects screen coordinates back
		// to model space using state set by Op02 (Parameter / projection).
		// Aliases 0x0E, 0x1E, 0x2E, 0x3E share the handler.
		h := readWordLE(d.parameters[0:])
		v := readWordLE(d.parameters[2:])
		x, y := d.target(h, v)
		writeWordLE(d.output[0:], x)
		writeWordLE(d.output[2:], y)
		d.outCount = 4
	case 0x04, 0x24:
		// snes9x dsp1.cpp DSP1_Op04:
		//   Op04Angle  = (int16) READ_WORD(&parameters[0])
		//   Op04Radius = (uint16)READ_WORD(&parameters[2])
		//   Op04Sin = DSP1_Sin(angle) * radius >> 15
		//   Op04Cos = DSP1_Cos(angle) * radius >> 15
		//   out_count = 4; output[0..1]=Sin, output[2..3]=Cos.
		angle := int16(uint16(d.parameters[0]) | uint16(d.parameters[1])<<8)
		radius := uint16(d.parameters[2]) | uint16(d.parameters[3])<<8
		sin := int16(int32(sinFP(angle)) * int32(radius) >> 15)
		cos := int16(int32(cosFP(angle)) * int32(radius) >> 15)
		writeWordLE(d.output[0:], sin)
		writeWordLE(d.output[2:], cos)
		d.outCount = 4
	case 0x0f, 0x07, 0x2f, 0x27:
		// Identity / status. snes9x writes the version word; we leave it as
		// no-op until a downstream gate needs it.
		d.outCount = 0
	default:
		d.outCount = 0
	}
}

func (d *Device) Step(masterCycles uint64) {}

type state struct {
	MapType         MapType
	Command         uint8
	Waiting4command bool
	FirstParameter  bool
	Parameters      [16]uint8
	InIndex         uint8
	InCount         uint8
	Output          [32]uint8
	OutIndex        uint8
	OutCount        uint16

	SinAas, CosAas, SinAzs, CosAzs int16
	Nx, Ny, Nz                     int16
	CentreX, CentreY               int16
	Gx, Gy, Gz                     int16
	CLes, ELes, GLes               int16
	VPlaneC, VPlaneE               int16
	SinAZS, CosAZS                 int16
	SecAZS_C1, SecAZS_E1           int16
	SecAZS_C2, SecAZS_E2           int16
	VOffset                        int16
	Op0AVS                         int16
	MatrixA                        [3][3]int16
	MatrixB                        [3][3]int16
	MatrixC                        [3][3]int16
}

func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{
		MapType:         d.MapType,
		Command:         d.command,
		Waiting4command: d.waiting4command,
		FirstParameter:  d.firstParameter,
		Parameters:      d.parameters,
		InIndex:         d.inIndex,
		InCount:         d.inCount,
		Output:          d.output,
		OutIndex:        d.outIndex,
		OutCount:        d.outCount,

		SinAas: d.sinAas, CosAas: d.cosAas, SinAzs: d.sinAzs, CosAzs: d.cosAzs,
		Nx: d.nx, Ny: d.ny, Nz: d.nz,
		CentreX: d.centreX, CentreY: d.centreY,
		Gx: d.gx, Gy: d.gy, Gz: d.gz,
		CLes: d.cLes, ELes: d.eLes, GLes: d.gLes,
		VPlaneC: d.vplaneC, VPlaneE: d.vplaneE,
		SinAZS: d.sinAZS, CosAZS: d.cosAZS,
		SecAZS_C1: d.secAZS_C1, SecAZS_E1: d.secAZS_E1,
		SecAZS_C2: d.secAZS_C2, SecAZS_E2: d.secAZS_E2,
		VOffset: d.vOffset,
		Op0AVS:  d.op0AVS,
		MatrixA: d.matrixA,
		MatrixB: d.matrixB,
		MatrixC: d.matrixC,
	}); err != nil {
		return nil, fmt.Errorf("serialize dsp1: %w", err)
	}
	return buf.Bytes(), nil
}

func (d *Device) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize dsp1: %w", err)
	}
	d.MapType = s.MapType
	d.command = s.Command
	d.waiting4command = s.Waiting4command
	d.firstParameter = s.FirstParameter
	d.parameters = s.Parameters
	d.inIndex = s.InIndex
	d.inCount = s.InCount
	d.output = s.Output
	d.outIndex = s.OutIndex
	d.outCount = s.OutCount
	d.sinAas, d.cosAas, d.sinAzs, d.cosAzs = s.SinAas, s.CosAas, s.SinAzs, s.CosAzs
	d.nx, d.ny, d.nz = s.Nx, s.Ny, s.Nz
	d.centreX, d.centreY = s.CentreX, s.CentreY
	d.gx, d.gy, d.gz = s.Gx, s.Gy, s.Gz
	d.cLes, d.eLes, d.gLes = s.CLes, s.ELes, s.GLes
	d.vplaneC, d.vplaneE = s.VPlaneC, s.VPlaneE
	d.sinAZS, d.cosAZS = s.SinAZS, s.CosAZS
	d.secAZS_C1, d.secAZS_E1 = s.SecAZS_C1, s.SecAZS_E1
	d.secAZS_C2, d.secAZS_E2 = s.SecAZS_C2, s.SecAZS_E2
	d.vOffset = s.VOffset
	d.op0AVS = s.Op0AVS
	d.matrixA = s.MatrixA
	d.matrixB = s.MatrixB
	d.matrixC = s.MatrixC
	return nil
}

// readWordLE / writeWordLE mirror snes9x READ_WORD / WRITE_WORD on a
// little-endian host (port.h FAST_LSB_WORD_ACCESS path).
func readWordLE(b []uint8) int16 {
	return int16(uint16(b[0]) | uint16(b[1])<<8)
}

func writeWordLE(b []uint8, v int16) {
	u := uint16(v)
	b[0] = uint8(u & 0xff)
	b[1] = uint8(u >> 8)
}

// maxAZSExp matches the static const int16 MaxAZS_Exp[16] table in
// snes9x DSP1_Parameter.
var maxAZSExp = [16]int16{
	0x38b4, 0x38b7, 0x38ba, 0x38be, 0x38c0, 0x38c4, 0x38c7, 0x38ca,
	0x38ce, 0x38d0, 0x38d4, 0x38d7, 0x38da, 0x38dd, 0x38e0, 0x38e4,
}

// parameter ports snes9x DSP1_Parameter line-for-line. It populates the
// projection state used by Op 0x0A and Op 0x06 (sinAas/cosAas/sinAzs/
// cosAzs, nx/ny/nz, centreX/Y, gx/gy/gz, cLes/eLes/gLes, vplaneC/E,
// sinAZS/cosAZS, secAZS_C1/E1, secAZS_C2/E2, vOffset) and returns the
// 4-word output (vof, vva, cx, cy).
func (d *Device) parameter(fx, fy, fz, lfe, les, aas, azs int16) (vof, vva, cx, cy int16) {
	AZS := azs

	d.sinAas = sinFP(aas)
	d.cosAas = cosFP(aas)
	d.sinAzs = sinFP(azs)
	d.cosAzs = cosFP(azs)

	d.nx = int16(int32(d.sinAzs) * int32(-d.sinAas) >> 15)
	d.ny = int16(int32(d.sinAzs) * int32(d.cosAas) >> 15)
	d.nz = int16(int32(d.cosAzs) * 0x7fff >> 15)

	lfeNx := int16(int32(lfe) * int32(d.nx) >> 15)
	lfeNy := int16(int32(lfe) * int32(d.ny) >> 15)
	lfeNz := int16(int32(lfe) * int32(d.nz) >> 15)

	d.centreX = fx + lfeNx
	d.centreY = fy + lfeNy
	centreZ := fz + lfeNz

	lesNx := int16(int32(les) * int32(d.nx) >> 15)
	lesNy := int16(int32(les) * int32(d.ny) >> 15)
	lesNz := int16(int32(les) * int32(d.nz) >> 15)

	d.gx = d.centreX - lesNx
	d.gy = d.centreY - lesNy
	d.gz = centreZ - lesNz

	d.eLes = 0
	d.cLes, d.eLes = normalize(les, d.eLes)
	d.gLes = les

	var C, E int16
	C, E = normalize(centreZ, 0)

	d.vplaneC = C
	d.vplaneE = E

	maxAZS := maxAZSExp[-E]
	if AZS < 0 {
		maxAZS = -maxAZS
		if AZS < maxAZS+1 {
			AZS = maxAZS + 1
		}
	} else {
		if AZS > maxAZS {
			AZS = maxAZS
		}
	}

	d.sinAZS = sinFP(AZS)
	d.cosAZS = cosFP(AZS)

	d.secAZS_C1, d.secAZS_E1 = inverse(d.cosAZS, 0)
	C, E = normalize(int16(int32(C)*int32(d.secAZS_C1)>>15), E)
	E += d.secAZS_E1

	C = int16(int32(truncate(C, E)) * int32(d.sinAZS) >> 15)

	d.centreX += int16(int32(C) * int32(d.sinAas) >> 15)
	d.centreY -= int16(int32(C) * int32(d.cosAas) >> 15)

	cx = d.centreX
	cy = d.centreY

	vof = 0

	if azs != AZS || azs == maxAZS {
		if azs == -32768 {
			azs = -32767
		}
		C = azs - maxAZS
		if C >= 0 {
			C--
		}
		Aux := int16(^(int32(C) << 2))

		C = int16(int32(Aux) * int32(dsp1ROM[0x0328]) >> 15)
		C = int16(int32(C)*int32(Aux)>>15) + int16(dsp1ROM[0x0327])
		vof -= int16(int32(int16(int32(C)*int32(Aux)>>15)) * int32(les) >> 15)

		C = int16(int32(Aux) * int32(Aux) >> 15)
		Aux = int16(int32(C)*int32(dsp1ROM[0x0324])>>15) + int16(dsp1ROM[0x0325])
		d.cosAZS += int16(int32(int16(int32(C)*int32(Aux)>>15)) * int32(d.cosAZS) >> 15)
	}

	d.vOffset = int16(int32(les) * int32(d.cosAZS) >> 15)

	var CSec int16
	CSec, E = inverse(d.sinAZS, 0)
	C, E = normalize(d.vOffset, E)
	C, E = normalize(int16(int32(C)*int32(CSec)>>15), E)

	if C == -32768 {
		C >>= 1
		E++
	}

	vva = truncate(-C, E)

	d.secAZS_C2, d.secAZS_E2 = inverse(d.cosAZS, 0)
	return
}

// raster ports snes9x DSP1_Raster line-for-line. Reads projection state
// set by Op 0x02 (sinAzs, vOffset, vplaneE, vplaneC, secAZS_E2, secAZS_C2,
// cosAas, sinAas) plus the Vs scan-line argument; returns the four
// raster coefficients (An, Bn, Cn, Dn).
func (d *Device) raster(vs int16) (an, bn, cn, dn int16) {
	C, E := inverse(int16(int32(vs)*int32(d.sinAzs)>>15)+d.vOffset, 7)
	E += d.vplaneE

	C1 := int16(int32(C) * int32(d.vplaneC) >> 15)
	E1 := E + d.secAZS_E2

	C, E = normalize(C1, E)
	C = truncate(C, E)

	an = int16(int32(C) * int32(d.cosAas) >> 15)
	cn = int16(int32(C) * int32(d.sinAas) >> 15)

	C, E1 = normalize(int16(int32(C1)*int32(d.secAZS_C2)>>15), E1)
	C = truncate(C, E1)

	bn = int16(int32(C) * int32(-d.sinAas) >> 15)
	dn = int16(int32(C) * int32(d.cosAas) >> 15)
	return
}

// objectiveMatrix ports snes9x DSP1_Op0D/Op1D/Op2D line-for-line. The
// three functions are byte-identical apart from which matrix they read,
// so a single helper takes the matrix pointer. Computes
//   F = (X·m[0][0] + Y·m[0][1] + Z·m[0][2]) >>15 (per term)
//   L = (X·m[1][0] + Y·m[1][1] + Z·m[1][2])
//   U = (X·m[2][0] + Y·m[2][1] + Z·m[2][2])
// Each row's three products are >>15 truncated to int16 then summed.
func objectiveMatrix(mat *[3][3]int16, x, y, z int16) (f, l, u int16) {
	f = int16(int32(x)*int32(mat[0][0])>>15) +
		int16(int32(y)*int32(mat[0][1])>>15) +
		int16(int32(z)*int32(mat[0][2])>>15)
	l = int16(int32(x)*int32(mat[1][0])>>15) +
		int16(int32(y)*int32(mat[1][1])>>15) +
		int16(int32(z)*int32(mat[1][2])>>15)
	u = int16(int32(x)*int32(mat[2][0])>>15) +
		int16(int32(y)*int32(mat[2][1])>>15) +
		int16(int32(z)*int32(mat[2][2])>>15)
	return
}

// subjectiveMatrix ports snes9x DSP1_Op03/Op13/Op23 line-for-line. The
// three functions are byte-identical apart from which matrix they read,
// so a single helper takes the matrix pointer. Computes
//   X = (F·m[0][0] + L·m[1][0] + U·m[2][0]) >>15 (per term)
//   Y = (F·m[0][1] + L·m[1][1] + U·m[2][1])
//   Z = (F·m[0][2] + L·m[1][2] + U·m[2][2])
// This is the transpose of objectiveMatrix above — same products with
// transposed indexing — implementing M^T·v (world→object inverse) for
// the orthogonal rotation matrices that attitudeMatrix produces.
func subjectiveMatrix(mat *[3][3]int16, f, l, u int16) (x, y, z int16) {
	x = int16(int32(f)*int32(mat[0][0])>>15) +
		int16(int32(l)*int32(mat[1][0])>>15) +
		int16(int32(u)*int32(mat[2][0])>>15)
	y = int16(int32(f)*int32(mat[0][1])>>15) +
		int16(int32(l)*int32(mat[1][1])>>15) +
		int16(int32(u)*int32(mat[2][1])>>15)
	z = int16(int32(f)*int32(mat[0][2])>>15) +
		int16(int32(l)*int32(mat[1][2])>>15) +
		int16(int32(u)*int32(mat[2][2])>>15)
	return
}

// attitudeMatrix ports snes9x DSP1_Op01/Op11/Op21 line-for-line. The three
// functions in dsp1.cpp are byte-identical except for the destination
// (matrixA/B/C); a single helper takes the target. Builds a 3x3 rotation
// matrix from Euler Z/Y/X angles scaled by m. snes9x mutates DSP1.Op*m
// (>>=1) before computing — we operate on the local copy.
// scalarMatrix ports snes9x DSP1_Op0B/Op1B/Op2B line-for-line. The
// three functions are byte-identical apart from which matrix they
// read, so a single helper takes the matrix pointer. Computes the
// scalar product of (x,y,z) with row 0 of mat:
//   S = (x·m[0][0] + y·m[0][1] + z·m[0][2]) >> 15
// Each per-axis product is >>15 truncated to int16 then summed.
// Note: this matches the f-component of objectiveMatrix() at
// dsp1.go:769-781, but exposing it as a dedicated helper keeps
// the dispatch shape parallel with the other matrix-algebra ops.
func scalarMatrix(mat *[3][3]int16, x, y, z int16) int16 {
	return int16(int32(x)*int32(mat[0][0])>>15) +
		int16(int32(y)*int32(mat[0][1])>>15) +
		int16(int32(z)*int32(mat[0][2])>>15)
}

// target ports snes9x DSP1_Target (dsp1.cpp:972-999) line-for-line.
// Given screen coordinates (H, V) and the projection state set by
// Op02 (sinAzs, vOffset, vplaneE/C, secAZS_E1, centreX/Y, sinAas/
// cosAas), it returns model-space (X, Y) — the inverse of Op06's
// forward projection.
func (d *Device) target(h, v int16) (x, y int16) {
	// DSP1_Inverse((V * SinAzs >> 15) + VOffset, 8, &C, &E)
	c, e := inverse(int16(int32(v)*int32(d.sinAzs)>>15)+d.vOffset, 8)
	// E += VPlane_E
	e += d.vplaneE
	// C1 = C * VPlane_C >> 15
	c1 := int16(int32(c) * int32(d.vplaneC) >> 15)
	// E1 = E + SecAZS_E1
	e1 := e + d.secAZS_E1
	// H <<= 8
	hShift := int32(h) << 8
	// Normalize(C1, &C, &E)
	c, e = normalize(c1, e)
	// C = Truncate(C, E) * H >> 15
	c = int16(int32(truncate(c, e)) * hShift >> 15)
	// *X = CentreX + (C * CosAas >> 15)
	x = d.centreX + int16(int32(c)*int32(d.cosAas)>>15)
	// *Y = CentreY - (C * SinAas >> 15)
	y = d.centreY - int16(int32(c)*int32(d.sinAas)>>15)
	// V <<= 8
	vShift := int32(v) << 8
	// Normalize(C1 * SecAZS_C1 >> 15, &C, &E1)
	c, e1 = normalize(int16(int32(c1)*int32(d.secAZS_C1)>>15), e1)
	// C = Truncate(C, E1) * V >> 15
	c = int16(int32(truncate(c, e1)) * vShift >> 15)
	// *X += C * -SinAas >> 15
	x += int16(int32(c) * int32(-d.sinAas) >> 15)
	// *Y += C * CosAas >> 15
	y += int16(int32(c) * int32(d.cosAas) >> 15)
	return
}

func (d *Device) attitudeMatrix(mat *[3][3]int16, m, zr, yr, xr int16) {
	sinAz := sinFP(zr)
	cosAz := cosFP(zr)
	sinAy := sinFP(yr)
	cosAy := cosFP(yr)
	sinAx := sinFP(xr)
	cosAx := cosFP(xr)

	m >>= 1

	mat[0][0] = int16(int32(int16(int32(m)*int32(cosAz)>>15)) * int32(cosAy) >> 15)
	mat[0][1] = -int16(int32(int16(int32(m)*int32(sinAz)>>15)) * int32(cosAy) >> 15)
	mat[0][2] = int16(int32(m) * int32(sinAy) >> 15)

	mat[1][0] = int16(int32(int16(int32(m)*int32(sinAz)>>15))*int32(cosAx)>>15) +
		int16(int32(int16(int32(int16(int32(m)*int32(cosAz)>>15))*int32(sinAx)>>15))*int32(sinAy)>>15)
	mat[1][1] = int16(int32(int16(int32(m)*int32(cosAz)>>15))*int32(cosAx)>>15) -
		int16(int32(int16(int32(int16(int32(m)*int32(sinAz)>>15))*int32(sinAx)>>15))*int32(sinAy)>>15)
	mat[1][2] = -int16(int32(int16(int32(m)*int32(sinAx)>>15)) * int32(cosAy) >> 15)

	mat[2][0] = int16(int32(int16(int32(m)*int32(sinAz)>>15))*int32(sinAx)>>15) -
		int16(int32(int16(int32(int16(int32(m)*int32(cosAz)>>15))*int32(cosAx)>>15))*int32(sinAy)>>15)
	mat[2][1] = int16(int32(int16(int32(m)*int32(cosAz)>>15))*int32(sinAx)>>15) +
		int16(int32(int16(int32(int16(int32(m)*int32(sinAz)>>15))*int32(cosAx)>>15))*int32(sinAy)>>15)
	mat[2][2] = int16(int32(int16(int32(m)*int32(cosAx)>>15)) * int32(cosAy) >> 15)
}

// project ports snes9x DSP1_Project line-for-line. Inputs (X,Y,Z) are an
// object-space point; outputs (H, V, M) are screen offset and a depth-like
// scale. Uses projection state populated by Op 0x02 (Gx/Gy/Gz, Nx/Ny/Nz,
// G_Les, C_Les, E_Les, SinAas/CosAas, SinAzs/CosAzs).
func (d *Device) project(x, y, z int16) (h, v, m int16) {
	var E, E2, E3, E4, refE, E6, E7 int16
	var Px, Py, Pz int16

	Px, E4 = normalizeDouble(int32(x) - int32(d.gx))
	Py, E = normalizeDouble(int32(y) - int32(d.gy))
	Pz, E3 = normalizeDouble(int32(z) - int32(d.gz))
	Px >>= 1
	E4--
	Py >>= 1
	E--
	Pz >>= 1
	E3--

	refE = E
	if E3 < refE {
		refE = E3
	}
	if E4 < refE {
		refE = E4
	}

	Px = shiftR(Px, E4-refE)
	Py = shiftR(Py, E-refE)
	Pz = shiftR(Pz, E3-refE)

	C11 := -int16(int32(Px) * int32(d.nx) >> 15)
	C8 := -int16(int32(Py) * int32(d.ny) >> 15)
	C9 := -int16(int32(Pz) * int32(d.nz) >> 15)
	C12 := C11 + C8 + C9

	aux4 := int32(C12)
	refE = 16 - refE
	if refE >= 0 {
		aux4 <<= uint(refE)
	} else {
		aux4 >>= uint(-refE)
	}
	if aux4 == -1 {
		aux4 = 0
	}
	aux4 >>= 1

	aux := int32(uint16(d.gLes)) + aux4
	var C10 int16
	C10, E2 = normalizeDouble(aux)
	E2 = 15 - E2

	var C4 int16
	C4, E4 = inverse(C10, 0)
	C2 := int16(int32(C4) * int32(d.cLes) >> 15)

	E7 = 0
	C16 := int16(int32(Px) * int32(int16(int32(d.cosAas)*0x7fff>>15)) >> 15)
	C20 := int16(int32(Py) * int32(int16(int32(d.sinAas)*0x7fff>>15)) >> 15)
	C17 := C16 + C20

	C18 := int16(int32(C17) * int32(C2) >> 15)
	var C19 int16
	C19, E7 = normalize(C18, E7)
	h = truncate(C19, d.eLes-E2+refE+E7)

	E6 = 0
	C21 := int16(int32(Px) * int32(int16(int32(d.cosAzs)*int32(-d.sinAas)>>15)) >> 15)
	C22 := int16(int32(Py) * int32(int16(int32(d.cosAzs)*int32(d.cosAas)>>15)) >> 15)
	C23 := int16(int32(Pz) * int32(int16(int32(-d.sinAzs)*0x7fff>>15)) >> 15)
	C24 := C21 + C22 + C23

	C26 := int16(int32(C24) * int32(C2) >> 15)
	var C25 int16
	C25, E6 = normalize(C26, E6)
	v = truncate(C25, d.eLes-E2+refE+E6)

	var C6 int16
	C6, E4 = normalize(C2, E4)
	m = truncate(C6, E4+d.eLes-E2-7)
	return
}

// executeOp0A wraps snes9x DSP1_Op0A: run raster, write 4 little-endian
// output words at offset 0, then advance op0AVS for the next call.
func (d *Device) executeOp0A() {
	an, bn, cn, dn := d.raster(d.op0AVS)
	writeWordLE(d.output[0:], an)
	writeWordLE(d.output[2:], bn)
	writeWordLE(d.output[4:], cn)
	writeWordLE(d.output[6:], dn)
	d.op0AVS++
}
