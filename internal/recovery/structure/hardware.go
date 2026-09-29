package structure

import "fmt"

// HardwareRegister returns the canonical SNES hardware register name if addr is a memory-mapped register.
func HardwareRegister(addr uint32) string {
	// Standard SNES MMIO mirrors in bank $00-$3F and $80-$BF for addresses $2000-$5FFF.
	bank := (addr >> 16) & 0xFF
	pageAddr := uint16(addr & 0xFFFF)

	if !isMMIOBank(byte(bank)) {
		return ""
	}

	switch pageAddr {
	case 0x2100:
		return "INIDISP"
	case 0x2101:
		return "OBSEL"
	case 0x2102:
		return "OAMADDL"
	case 0x2103:
		return "OAMADDH"
	case 0x2104:
		return "OAMDATA"
	case 0x2105:
		return "BGMODE"
	case 0x2106:
		return "MOSAIC"
	case 0x2107:
		return "BG1SC"
	case 0x2108:
		return "BG2SC"
	case 0x2109:
		return "BG3SC"
	case 0x210A:
		return "BG4SC"
	case 0x210B:
		return "BG12NBA"
	case 0x210C:
		return "BG34NBA"
	case 0x210D:
		return "BG1HOFS"
	case 0x210E:
		return "BG1VOFS"
	case 0x210F:
		return "BG2HOFS"
	case 0x2110:
		return "BG2VOFS"
	case 0x2111:
		return "BG3HOFS"
	case 0x2112:
		return "BG3VOFS"
	case 0x2113:
		return "BG4HOFS"
	case 0x2114:
		return "BG4VOFS"
	case 0x2115:
		return "VMAIN"
	case 0x2116:
		return "VMADDL"
	case 0x2117:
		return "VMADDH"
	case 0x2118:
		return "VMDATAL"
	case 0x2119:
		return "VMDATAH"
	case 0x211A:
		return "M7SEL"
	case 0x211B:
		return "M7A"
	case 0x211C:
		return "M7B"
	case 0x211D:
		return "M7C"
	case 0x211E:
		return "M7D"
	case 0x211F:
		return "M7X"
	case 0x2120:
		return "M7Y"
	case 0x2121:
		return "CGADD"
	case 0x2122:
		return "CGDATA"
	case 0x2123:
		return "W12SEL"
	case 0x2124:
		return "W34SEL"
	case 0x2125:
		return "WOBJSEL"
	case 0x2126:
		return "WH0"
	case 0x2127:
		return "WH1"
	case 0x2128:
		return "WH2"
	case 0x2129:
		return "WH3"
	case 0x212A:
		return "WBGLOG"
	case 0x212B:
		return "WOBJLOG"
	case 0x212C:
		return "TM"
	case 0x212D:
		return "TS"
	case 0x212E:
		return "TMW"
	case 0x212F:
		return "TSW"
	case 0x2130:
		return "CGWSEL"
	case 0x2131:
		return "CGADSUB"
	case 0x2132:
		return "COLDATA"
	case 0x2133:
		return "SETINI"
	case 0x2134:
		return "MPYL"
	case 0x2135:
		return "MPYM"
	case 0x2136:
		return "MPYH"
	case 0x2137:
		return "SLHV"
	case 0x2138:
		return "OAMDATAREAD"
	case 0x2139:
		return "VMDATAREADL"
	case 0x213A:
		return "VMDATAREADH"
	case 0x213B:
		return "CGDATAREAD"
	case 0x213C:
		return "OPHCT"
	case 0x213D:
		return "OPVCT"
	case 0x213E:
		return "STAT77"
	case 0x213F:
		return "STAT78"
	case 0x2140:
		return "APUIO0"
	case 0x2141:
		return "APUIO1"
	case 0x2142:
		return "APUIO2"
	case 0x2143:
		return "APUIO3"
	case 0x2180:
		return "WMDATA"
	case 0x2181:
		return "WMADDL"
	case 0x2182:
		return "WMADDM"
	case 0x2183:
		return "WMADDH"
	case 0x4200:
		return "NMITIMEN"
	case 0x4201:
		return "WRIO"
	case 0x4202:
		return "WRMPYA"
	case 0x4203:
		return "WRMPYB"
	case 0x4204:
		return "WRDIVL"
	case 0x4205:
		return "WRDIVH"
	case 0x4206:
		return "WRDIVB"
	case 0x4207:
		return "HTIMEL"
	case 0x4208:
		return "HTIMEH"
	case 0x4209:
		return "VTIMEL"
	case 0x420A:
		return "VTIMEH"
	case 0x420B:
		return "MDMAEN"
	case 0x420C:
		return "HDMAEN"
	case 0x420D:
		return "MEMSEL"
	case 0x4210:
		return "RDNMI"
	case 0x4211:
		return "TIMEUP"
	case 0x4212:
		return "HVBJOY"
	case 0x4213:
		return "RDIO"
	case 0x4214:
		return "RDDIVL"
	case 0x4215:
		return "RDDIVH"
	case 0x4216:
		return "RDMPYL"
	case 0x4217:
		return "RDMPYH"
	case 0x4218:
		return "JOY1L"
	case 0x4219:
		return "JOY1H"
	case 0x421A:
		return "JOY2L"
	case 0x421B:
		return "JOY2H"
	}

	// DMA registers: $4300-$437B
	if pageAddr >= 0x4300 && pageAddr <= 0x437B {
		ch := (pageAddr >> 4) & 0x7
		reg := pageAddr & 0xF
		switch reg {
		case 0x0:
			return fmt.Sprintf("DMAP%d", ch)
		case 0x1:
			return fmt.Sprintf("BBAD%d", ch)
		case 0x2:
			return fmt.Sprintf("A1T%dL", ch)
		case 0x3:
			return fmt.Sprintf("A1T%dH", ch)
		case 0x4:
			return fmt.Sprintf("A1B%d", ch)
		case 0x5:
			return fmt.Sprintf("DAS%dL", ch)
		case 0x6:
			return fmt.Sprintf("DAS%dH", ch)
		case 0x7:
			return fmt.Sprintf("DASB%d", ch)
		case 0x8:
			return fmt.Sprintf("A2A%dL", ch)
		case 0x9:
			return fmt.Sprintf("A2A%dH", ch)
		case 0xA:
			return fmt.Sprintf("NTRL%d", ch)
		}
	}

	return ""
}

func isMMIOBank(bank byte) bool {
	return (bank <= 0x3F) || (bank >= 0x80 && bank <= 0xBF)
}

// ClassifyAddress returns the logical address space for a 24-bit SNES bus address.
func ClassifyAddress(addr uint32) string {
	if reg := HardwareRegister(addr); reg != "" {
		return "hardware_register"
	}
	bank := (addr >> 16) & 0xFF
	pageAddr := uint16(addr & 0xFFFF)

	if bank == 0x7E || bank == 0x7F {
		return "wram"
	}
	if isMMIOBank(byte(bank)) && pageAddr < 0x2000 {
		return "wram" // low 8KB WRAM mirror
	}
	if pageAddr >= 0x8000 {
		return "rom"
	}
	return "unknown"
}
