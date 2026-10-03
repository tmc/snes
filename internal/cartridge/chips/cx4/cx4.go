// Package cx4 implements the HLE surface of Capcom's Cx4 coprocessor.
//
// The Cx4 is a Hitachi HG51BS169 math coprocessor. This package starts
// with the CPU-visible data RAM,
// register, DMA, and simple command surface used by snes9x's C4 HLE.
package cx4

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
)

const (
	ramSize = 0x2000
	c4PI    = 3.14159265
)

// Device is the Cx4 HLE coprocessor surface.
type Device struct {
	rom   []byte
	ram   [ramSize]byte
	trace func(CommandTrace)
}

type state struct {
	RAM [ramSize]byte
}

// CommandTrace is a snapshot of the CPU-visible command registers at the
// moment a Cx4 command is triggered.
type CommandTrace struct {
	Command    uint8
	Subcommand uint8
	F80        uint16
	F83        uint16
	F86        uint16
	F89        uint8
	F8C        uint8
	F8F        uint16
	F92        uint16
}

// New returns a fresh Cx4 device. The ROM slice is retained read-only for
// Cx4 DMA commands.
func New(rom []byte) *Device {
	return &Device{rom: rom}
}

// SetTrace installs a command trace hook. It is intended for diagnostics and
// tests; nil disables tracing.
func (d *Device) SetTrace(fn func(CommandTrace)) {
	d.trace = fn
}

// Read implements the cartridge Coprocessor interface.
func (d *Device) Read(addr uint32) (uint8, bool) {
	if idx, ok := dataRAMIndex(addr); ok {
		return d.ram[idx], true
	}
	if idx, ok := ioIndex(addr); ok {
		if idx == 0x1f5e {
			return 0, true
		}
		return d.ram[idx], true
	}
	return 0, false
}

// Write implements the cartridge Coprocessor interface.
func (d *Device) Write(addr uint32, val uint8) bool {
	if idx, ok := dataRAMIndex(addr); ok {
		d.ram[idx] = val
		return true
	}
	if idx, ok := ioIndex(addr); ok {
		d.ram[idx] = val
		switch idx {
		case 0x1f47:
			d.dma()
		case 0x1f4f:
			d.command(val)
		}
		return true
	}
	return false
}

// Step is present for the cartridge Coprocessor interface. The HLE command
// path executes synchronously on the triggering register write.
func (d *Device) Step(masterCycles uint64) {}

// Serialize captures Cx4 HLE state.
func (d *Device) Serialize() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state{RAM: d.ram}); err != nil {
		return nil, fmt.Errorf("serialize cx4: %w", err)
	}
	return buf.Bytes(), nil
}

// Unserialize restores Cx4 HLE state.
func (d *Device) Unserialize(data []byte) error {
	var s state
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return fmt.Errorf("unserialize cx4: %w", err)
	}
	d.ram = s.RAM
	return nil
}

func dataRAMIndex(addr uint32) (uint32, bool) {
	if !lowMirrorBank(addr) {
		return 0, false
	}
	off := addr & 0xffff
	if off >= 0x6000 && off <= 0x6bff {
		return off - 0x6000, true
	}
	if off >= 0x7000 && off <= 0x7bff {
		return off - 0x7000, true
	}
	return 0, false
}

func ioIndex(addr uint32) (uint32, bool) {
	if !lowMirrorBank(addr) {
		return 0, false
	}
	off := addr & 0xffff
	if off&0xec00 != 0x6c00 {
		return 0, false
	}
	return 0x1c00 | (off & 0x03ff), true
}

func lowMirrorBank(addr uint32) bool {
	bank := (addr >> 16) & 0xff
	return bank <= 0x3f || (bank >= 0x80 && bank <= 0xbf)
}

func (d *Device) dma() {
	source := uint32(d.ram[0x1f40]) | uint32(d.ram[0x1f41])<<8 | uint32(d.ram[0x1f42])<<16
	length := uint32(d.ram[0x1f43]) | uint32(d.ram[0x1f44])<<8
	target := uint32(d.ram[0x1f45]) | uint32(d.ram[0x1f46])<<8
	for i := uint32(0); i < length; i++ {
		d.ram[(target+i)&0x1fff] = d.romRead(source + i)
	}
}

