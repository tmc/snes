package cx4

import "testing"

func TestBusWindows(t *testing.T) {
	d := New(nil)
	tests := []struct {
		name  string
		addr  uint32
		alias uint32
	}{
		{"low bank data", 0x006000, 0x007000},
		{"high mirror data", 0x806bff, 0x807bff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !d.Write(tt.addr, 0xa5) {
				t.Fatalf("Write(%#06x) not claimed", tt.addr)
			}
			if got, ok := d.Read(tt.addr); !ok || got != 0xa5 {
				t.Fatalf("Read(%#06x) = %#02x, %v; want %#02x, true", tt.addr, got, ok, uint8(0xa5))
			}
			if got, ok := d.Read(tt.alias); !ok || got != 0xa5 {
				t.Fatalf("alias Read(%#06x) = %#02x, %v; want %#02x, true", tt.alias, got, ok, uint8(0xa5))
			}
		})
	}

	if d.Write(0x006c00, 0x12) != true {
		t.Fatalf("I/O write not claimed")
	}
	if got, ok := d.Read(0x007c00); !ok || got != 0x12 {
		t.Fatalf("I/O mirror read = %#02x, %v; want %#02x, true", got, ok, uint8(0x12))
	}
	if got, ok := d.Read(0x007f5e); !ok || got != 0 {
		t.Fatalf("status read = %#02x, %v; want 0, true", got, ok)
	}
	if d.Write(0x406000, 0x34) {
		t.Fatalf("unexpected claim for bank $40 data window")
	}
	if _, ok := d.Read(0x008000); ok {
		t.Fatalf("unexpected claim for LoROM program window")
	}
}

func TestDMA(t *testing.T) {
	rom := make([]byte, 0x400000)
	copy(rom[0x001234:], []byte{0xc4, 0x40, 0x12, 0x34})
	copy(rom[0x201234:], []byte{0xde, 0xad, 0xbe, 0xef})
	d := New(rom)

	writeIO(t, d, 0x7f40, 0x34)
	writeIO(t, d, 0x7f41, 0x12)
	writeIO(t, d, 0x7f42, 0x40)
	writeIO(t, d, 0x7f43, 0x04)
	writeIO(t, d, 0x7f44, 0x00)
	writeIO(t, d, 0x7f45, 0x00)
	writeIO(t, d, 0x7f46, 0x60)
	writeIO(t, d, 0x7f47, 0x00)

	for i, want := range []uint8{0xde, 0xad, 0xbe, 0xef} {
		if got, _ := d.Read(0x006000 + uint32(i)); got != want {
			t.Fatalf("DMA byte %d = %#02x, want %#02x", i, got, want)
		}
	}
}

