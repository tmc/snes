// snesdis is a linear disassembler for raw 65c816 or SPC700 byte streams.
//
// It reads bytes from a file path or stdin and walks them linearly, printing
// one instruction per line. The primary reason to use it instead of a hex
// dump is that 65c816 operand widths depend on the dynamic m (memory) and
// x (index) status bits; snesdis tracks SEP/REP in the stream and widens or
// narrows subsequent immediates accordingly.
//
// Usage:
//
//	snesdis [flags] [file]
//
// If no file is given, bytes are read from stdin.
//
// Flags:
//
//	--cpu=65816|spc700   target CPU (default 65816)
//	--base=0xADDR        address to assign to the first disassembled byte
//	--mx=HH              initial P flags nibble (bit1=m, bit0=x); both set by default
//	--emulation          start in emulation mode (65816 only)
//	--count=N            stop after N instructions
//	--offset=N           skip the first N bytes of input before disassembling
//
// snesdis does not parse ROM headers, apply memory maps, or follow branches.
// It is a raw-bytes tool by design.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/tmc/snes/internal/apu/spc700"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/disasm"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("snesdis", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		cpuFlag   = fs.String("cpu", "65816", "target CPU: 65816 or spc700")
		baseFlag  = fs.String("base", "0", "address of the first byte (hex with 0x prefix, or decimal)")
		mxFlag    = fs.String("mx", "3", "initial m/x flags as hex (bit1=m, bit0=x); default both 8-bit")
		emuFlag   = fs.Bool("emulation", false, "start in emulation mode (65816 only)")
		countFlag = fs.Int("count", 0, "stop after N instructions (0 = run to EOF)")
		offFlag   = fs.Int("offset", 0, "skip the first N input bytes before disassembling")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	base, err := parseUint(*baseFlag, 32)
	if err != nil {
		fmt.Fprintf(stderr, "snesdis: --base: %v\n", err)
		return 2
	}
	mx, err := parseUint(*mxFlag, 8)
	if err != nil {
		fmt.Fprintf(stderr, "snesdis: --mx: %v\n", err)
		return 2
	}

	// Read all input bytes.
	var src io.Reader = stdin
	if rest := fs.Args(); len(rest) > 0 {
		f, err := os.Open(rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "snesdis: %v\n", err)
			return 1
		}
		defer f.Close()
		src = f
	}
	data, err := io.ReadAll(src)
	if err != nil {
		fmt.Fprintf(stderr, "snesdis: read: %v\n", err)
		return 1
	}
	if *offFlag > 0 {
		if *offFlag >= len(data) {
			return 0
		}
		data = data[*offFlag:]
	}

	switch *cpuFlag {
	case "65816":
		return walk65816(data, uint32(base), uint8(mx), *emuFlag, *countFlag, stdout)
	case "spc700":
		return walkSPC700(data, uint16(base), *countFlag, stdout)
	default:
		fmt.Fprintf(stderr, "snesdis: unknown --cpu=%q (want 65816 or spc700)\n", *cpuFlag)
		return 2
	}
}

// parseUint accepts decimal, or hex with 0x/0X prefix.
func parseUint(s string, bitSize int) (uint64, error) {
	return strconv.ParseUint(s, 0, bitSize)
}

// sliceBus65816 adapts a byte slice to the PeekBus65816 interface, treating
// the slice as a window starting at base. Reads outside the slice return 0.
type sliceBus65816 struct {
	data []byte
	base uint32
}

func (b *sliceBus65816) Read(addr uint32) uint8 {
	off := int64(addr) - int64(b.base)
	if off < 0 || off >= int64(len(b.data)) {
		return 0
	}
	return b.data[off]
}

type sliceBusSPC struct {
	data []byte
	base uint16
}

func (b *sliceBusSPC) Read(addr uint16) uint8 {
	off := int(addr) - int(b.base)
	if off < 0 || off >= len(b.data) {
		return 0
	}
	return b.data[off]
}

func walk65816(data []byte, base uint32, mx uint8, emu bool, limit int, out io.Writer) int {
	c := &cpu.CPU{}
	c.PB = uint8((base >> 16) & 0xFF)
	c.PC = uint16(base & 0xFFFF)
	c.E = emu
	// m = bit 5 of P, x = bit 4 of P. Accept mx as a 2-bit nibble where
	// bit 1 means m, bit 0 means x (matches the user-visible convention).
	c.P = 0
	if mx&0x2 != 0 {
		c.P |= 0x20
	}
	if mx&0x1 != 0 {
		c.P |= 0x10
	}
	bus := &sliceBus65816{data: data, base: base}

	offset := uint32(0)
	n := 0
	for int(offset) < len(data) {
		// If the remaining bytes are too few for the instruction we're
		// about to decode, stop cleanly. InstructionSize65816 reads only
		// the opcode; we check actual size after computing it.
		size := disasm.InstructionSize65816(c, bus)
		if int(offset)+size > len(data) {
			fmt.Fprintf(out, "; truncated: %d byte(s) remain, need %d for opcode %02X\n",
				len(data)-int(offset), size, data[offset])
			break
		}

		opcode := data[offset]
		fmt.Fprintln(out, disasm.Disassemble65816(c, bus))

		// Apply SEP/REP effect so subsequent immediates decode correctly.
		switch opcode {
		case 0xE2: // SEP #imm -- set the bits listed in the operand.
			c.P |= data[offset+1]
			if c.E {
				c.P |= 0x30
			}
		case 0xC2: // REP #imm -- clear the bits listed in the operand.
			c.P &^= data[offset+1]
			if c.E {
				c.P |= 0x30
			}
		case 0xFB: // XCE -- exchange Carry with Emulation.
			carry := c.P & 0x01
			if c.E {
				c.P |= 0x01
			} else {
				c.P &^= 0x01
			}
			c.E = carry != 0
			if c.E {
				c.P |= 0x30
			}
		}

		offset += uint32(size)
		c.PC += uint16(size)
		n++
		if limit > 0 && n >= limit {
			break
		}
	}
	return 0
}

func walkSPC700(data []byte, base uint16, limit int, out io.Writer) int {
	s := &spc700.SPC700{}
	s.PC = base
	bus := &sliceBusSPC{data: data, base: base}

	offset := 0
	n := 0
	for offset < len(data) {
		size := disasm.InstructionSizeSPC700(s, bus)
		if offset+size > len(data) {
			fmt.Fprintf(out, "; truncated: %d byte(s) remain, need %d for opcode %02X\n",
				len(data)-offset, size, data[offset])
			break
		}
		fmt.Fprintln(out, disasm.DisassembleSPC700(s, bus))
		offset += size
		s.PC += uint16(size)
		n++
		if limit > 0 && n >= limit {
			break
		}
	}
	return 0
}