func (d *Device) command(cmd uint8) {
	d.recordCommand(cmd)
	if d.ram[0x1f4d] == 0x0e && cmd < 0x40 && cmd&3 == 0 {
		d.ram[0x1f80] = cmd >> 2
		return
	}

	switch cmd {
	case 0x01:
		// Mirrors snes9x c4emu.cpp:798-805 and bsnes op01: clear the
		// output region, then draw the shared wireframe command.
		d.drawWireFrame(true)
	case 0x00:
		switch d.ram[0x1f4d] {
		case 0x00:
			// Mirrors snes9x c4emu.cpp:171-280 for the trace-proven
			// 0x00/0x00 OAM conversion path.
			d.buildOAM()
		case 0x03:
			// Mirrors snes9x c4emu.cpp:C4DoScaleRotate with row_padding=0.
			d.scaleRotate(0)
		case 0x05:
			// Mirrors snes9x c4emu.cpp:513-562 and bsnes op00_05.
			d.transformLines()
		case 0x07:
			// Mirrors snes9x c4emu.cpp:C4DoScaleRotate with row_padding=64.
			d.scaleRotate(64)
		case 0x08:
			// Mirrors the 0x00/0x08 C4ProcessSprites wireframe path.
			// Unlike command 0x01/0x08, snes9x/bsnes do not clear the
			// output buffer first.
			d.drawWireFrame(false)
		case 0x0b:
			d.disintegrate()
		case 0x0c:
			d.bitPlaneWave()
		}
	case 0x05:
		tmp := uint32(0x10000)
		if den := read16(d.ram[:], 0x1f83); den != 0 {
			tmp = (tmp / uint32(den)) * uint32(read16(d.ram[:], 0x1f81)) >> 8
		}
		write16(d.ram[:], 0x1f80, uint16(tmp))
	case 0x0d:
		x := int16(read16(d.ram[:], 0x1f80))
		y := int16(read16(d.ram[:], 0x1f83))
		dist := int16(read16(d.ram[:], 0x1f86))
		x, y = setVectorLength(x, y, dist)
		write16(d.ram[:], 0x1f89, uint16(x))
		write16(d.ram[:], 0x1f8c, uint16(y))
	case 0x10:
		// Mirrors snes9x c4emu.cpp:837-857.
		r := int32(read16(d.ram[:], 0x1f83))
		if r&0x8000 != 0 {
			r |= ^int32(0x7fff)
		} else {
			r &= 0x7fff
		}
		angle := int(read16(d.ram[:], 0x1f80) & 0x1ff)
		write24(d.ram[:], 0x1f86, uint32((r*int32(c4Cos(angle))*2)>>16))
		tmp := (r * int32(c4Sin(angle)) * 2) >> 16
		write24(d.ram[:], 0x1f89, uint32(tmp-(tmp>>6)))
	case 0x13:
		// Mirrors snes9x c4emu.cpp:859-872.
		r := int32(read16(d.ram[:], 0x1f83))
		angle := int(read16(d.ram[:], 0x1f80) & 0x1ff)
		write24(d.ram[:], 0x1f86, uint32((r*int32(c4Cos(angle))*2)>>8))
		write24(d.ram[:], 0x1f89, uint32((r*int32(c4Sin(angle))*2)>>8))
	case 0x15:
		// Mirrors snes9x c4emu.cpp:874-884 (optimized C4Op15,
		// equivalent to c4.cpp:132-136).
		x := int16(read16(d.ram[:], 0x1f80))
		y := int16(read16(d.ram[:], 0x1f83))
		dist := int16(math.Sqrt(float64(x)*float64(x) + float64(y)*float64(y)))
		write16(d.ram[:], 0x1f80, uint16(dist))
	case 0x1f:
		x := int16(read16(d.ram[:], 0x1f80))
		y := int16(read16(d.ram[:], 0x1f83))
		write16(d.ram[:], 0x1f86, atanAngle(x, y))
	case 0x22:
		// Mirrors snes9x c4emu.cpp:899-966.
		d.trapezoid()
	case 0x25:
		a := int32(read24(d.ram[:], 0x1f80))
		b := int32(read24(d.ram[:], 0x1f83))
		write24(d.ram[:], 0x1f80, uint32(a*b))
	case 0x2d:
		// Mirrors snes9x c4emu.cpp:982-1002.
		x := int16(read16(d.ram[:], 0x1f81))
		y := int16(read16(d.ram[:], 0x1f84))
		z := int16(read16(d.ram[:], 0x1f87))
		x, y = transformWireFrame2(x, y, z, d.ram[0x1f89], d.ram[0x1f8a], d.ram[0x1f8b], read16(d.ram[:], 0x1f90))
		write16(d.ram[:], 0x1f80, uint16(x))
		write16(d.ram[:], 0x1f83, uint16(y))
	case 0x40:
		var sum uint16
		for i := 0; i < 0x800; i++ {
			sum += uint16(d.ram[i])
		}
		write16(d.ram[:], 0x1f80, sum)
	case 0x54:
		a := int64(sign24(read24(d.ram[:], 0x1f80)))
		product := uint64(a * a)
		write24(d.ram[:], 0x1f83, uint32(product))
		write24(d.ram[:], 0x1f86, uint32(product>>24))
	case 0x5c:
		// Mirrors snes9x c4emu.cpp:1034-1042.
		copy(d.ram[:], c4TestPattern[:])
	case 0x89:
		d.ram[0x1f80] = 0x36
		d.ram[0x1f81] = 0x43
		d.ram[0x1f82] = 0x05
	}
}