func TestSimpleCommands(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Device)
		cmd   uint8
		want  map[uint32]uint8
	}{
		{
			name: "propulsion divide",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f81, 0x0020)
				set16(d, 0x1f83, 0x0004)
			},
			cmd:  0x05,
			want: bytesAt(0x1f80, 0x00, 0x08),
		},
		{
			name: "set vector length",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f80, 30)
				set16(d, 0x1f83, 40)
				set16(d, 0x1f86, 100)
			},
			cmd:  0x0d,
			want: bytesAt(0x1f89, 58, 0, 0, 79, 0),
		},
		{
			name: "polar to rectangular low scale angle 0",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f80, 0)
				set16(d, 0x1f83, 0x0100)
			},
			cmd:  0x10,
			want: bytesAt(0x1f86, 0xff, 0x00, 0x00, 0, 0, 0),
		},
		{
			name: "polar to rectangular high scale angle 0",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f80, 0)
				set16(d, 0x1f83, 0x0100)
			},
			cmd:  0x13,
			want: bytesAt(0x1f86, 0xfe, 0xff, 0x00, 0, 0, 0),
		},
		{
			name: "pythagorean",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f80, 300)
				set16(d, 0x1f83, 400)
			},
			cmd:  0x15,
			want: bytesAt(0x1f80, 0xf4, 0x01),
		},
		{
			name: "atan",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f80, 1)
				set16(d, 0x1f83, 1)
			},
			cmd:  0x1f,
			want: bytesAt(0x1f86, 0x40, 0x00),
		},
		{
			name: "trapezoid flat edges",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f80, 0)
				set16(d, 0x1f83, 0)
				set16(d, 0x1f86, 0)
				set16(d, 0x1f89, 0)
				set16(d, 0x1f8c, 0)
				set16(d, 0x1f8f, 0)
				set16(d, 0x1f93, 10)
			},
			cmd: 0x22,
			want: map[uint32]uint8{
				0x0800: 0x00,
				0x0900: 0x0a,
				0x08e0: 0x00,
				0x09e0: 0x0a,
			},
		},
		{
			name: "multiply",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set24(d, 0x1f80, 0x000123)
				set24(d, 0x1f83, 0x000010)
			},
			cmd:  0x25,
			want: bytesAt(0x1f80, 0x30, 0x12, 0x00),
		},
		{
			name: "transform coords identity scale",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x02
				set16(d, 0x1f81, 10)
				set16(d, 0x1f84, 20)
				set16(d, 0x1f87, 0)
				d.ram[0x1f89] = 0
				d.ram[0x1f8a] = 0
				d.ram[0x1f8b] = 0
				set16(d, 0x1f90, 0x0100)
			},
			cmd:  0x2d,
			want: bytesAt(0x1f80, 0x0a, 0x00, 0, 0x14, 0x00),
		},
		{
			name: "sum",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x0e
				d.ram[0] = 1
				d.ram[1] = 2
				d.ram[0x7ff] = 3
			},
			cmd:  0x40,
			want: bytesAt(0x1f80, 0x06, 0x00),
		},
		{
			name: "square signed 24",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x0e
				set24(d, 0x1f80, 0xfffffe)
			},
			cmd:  0x54,
			want: bytesAt(0x1f83, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00),
		},
		{
			name: "immediate register pattern",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x0e
			},
			cmd:  0x5c,
			want: bytesAt(0x0000, c4TestPattern[:]...),
		},
		{
			name: "ROM test",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x0e
			},
			cmd:  0x89,
			want: bytesAt(0x1f80, 0x36, 0x43, 0x05),
		},
		{
			name: "test command path",
			setup: func(d *Device) {
				d.ram[0x1f4d] = 0x0e
			},
			cmd:  0x20,
			want: bytesAt(0x1f80, 0x08),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(nil)
			tt.setup(d)
			writeIO(t, d, 0x7f4f, tt.cmd)
			for off, want := range tt.want {
				if got := d.ram[off]; got != want {
					t.Fatalf("ram[%#04x] = %#02x, want %#02x", off, got, want)
				}
			}
		})
	}
}

func TestBuildOAMBaseSprite(t *testing.T) {
	d := New(nil)
	d.ram[0x1f4d] = 0x00
	d.ram[0x620] = 1
	set16(d, 0x220, 0x0012)
	set16(d, 0x222, 0x0034)
	d.ram[0x224] = 0x20
	d.ram[0x225] = 0x56
	d.ram[0x226] = 0x04
	set24(d, 0x227, 0x806000)

	writeIO(t, d, 0x7f4f, 0x00)

	want := bytesAt(0, 0x12, 0x34, 0x56, 0x24)
	want[0x200] = 0x02
	want[0x1fd] = 0xe0
	want[0x626] = 0x00
	for off, want := range want {
		if got := d.ram[off]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", off, got, want)
		}
	}
}

func TestBuildOAMMetaSprite(t *testing.T) {
	d := New(nil)
	d.ram[0x1f4d] = 0x00
	d.ram[0x620] = 1
	set16(d, 0x220, 0x0010)
	set16(d, 0x222, 0x0020)
	d.ram[0x225] = 0x40
	set24(d, 0x227, 0x806300)
	d.ram[0x300] = 1
	d.ram[0x301] = 0x20
	d.ram[0x302] = 0x02
	d.ram[0x303] = 0x03
	d.ram[0x304] = 0x04

	writeIO(t, d, 0x7f4f, 0x00)

	want := bytesAt(0, 0x12, 0x23, 0x44, 0x00)
	want[0x200] = 0x02
	want[0x626] = 0x00
	for off, want := range want {
		if got := d.ram[off]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", off, got, want)
		}
	}
}

func TestBuildOAMKeepsStartIndex(t *testing.T) {
	d := New(nil)
	d.ram[0x1f4d] = 0x00
	d.ram[0x620] = 2
	d.ram[0x626] = 1
	set16(d, 0x220, 0x0102)
	set16(d, 0x222, 0x0008)
	d.ram[0x225] = 0x20
	set24(d, 0x227, 0x806000)
	set16(d, 0x230, 0x0004)
	set16(d, 0x232, 0x0009)
	d.ram[0x235] = 0x30
	set24(d, 0x237, 0x806000)

	writeIO(t, d, 0x7f4f, 0x00)

	want := map[uint32]uint8{
		0x004: 0x02, 0x005: 0x08, 0x006: 0x20, 0x007: 0x00,
		0x008: 0x04, 0x009: 0x09, 0x00a: 0x30, 0x00b: 0x00,
		0x200: 0x2c,
		0x626: 0x01,
	}
	for off, want := range want {
		if got := d.ram[off]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", off, got, want)
		}
	}
}

