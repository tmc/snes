// Package obc1 implements the SETA OBC-1 OAM-indirection coprocessor
// with 8 KiB of
// SRAM at $00-$3F:6000-7FFF (and $80-$BF mirrors). Writes/reads in
// the special $7FF0-$7FF6 register window are dispatched through a
// packed-OAM-format indirection driven by status registers held
// inside the SRAM at $1FF5/$1FF6. Outside the register window the
// access is a plain SRAM read/write.
//
// Game flow: write packed OAM data to OBC-1 SRAM through the
// register window; the chip transposes it into the unpacked
// 0x800-byte OAM block at $1800 or $1C00 within its SRAM; the game
// then DMAs the unpacked block to the PPU OAM via standard $2104.
// OBC-1 itself never touches PPU registers — the OAM hot path is
// untouched by this device.
//
// References:
//
//   - bsnes/sfc/coprocessor/obc1/obc1.cpp (port target, ~70 lines)
//   - snes9x obc1.cpp (cross-check, 105 lines)
//   - bsnes/heuristics/super-famicom.cpp:295 (detection: cartridge
//     type-hi == 0x2)
package obc1

// Device is the OBC-1 register surface. SRAM lives in the cartridge;
// the device borrows a slice via SetRAM at attach time so reads and
// writes hit the same backing store the rest of the cartridge uses
// for the SRAM address window.
type Device struct {
	ram []byte

	// status fields, derived from ram bytes 0x1FF5/0x1FF6 at power.
	// $1FF5 bit 0 selects baseptr (1=0x1800, 0=0x1C00); $1FF6 bits
	// 0..6 = address (0..127); $1FF6 bits 0..1 = shift (×2).
	baseptr uint16
	address uint8
	shift   uint8
}

// New returns a fresh OBC-1 device. SetRAM must be called before
// any Read/Write to bind the SRAM backing store.
func New() *Device {
	return &Device{baseptr: 0x1C00}
}

// SetRAM attaches the cartridge SRAM slice. The slice must be at
// least 8 KiB (0x2000); only the low 0x1FFF bytes are addressable.
func (d *Device) SetRAM(ram []byte) {
	d.ram = ram
	d.power()
}

// power latches the status fields from RAM contents, mirroring
// bsnes obc1.cpp:12-16. Called automatically when SetRAM installs
// a non-empty SRAM slice.
func (d *Device) power() {
	if len(d.ram) == 0 {
		return
	}
	if d.ramRead(0x1FF5)&1 != 0 {
		d.baseptr = 0x1800
	} else {
		d.baseptr = 0x1C00
	}
	d.address = d.ramRead(0x1FF6) & 0x7F
	d.shift = (d.ramRead(0x1FF6) & 0x3) << 1
}

// Read returns the OBC-1-mapped byte at addr (any 0..0x1FFF range
// or a banked address that masks to the SRAM window — the device
// only consults the low 13 bits). Mirrors bsnes obc1.cpp:18-30.
func (d *Device) Read(addr uint32) uint8 {
	a := addr & 0x1FFF
	switch a {
	case 0x1FF0:
		return d.ramRead(uint32(d.baseptr) + uint32(d.address)<<2 + 0)
	case 0x1FF1:
		return d.ramRead(uint32(d.baseptr) + uint32(d.address)<<2 + 1)
	case 0x1FF2:
		return d.ramRead(uint32(d.baseptr) + uint32(d.address)<<2 + 2)
	case 0x1FF3:
		return d.ramRead(uint32(d.baseptr) + uint32(d.address)<<2 + 3)
	case 0x1FF4:
		return d.ramRead(uint32(d.baseptr) + uint32(d.address)>>2 + 0x200)
	}
	return d.ramRead(a)
}

// Write applies an OBC-1-mapped byte at addr. Mirrors bsnes
// obc1.cpp:32-60.
func (d *Device) Write(addr uint32, val uint8) {
	a := addr & 0x1FFF
	switch a {
	case 0x1FF0:
		d.ramWrite(uint32(d.baseptr)+uint32(d.address)<<2+0, val)
		return
	case 0x1FF1:
		d.ramWrite(uint32(d.baseptr)+uint32(d.address)<<2+1, val)
		return
	case 0x1FF2:
		d.ramWrite(uint32(d.baseptr)+uint32(d.address)<<2+2, val)
		return
	case 0x1FF3:
		d.ramWrite(uint32(d.baseptr)+uint32(d.address)<<2+3, val)
		return
	case 0x1FF4:
		// Packed-bit update: replace 2 bits at `shift` position
		// inside the byte at baseptr + (address>>2) + 0x200.
		off := uint32(d.baseptr) + uint32(d.address)>>2 + 0x200
		t := d.ramRead(off)
		t = (t &^ (3 << d.shift)) | ((val & 3) << d.shift)
		d.ramWrite(off, t)
		return
	case 0x1FF5:
		if val&1 != 0 {
			d.baseptr = 0x1800
		} else {
			d.baseptr = 0x1C00
		}
		d.ramWrite(a, val)
		return
	case 0x1FF6:
		d.address = val & 0x7F
		d.shift = (val & 3) << 1
		d.ramWrite(a, val)
		return
	case 0x1FF7:
		d.ramWrite(a, val)
		return
	}
	d.ramWrite(a, val)
}

func (d *Device) ramRead(off uint32) uint8 {
	return d.ram[off&0x1FFF]
}

func (d *Device) ramWrite(off uint32, val uint8) {
	d.ram[off&0x1FFF] = val
}