func (d *Device) drawWireFrame(clearOutput bool) {
	if d.ram[0x1f4d] != 0x08 {
		return
	}

	if clearOutput {
		clear(d.ram[0x0300:0x0c00])
	}

	line := read24(d.ram[:], 0x1f80)
	for i := uint8(0); i < d.ram[0x0295]; i++ {
		point1 := d.wireFramePointAddr(line, true)
		point2 := d.wireFramePointAddr(line, false)
		d.drawLine(
			d.pointerRead16BE(point1),
			d.pointerRead16BE(point1+2),
			d.pointerRead16BE(point1+4),
			d.pointerRead16BE(point2),
			d.pointerRead16BE(point2+2),
			d.pointerRead16BE(point2+4),
			d.pointerRead(line+4),
		)
		line += 5
	}
}

func (d *Device) wireFramePointAddr(line uint32, first bool) uint32 {
	if first {
		if d.pointerRead(line) == 0xff && d.pointerRead(line+1) == 0xff {
			tmp := line - 5
			for tmp+2 < line && d.pointerRead(tmp+2) == 0xff && d.pointerRead(tmp+3) == 0xff {
				tmp -= 5
			}
			return d.wireFrameAddr(d.pointerRead(tmp+2), d.pointerRead(tmp+3))
		}
		return d.wireFrameAddr(d.pointerRead(line), d.pointerRead(line+1))
	}
	return d.wireFrameAddr(d.pointerRead(line+2), d.pointerRead(line+3))
}

func (d *Device) wireFrameAddr(hi, lo uint8) uint32 {
	return uint32(d.ram[0x1f82])<<16 | uint32(hi)<<8 | uint32(lo)
}

func (d *Device) drawLine(x1, y1, z1, x2, y2, z2 int16, color uint8) {
	tx1, ty1 := transformWireFrame2(x1, y1, z1, d.ram[0x1f86], d.ram[0x1f87], d.ram[0x1f88], uint16(d.ram[0x1f90]))
	tx2, ty2 := transformWireFrame2(x2, y2, z2, d.ram[0x1f86], d.ram[0x1f87], d.ram[0x1f88], uint16(d.ram[0x1f90]))

	fx := (int32(tx1) + 48) << 8
	fy := (int32(ty1) + 48) << 8
	dx, dy, dist := calcWireFrameStep(int16(tx1+48), int16(ty1+48), int16(tx2+48), int16(ty2+48))
	if dist == 0 {
		dist = 1
	}
	for i := int32(0); i < dist; i++ {
		if fx > 0xff && fy > 0xff && fx < 0x6000 && fy < 0x6000 {
			px := fx >> 8
			py := fy >> 8
			addr := uint32(((py >> 3) << 8) - ((py >> 3) << 6) + ((px >> 3) << 4) + (py&7)*2)
			bit := uint8(0x80 >> (px & 7))
			if addr+0x301 < ramSize {
				d.ram[addr+0x300] &^= bit
				d.ram[addr+0x301] &^= bit
				if color&1 != 0 {
					d.ram[addr+0x300] |= bit
				}
				if color&2 != 0 {
					d.ram[addr+0x301] |= bit
				}
			}
		}
		fx += int32(dx)
		fy += int32(dy)
	}
}