func TestScaleRotateIdentity48x64(t *testing.T) {
	d := New(nil)
	d.ram[0x1f4d] = 0x03
	set16(d, 0x1f80, 0)
	set16(d, 0x1f83, 0x0018)
	set16(d, 0x1f86, 0x0020)
	d.ram[0x1f89] = 0x30
	d.ram[0x1f8c] = 0x40
	set16(d, 0x1f8f, 0x1000)
	set16(d, 0x1f92, 0x1000)
	for i := 0; i < 1536; i++ {
		lo := uint8(i & 15)
		hi := uint8((15 - i) & 15)
		d.ram[0x600+i] = lo | hi<<4
	}

	writeIO(t, d, 0x7f4f, 0x00)

	wantPrefix := []uint8{
		0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a,
		0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a,
		0x55, 0x55, 0x55, 0xaa, 0x55, 0x55, 0x55, 0xaa,
		0x55, 0x55, 0x55, 0xaa, 0x55, 0x55, 0x55, 0xaa,
		0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a,
		0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a, 0x66, 0x5a,
		0xaa, 0x55, 0xaa, 0xaa, 0xaa, 0x55, 0xaa, 0xaa,
		0xaa, 0x55, 0xaa, 0xaa, 0xaa, 0x55, 0xaa, 0xaa,
	}
	for i, want := range wantPrefix {
		if got := d.ram[i]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", i, got, want)
		}
	}
	if got := cx4GoldenFNV64(d.ram[:1536]); got != 0x05d78a8a08280b03 {
		t.Fatalf("output FNV64 = %#016x, want 0x05d78a8a08280b03", got)
	}
}

func TestScaleRotateMasksWidthHeight(t *testing.T) {
	d := New(nil)
	for i := 0; i < 0x40; i++ {
		d.ram[i] = 0xa5
	}
	d.ram[0x1f4d] = 0x03
	set16(d, 0x1f80, 0)
	set16(d, 0x1f83, 0x0004)
	set16(d, 0x1f86, 0x0004)
	d.ram[0x1f89] = 0x0f
	d.ram[0x1f8c] = 0x0f
	set16(d, 0x1f8f, 0x1000)
	set16(d, 0x1f92, 0x1000)
	for i := 0; i < 0x20; i += 4 {
		d.ram[0x600+i+0] = 0x10
		d.ram[0x600+i+1] = 0x32
		d.ram[0x600+i+2] = 0x54
		d.ram[0x600+i+3] = 0x76
	}

	writeIO(t, d, 0x7f4f, 0x00)

	want := []uint8{
		0x55, 0x33, 0x55, 0x33, 0x55, 0x33, 0x55, 0x33,
		0x55, 0x33, 0x55, 0x33, 0x55, 0x33, 0x55, 0x33,
		0x0f, 0x00, 0x0f, 0x00, 0x0f, 0x00, 0x0f, 0x00,
		0x0f, 0x00, 0x0f, 0x00, 0x0f, 0x00, 0x0f, 0x00,
		0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5,
		0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5,
		0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5,
		0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5,
	}
	for i, want := range want {
		if got := d.ram[i]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", i, got, want)
		}
	}
}

func TestDrawWireFrameZeroScaleX2Fixture(t *testing.T) {
	rom := make([]byte, 0x147000)
	line := uint32(0x28eeca)
	for i, rec := range [][]uint8{
		{0xef, 0x23, 0xef, 0x41, 0x03},
		{0xef, 0x5f, 0xef, 0x7d, 0x03},
		{0xff, 0xff, 0xef, 0x41, 0x03},
		{0xef, 0x23, 0xef, 0x5f, 0x03},
		{0xef, 0x17, 0xef, 0x35, 0x03},
		{0xef, 0x71, 0xef, 0x53, 0x03},
		{0xef, 0x29, 0xef, 0x47, 0x03},
		{0xef, 0x83, 0xef, 0x65, 0x03},
		{0xef, 0x89, 0xef, 0x0b, 0x03},
		{0xef, 0x1d, 0xef, 0x59, 0x03},
		{0xef, 0x4d, 0xef, 0x11, 0x03},
		{0xef, 0x6b, 0xef, 0x2f, 0x03},
		{0xef, 0x3b, 0xef, 0x77, 0x03},
	} {
		copy(rom[c4ROMAddress(line)+i*5:], rec)
	}
	d := New(rom)
	for i := 0x0300; i < 0x0c00; i++ {
		d.ram[i] = 0xa5
	}
	d.ram[0x0295] = 0x0d
	d.ram[0x1f4d] = 0x08
	set24(d, 0x1f80, line)
	d.ram[0x1f86] = 0
	d.ram[0x1f87] = 0
	d.ram[0x1f88] = 0
	d.ram[0x1f90] = 0

	writeIO(t, d, 0x7f4f, 0x01)

	for off := 0x0300; off < 0x0c00; off++ {
		want := uint8(0)
		if off == 0x07e0 || off == 0x07e1 {
			want = 0x80
		}
		if got := d.ram[off]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", off, got, want)
		}
	}
	if got := cx4FNV64a(d.ram[0x0300:0x0c00]); got != 0xee143e96a8ee9925 {
		t.Fatalf("output FNV64 = %#016x, want 0xee143e96a8ee9925", got)
	}
}

