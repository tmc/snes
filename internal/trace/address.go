package trace

import (
	"fmt"
	"strconv"
	"strings"
)

type Address struct {
	Space string
	Addr  uint32
}

func ParseAddress(s string) (Address, error) {
	space, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(s)), ":")
	if !ok {
		return Address{}, fmt.Errorf("address %q: missing address space", s)
	}
	if space == "cpu" {
		bank, off, ok := strings.Cut(rest, ":")
		if !ok {
			return Address{}, fmt.Errorf("address %q: CPU address must be cpu:bb:aaaa", s)
		}
		b, err := strconv.ParseUint(bank, 16, 8)
		if err != nil {
			return Address{}, fmt.Errorf("address %q: parse CPU bank: %w", s, err)
		}
		a, err := strconv.ParseUint(off, 16, 16)
		if err != nil {
			return Address{}, fmt.Errorf("address %q: parse CPU offset: %w", s, err)
		}
		return Address{Space: "cpu", Addr: uint32(b)<<16 | uint32(a)}, nil
	}
	if space == "dma" && strings.Contains(rest, ":") {
		chText, off, _ := strings.Cut(rest, ":")
		ch, err := strconv.ParseUint(chText, 0, 3)
		if err != nil {
			return Address{}, fmt.Errorf("address %q: parse DMA channel: %w", s, err)
		}
		addr, err := strconv.ParseUint(off, 0, 16)
		if err != nil {
			return Address{}, fmt.Errorf("address %q: parse DMA offset: %w", s, err)
		}
		start := uint64(0x4300 + ch*0x10)
		end := start + 0x0f
		if addr < start || addr > end {
			return Address{}, fmt.Errorf("address %q: DMA channel %d offset must be %#x-%#x", s, ch, start, end)
		}
		return Address{Space: "dma", Addr: uint32(addr)}, nil
	}
	addr, err := strconv.ParseUint(rest, 0, 32)
	if err != nil {
		return Address{}, fmt.Errorf("address %q: parse address: %w", s, err)
	}
	return Address{Space: space, Addr: uint32(addr)}, nil
}

func ParseRange(s string) (Range, error) {
	startText, endText, ok := strings.Cut(s, "-")
	start, err := ParseAddress(startText)
	if err != nil {
		return Range{}, err
	}
	if !ok {
		return Range{Space: start.Space, Start: start.Addr, End: start.Addr}, nil
	}
	if strings.Contains(endText, ":") {
		end, err := ParseAddress(endText)
		if err != nil {
			return Range{}, err
		}
		if end.Space != start.Space {
			return Range{}, fmt.Errorf("range %q: mixed address spaces", s)
		}
		return Range{Space: start.Space, Start: start.Addr, End: end.Addr}, nil
	}
	end, err := strconv.ParseUint(strings.TrimSpace(endText), 0, 32)
	if err != nil {
		return Range{}, fmt.Errorf("range %q: parse range end: %w", s, err)
	}
	return Range{Space: start.Space, Start: start.Addr, End: uint32(end)}, nil
}

func (r Range) Contains(space string, addr uint32) bool {
	return r.Space == space && addr >= r.Start && addr <= r.End
}

func (r Range) Intersects(o Range) bool {
	return r.Space == o.Space && r.Start <= o.End && o.Start <= r.End
}

func CPUSpace(addr uint32) (string, uint32) {
	bank := (addr >> 16) & 0xff
	off := addr & 0xffff
	if bank == 0x7e || bank == 0x7f {
		return "wram", (bank-0x7e)<<16 | off
	}
	if (bank <= 0x3f || (bank >= 0x80 && bank <= 0xbf)) && off <= 0x1fff {
		return "wram", off
	}
	if off >= 0x2140 && off <= 0x2143 {
		return "apu", off
	}
	if off >= 0x2100 && off <= 0x21ff {
		return "ppu", off
	}
	if off >= 0x4300 && off <= 0x437f {
		return "dma", off
	}
	return "cpu", addr & 0xffffff
}