func (d *Device) transformLines() {
	rx := d.ram[0x1f83]
	ry := d.ram[0x1f86]
	rz := d.ram[0x1f89]
	scale := d.ram[0x1f8c]

	ptr := uint32(0)
	for i := read16(d.ram[:], 0x1f80); i > 0; i-- {
		x := int16(read16(d.ram[:], ptr+1))
		y := int16(read16(d.ram[:], ptr+5))
		z := int16(read16(d.ram[:], ptr+9))
		x, y = transformWireFrame(x, y, z, rx, ry, rz, scale)
		write16(d.ram[:], ptr+1, uint16(x+0x80))
		write16(d.ram[:], ptr+5, uint16(y+0x50))
		ptr += 0x10
	}

	write16(d.ram[:], 0x600, 23)
	write16(d.ram[:], 0x602, 0x60)
	write16(d.ram[:], 0x605, 0x40)
	write16(d.ram[:], 0x608, 23)
	write16(d.ram[:], 0x60a, 0x60)
	write16(d.ram[:], 0x60d, 0x40)

	ptr = 0x0b02
	out := uint32(0)
	for i := read16(d.ram[:], 0x0b00); i > 0; i-- {
		p1 := uint32(d.ram[ptr]) << 4
		p2 := uint32(d.ram[ptr+1]) << 4
		x1 := int16(read16(d.ram[:], p1+1))
		y1 := int16(read16(d.ram[:], p1+5))
		x2 := int16(read16(d.ram[:], p2+1))
		y2 := int16(read16(d.ram[:], p2+5))
		dx, dy, dist := calcWireFrameStep(x1, y1, x2, y2)
		if dist == 0 {
			dist = 1
		}
		write16(d.ram[:], out+0x600, uint16(dist))
		write16(d.ram[:], out+0x602, uint16(dx))
		write16(d.ram[:], out+0x605, uint16(dy))
		ptr += 2
		out += 8
	}
}

func transformWireFrame(x, y, z int16, rx, ry, rz, scale uint8) (int16, int16) {
	c4x := float64(x)
	c4y := float64(y)
	c4z := float64(z) - 0x95

	tanval := -float64(rx) * c4PI * 2 / 128
	c4y2 := c4y*math.Cos(tanval) - c4z*math.Sin(tanval)
	c4z2 := c4y*math.Sin(tanval) + c4z*math.Cos(tanval)

	tanval = -float64(ry) * c4PI * 2 / 128
	c4x2 := c4x*math.Cos(tanval) + c4z2*math.Sin(tanval)
	c4z = c4x*-math.Sin(tanval) + c4z2*math.Cos(tanval)

	tanval = -float64(rz) * c4PI * 2 / 128
	c4x = c4x2*math.Cos(tanval) - c4y2*math.Sin(tanval)
	c4y = c4x2*math.Sin(tanval) + c4y2*math.Cos(tanval)

	factor := float64(scale) / (0x90 * (c4z + 0x95)) * 0x95
	return int16(c4x * factor), int16(c4y * factor)
}