func TestDrawWireFrameZeroScaleCommand00DoesNotClear(t *testing.T) {
	rom := make([]byte, 0x147000)
	line := uint32(0x28e7da)
	copy(rom[c4ROMAddress(line):], []byte{0xef, 0x23, 0xef, 0x41, 0x03})
	d := New(rom)
	for i := 0x0300; i < 0x0c00; i++ {
		d.ram[i] = 0xa5
	}
	d.ram[0x0295] = 1
	d.ram[0x1f4d] = 0x08
	set24(d, 0x1f80, line)

	writeIO(t, d, 0x7f4f, 0x00)

	for off := 0x0300; off < 0x0c00; off++ {
		want := uint8(0xa5)
		if off == 0x07e0 || off == 0x07e1 {
			want = 0xa5 | 0x80
		}
		if got := d.ram[off]; got != want {
			t.Fatalf("ram[%#04x] = %#02x, want %#02x", off, got, want)
		}
	}
}

func TestDrawWireFrameZeroScaleRejectsNonzeroScale(t *testing.T) {
	d := New(make([]byte, 0x147000))
	for i := 0x0300; i < 0x0c00; i++ {
		d.ram[i] = 0xa5
	}
	d.ram[0x0295] = 1
	d.ram[0x1f4d] = 0x08
	set24(d, 0x1f80, 0x28eeca)
	d.ram[0x1f90] = 1

	writeIO(t, d, 0x7f4f, 0x01)

	for off := 0x0300; off < 0x0c00; off++ {
		if got := d.ram[off]; got != 0xa5 {
			t.Fatalf("ram[%#04x] = %#02x, want guard-preserved 0xa5", off, got)
		}
	}
}

func TestAtanAngleQuadrants(t *testing.T) {
	tests := []struct {
		name string
		x    int16
		y    int16
		want uint16
	}{
		{"vertical positive", 0, 1, 0x80},
		{"vertical negative", 0, -1, 0x180},
		{"quadrant one", 1, 1, 0x40},
		{"quadrant two", -1, 1, 0xc0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := atanAngle(tt.x, tt.y); got != tt.want {
				t.Fatalf("atanAngle(%d, %d) = %#03x, want %#03x", tt.x, tt.y, got, tt.want)
			}
		})
	}
}

func TestSerializeRoundTrip(t *testing.T) {
	d := New(nil)
	d.ram[0x0001] = 0x12
	d.ram[0x1f80] = 0x34
	data, err := d.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := New(nil)
	if err := restored.Unserialize(data); err != nil {
		t.Fatalf("Unserialize: %v", err)
	}
	if restored.ram[0x0001] != 0x12 || restored.ram[0x1f80] != 0x34 {
		t.Fatalf("state did not round trip: ram[1]=%#02x ram[1f80]=%#02x", restored.ram[1], restored.ram[0x1f80])
	}
}

func writeIO(t *testing.T, d *Device, addr uint32, val uint8) {
	t.Helper()
	if !d.Write(addr, val) {
		t.Fatalf("Write(%#06x, %#02x) not claimed", addr, val)
	}
}

func set16(d *Device, off uint32, v uint16) {
	d.ram[off] = uint8(v)
	d.ram[off+1] = uint8(v >> 8)
}

func set24(d *Device, off uint32, v uint32) {
	d.ram[off] = uint8(v)
	d.ram[off+1] = uint8(v >> 8)
	d.ram[off+2] = uint8(v >> 16)
}

func bytesAt(off uint32, vals ...uint8) map[uint32]uint8 {
	m := make(map[uint32]uint8)
	for i, v := range vals {
		m[off+uint32(i)] = v
	}
	return m
}

func cx4GoldenFNV64(p []byte) uint64 {
	var h uint64 = 1469598103934665603
	for _, b := range p {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

func cx4FNV64a(p []byte) uint64 {
	var h uint64 = 14695981039346656037
	for _, b := range p {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}