func calcWireFrameStep(x1, y1, x2, y2 int16) (dx, dy int16, dist int32) {
	dx = x2 - x1
	dy = y2 - y1
	adx := abs16(dx)
	ady := abs16(dy)
	switch {
	case adx > ady:
		dist = int32(adx) + 1
		dy = int16((256 * int32(dy)) / int32(adx))
		if dx < 0 {
			dx = -256
		} else {
			dx = 256
		}
	case dy != 0:
		dist = int32(ady) + 1
		dx = int16((256 * int32(dx)) / int32(ady))
		if dy < 0 {
			dy = -256
		} else {
			dy = 256
		}
	default:
		dist = 0
	}
	return dx, dy, dist
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func (d *Device) recordCommand(cmd uint8) {
	if d.trace == nil {
		return
	}
	d.trace(CommandTrace{
		Command:    cmd,
		Subcommand: d.ram[0x1f4d],
		F80:        read16(d.ram[:], 0x1f80),
		F83:        read16(d.ram[:], 0x1f83),
		F86:        read16(d.ram[:], 0x1f86),
		F89:        d.ram[0x1f89],
		F8C:        d.ram[0x1f8c],
		F8F:        read16(d.ram[:], 0x1f8f),
		F92:        read16(d.ram[:], 0x1f92),
	})
}

func (d *Device) romRead(addr uint32) uint8 {
	if len(d.rom) == 0 {
		return 0
	}
	pc := mirrorAddress(c4ROMAddress(addr), len(d.rom))
	return d.rom[pc]
}

func (d *Device) pointerRead(addr uint32) uint8 {
	if addr < 0x800000 {
		return d.romRead(addr)
	}
	off := addr & 0xffff
	if off >= 0x7f40 && off <= 0x7f5e {
		return 0
	}
	return d.ram[(off-0x6000)&0x1fff]
}

func (d *Device) pointerRead16BE(addr uint32) int16 {
	return int16(uint16(d.pointerRead(addr))<<8 | uint16(d.pointerRead(addr+1)))
}

func c4ROMAddress(addr uint32) int {
	return int(((addr & 0xff0000) >> 1) + (addr & 0x7fff))
}

func mirrorAddress(addr, size int) int {
	if size <= 0 {
		return 0
	}
	base := 0
	mask := 1 << 23
	for addr >= size {
		for addr&mask == 0 {
			mask >>= 1
		}
		addr -= mask
		if size > mask {
			size -= mask
			base += mask
		}
		mask >>= 1
	}
	return base + addr
}

func c4Sin(angle int) int16 { return c4SinTable[angle&0x1ff] }
func c4Cos(angle int) int16 { return c4SinTable[(angle+128)&0x1ff] }

func (d *Device) scaleRotate(rowPadding int) {
	xscale := int32(read16(d.ram[:], 0x1f8f))
	if xscale&0x8000 != 0 {
		xscale = 0x7fff
	}
	yscale := int32(read16(d.ram[:], 0x1f92))
	if yscale&0x8000 != 0 {
		yscale = 0x7fff
	}
	var a, b, c, deltaY int32
	switch angle := read16(d.ram[:], 0x1f80); angle {
	case 0:
		a = int32(int16(xscale))
		deltaY = int32(int16(yscale))
	case 128:
		b = int32(int16(-yscale))
		c = int32(int16(xscale))
	case 256:
		a = int32(int16(-xscale))
		deltaY = int32(int16(-yscale))
	case 384:
		b = int32(int16(yscale))
		c = int32(int16(-xscale))
	default:
		theta := int(angle & 0x1ff)
		a = int32(int16((int32(c4Cos(theta)) * xscale) >> 15))
		b = int32(int16(-((int32(c4Sin(theta)) * yscale) >> 15)))
		c = int32(int16((int32(c4Sin(theta)) * xscale) >> 15))
		deltaY = int32(int16((int32(c4Cos(theta)) * yscale) >> 15))
	}
	w := d.ram[0x1f89] &^ 7
	h := d.ram[0x1f8c] &^ 7
	clearLen := (int(w) + rowPadding/4) * int(h) / 2
	if clearLen > len(d.ram) {
		return
	}
	clear(d.ram[:clearLen])

	cx := int32(int16(read16(d.ram[:], 0x1f83)))
	cy := int32(int16(read16(d.ram[:], 0x1f86)))
	lineX := (cx << 12) - cx*a - cx*b
	lineY := (cy << 12) - cy*c - cy*deltaY
	out := 0
	bit := uint8(0x80)
	for y := uint8(0); y < h; y++ {
		xpos := uint32(lineX)
		ypos := uint32(lineY)
		for x := uint8(0); x < w; x++ {
			var b uint8
			if (xpos>>12) < uint32(w) && (ypos>>12) < uint32(h) {
				addr := (ypos>>12)*uint32(w) + (xpos >> 12)
				b = d.ram[0x600+(addr>>1)]
				if addr&1 != 0 {
					b >>= 4
				}
			}
			if out+17 >= len(d.ram) {
				return
			}
			if b&1 != 0 {
				d.ram[out] |= bit
			}
			if b&2 != 0 {
				d.ram[out+1] |= bit
			}
			if b&4 != 0 {
				d.ram[out+16] |= bit
			}
			if b&8 != 0 {
				d.ram[out+17] |= bit
			}
			bit >>= 1
			if bit == 0 {
				bit = 0x80
				out += 32
			}
			xpos += uint32(a)
			ypos += uint32(c)
		}
		out += 2 + rowPadding
		if out&0x10 != 0 {
			out &^= 0x10
		} else {
			out -= int(w)*4 + rowPadding
		}
		lineX += b
		lineY += deltaY
	}
}

func (d *Device) disintegrate() {
	width := uint32(d.ram[0x1f89])
	height := uint32(d.ram[0x1f8c])
	cx := int32(int16(read16(d.ram[:], 0x1f80)))
	cy := int32(int16(read16(d.ram[:], 0x1f83)))
	scaleX := int32(int16(read16(d.ram[:], 0x1f86)))
	scaleY := int32(int16(read16(d.ram[:], 0x1f8f)))
	startX := uint32(-cx*scaleX + (cx << 8))
	startY := uint32(-cy*scaleY + (cy << 8))
	clearLen := int(width * height / 2)
	if clearLen > len(d.ram) {
		return
	}
	clear(d.ram[:clearLen])

	src := 0x600
	for y, i := startY, uint32(0); i < height; i, y = i+1, y+uint32(scaleY) {
		for x, j := startX, uint32(0); j < width; j, x = j+1, x+uint32(scaleX) {
			if (x>>8) < width && (y>>8) < height && (y>>8)*width+(x>>8) < 0x2000 {
				if src >= len(d.ram) {
					return
				}
				pixel := d.ram[src]
				if j&1 != 0 {
					pixel >>= 4
				}
				idx := int((y>>11)*width*4 + (x>>11)*32 + ((y>>8)&7)*2)
				mask := uint8(0x80 >> ((x >> 8) & 7))
				if idx+17 >= len(d.ram) {
					return
				}
				if pixel&1 != 0 {
					d.ram[idx] |= mask
				}
				if pixel&2 != 0 {
					d.ram[idx+1] |= mask
				}
				if pixel&4 != 0 {
					d.ram[idx+16] |= mask
				}
				if pixel&8 != 0 {
					d.ram[idx+17] |= mask
				}
			}
			if j&1 != 0 {
				src++
			}
		}
	}
}

var c4WaveBMPData = [...]uint32{
	0x0000, 0x0002, 0x0004, 0x0006, 0x0008, 0x000a, 0x000c, 0x000e,
	0x0200, 0x0202, 0x0204, 0x0206, 0x0208, 0x020a, 0x020c, 0x020e,
	0x0400, 0x0402, 0x0404, 0x0406, 0x0408, 0x040a, 0x040c, 0x040e,
	0x0600, 0x0602, 0x0604, 0x0606, 0x0608, 0x060a, 0x060c, 0x060e,
	0x0800, 0x0802, 0x0804, 0x0806, 0x0808, 0x080a, 0x080c, 0x080e,
}

func (d *Device) bitPlaneWave() {
	dst := uint32(0)
	waveptr := uint32(d.ram[0x1f83])
	mask1 := uint16(0xc0c0)
	mask2 := uint16(0x3f3f)

	for j := 0; j < 0x10; j++ {
		for {
			height := -int16(int8(d.ram[waveptr+0x0b00])) - 16
			for i := 0; i < len(c4WaveBMPData); i++ {
				off := dst + c4WaveBMPData[i]
				tmp := read16(d.ram[:], off) & mask2
				if height >= 0 {
					if height < 8 {
						tmp |= mask1 & read16(d.ram[:], 0x0a00+uint32(height)*2)
					} else {
						tmp |= mask1 & 0xff00
					}
				}
				write16(d.ram[:], off, tmp)
				height++
			}
			waveptr = (waveptr + 1) & 0x7f
			mask1 = (mask1 >> 2) | (mask1 << 6)
			mask2 = (mask2 >> 2) | (mask2 << 6)
			if mask1 == 0xc0c0 {
				break
			}
		}
		dst += 16

		for {
			height := -int16(int8(d.ram[waveptr+0x0b00])) - 16
			for i := 0; i < len(c4WaveBMPData); i++ {
				off := dst + c4WaveBMPData[i]
				tmp := read16(d.ram[:], off) & mask2
				if height >= 0 {
					if height < 8 {
						tmp |= mask1 & read16(d.ram[:], 0x0a10+uint32(height)*2)
					} else {
						tmp |= mask1 & 0xff00
					}
				}
				write16(d.ram[:], off, tmp)
				height++
			}
			waveptr = (waveptr + 1) & 0x7f
			mask1 = (mask1 >> 2) | (mask1 << 6)
			mask2 = (mask2 >> 2) | (mask2 << 6)
			if mask1 == 0xc0c0 {
				break
			}
		}
		dst += 16
	}
}

// buildOAM mirrors snes9x c4emu.cpp:C4ConvOAM (lines 171-280). C4GetMemPointer
// source address handling follows snes9x c4.h:35-42 and c4.cpp:169-175.
func (d *Device) buildOAM() {
	oamPtr := int(d.ram[0x626]) << 2
	for i := 0x1fd; i > oamPtr; i -= 4 {
		d.ram[i] = 0xe0
	}

	globalX := read16(d.ram[:], 0x621)
	globalY := read16(d.ram[:], 0x623)
	oamPtr2 := 0x200 + int(d.ram[0x626]>>2)
	sprCount := int(uint8(128 - d.ram[0x626]))
	offset := (d.ram[0x626] & 3) * 2
	src := 0x220
	for n := int(d.ram[0x620]); n > 0 && sprCount > 0; n-- {
		sprX := int16(read16(d.ram[:], uint32(src)) - globalX)
		sprY := int16(read16(d.ram[:], uint32(src+2)) - globalY)
		sprAttr := d.ram[src+4] | d.ram[src+6]
		sprName := d.ram[src+5]
		sprPtr := read24(d.ram[:], uint32(src+7))

		if d.pointerRead(sprPtr) != 0 {
			tiles := int(d.pointerRead(sprPtr))
			sprPtr++
			for ; tiles > 0 && sprCount > 0; tiles-- {
				flags := d.pointerRead(sprPtr)
				x := int16(int8(d.pointerRead(sprPtr + 1)))
				if sprAttr&0x40 != 0 {
					size := int16(8)
					if flags&0x20 != 0 {
						size = 16
					}
					x = -x - size
				}
				x += sprX
				if x >= -16 && x <= 272 {
					y := int16(int8(d.pointerRead(sprPtr + 2)))
					if sprAttr&0x80 != 0 {
						size := int16(8)
						if flags&0x20 != 0 {
							size = 16
						}
						y = -y - size
					}
					y += sprY
					if y >= -16 && y <= 224 {
						d.ram[oamPtr] = uint8(x)
						d.ram[oamPtr+1] = uint8(y)
						d.ram[oamPtr+2] = sprName + d.pointerRead(sprPtr+3)
						d.ram[oamPtr+3] = sprAttr ^ (flags & 0xc0)
						d.ram[oamPtr2] &^= 3 << offset
						if uint16(x)&0x100 != 0 {
							d.ram[oamPtr2] |= 1 << offset
						}
						if flags&0x20 != 0 {
							d.ram[oamPtr2] |= 2 << offset
						}
						oamPtr += 4
						sprCount--
						offset = (offset + 2) & 6
						if offset == 0 {
							oamPtr2++
						}
					}
				}
				sprPtr += 4
			}
		} else if sprCount > 0 {
			d.ram[oamPtr] = uint8(sprX)
			d.ram[oamPtr+1] = uint8(sprY)
			d.ram[oamPtr+2] = sprName
			d.ram[oamPtr+3] = sprAttr
			d.ram[oamPtr2] &^= 3 << offset
			if uint16(sprX)&0x100 != 0 {
				d.ram[oamPtr2] |= 3 << offset
			} else {
				d.ram[oamPtr2] |= 2 << offset
			}
			oamPtr += 4
			sprCount--
			offset = (offset + 2) & 6
			if offset == 0 {
				oamPtr2++
			}
		}
		src += 16
	}
}

// trapezoid mirrors snes9x c4emu.cpp:899-966.
func (d *Device) trapezoid() {
	angle1 := int(read16(d.ram[:], 0x1f8c) & 0x1ff)
	angle2 := int(read16(d.ram[:], 0x1f8f) & 0x1ff)
	tan1 := int32(-0x80000000)
	if cos := c4Cos(angle1); cos != 0 {
		tan1 = (int32(c4Sin(angle1)) << 16) / int32(cos)
	}
	tan2 := int32(-0x80000000)
	if cos := c4Cos(angle2); cos != 0 {
		tan2 = (int32(c4Sin(angle2)) << 16) / int32(cos)
	}

	y := int16(read16(d.ram[:], 0x1f83) - read16(d.ram[:], 0x1f89))
	xoff := int16(read16(d.ram[:], 0x1f80))
	xbase := int16(read16(d.ram[:], 0x1f86))
	width := int16(read16(d.ram[:], 0x1f93))
	for j := 0; j < 225; j++ {
		left, right := int16(1), int16(0)
		if y >= 0 {
			left = int16((tan1*int32(y))>>16) - xoff + xbase
			right = int16((tan2*int32(y))>>16) - xoff + xbase + width
			if left < 0 && right < 0 {
				left, right = 1, 0
			} else if left < 0 {
				left = 0
			} else if right < 0 {
				right = 0
			}
			if left > 255 && right > 255 {
				left, right = 255, 254
			} else if left > 255 {
				left = 255
			} else if right > 255 {
				right = 255
			}
		}
		d.ram[0x800+j] = uint8(left)
		d.ram[0x900+j] = uint8(right)
		y++
	}
}

// transformWireFrame2 mirrors snes9x c4.cpp:C4TransfWireFrame2 (lines 57-81).
func transformWireFrame2(x, y, z int16, rx, ry, rz uint8, scale uint16) (int16, int16) {
	c4x := float64(x)
	c4y := float64(y)
	c4z := float64(z)

	tanval := -float64(rx) * c4PI * 2 / 128
	c4y2 := c4y*math.Cos(tanval) - c4z*math.Sin(tanval)
	c4z2 := c4y*math.Sin(tanval) + c4z*math.Cos(tanval)

	tanval = -float64(ry) * c4PI * 2 / 128
	c4x2 := c4x*math.Cos(tanval) + c4z2*math.Sin(tanval)
	c4z = c4x*-math.Sin(tanval) + c4z2*math.Cos(tanval)

	tanval = -float64(rz) * c4PI * 2 / 128
	c4x = c4x2*math.Cos(tanval) - c4y2*math.Sin(tanval)
	c4y = c4x2*math.Sin(tanval) + c4y2*math.Cos(tanval)

	return int16(c4x * float64(scale) / 0x100), int16(c4y * float64(scale) / 0x100)
}

// setVectorLength mirrors snes9x c4.cpp:C4Op0D (lines 138-144).
func setVectorLength(x, y, dist int16) (int16, int16) {
	length := math.Sqrt(float64(y)*float64(y) + float64(x)*float64(x))
	scale := float64(dist) / length
	y = int16(float64(y) * scale * 0.99)
	x = int16(float64(x) * scale * 0.98)
	return x, y
}

// atanAngle mirrors snes9x c4.cpp:C4Op1F (lines 113-130).
func atanAngle(x, y int16) uint16 {
	if x == 0 {
		if y > 0 {
			return 0x80
		}
		return 0x180
	}
	angle := int16(math.Atan(float64(y)/float64(x)) / (c4PI * 2) * 512)
	if x < 0 {
		angle += 0x100
	}
	return uint16(angle) & 0x1ff
}

func read16(ram []byte, off uint32) uint16 {
	return uint16(ram[off]) | uint16(ram[off+1])<<8
}

func read24(ram []byte, off uint32) uint32 {
	return uint32(ram[off]) | uint32(ram[off+1])<<8 | uint32(ram[off+2])<<16
}

func write16(ram []byte, off uint32, v uint16) {
	ram[off] = uint8(v)
	ram[off+1] = uint8(v >> 8)
}

func write24(ram []byte, off uint32, v uint32) {
	ram[off] = uint8(v)
	ram[off+1] = uint8(v >> 8)
	ram[off+2] = uint8(v >> 16)
}

func sign24(v uint32) int32 {
	v &= 0x00ffffff
	if v&0x00800000 != 0 {
		return int32(v | 0xff000000)
	}
	return int32(v)
}

// Power clears volatile data RAM and registers, retaining ROM and the trace hook.
// See bsnes sfc/coprocessor/cx4/cx4.cpp, Cx4::power.
func (d *Device) Power() {
	clear(d.ram[:0x0c00])
	clear(d.ram[0x1f00:])
}
