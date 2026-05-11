package trace

import "fmt"

// MMIORegister returns the conventional register name and side-effect class
// for a mapped CPU bus register address.
func MMIORegister(addr uint32) (name, category string) {
	switch {
	case addr >= 0x2100 && addr <= 0x213f:
		return ppuRegister(addr)
	case addr >= 0x2140 && addr <= 0x2143:
		return fmt.Sprintf("APUIO%d", addr-0x2140), "apu_port"
	case addr >= 0x4300 && addr <= 0x437f:
		ch := (addr - 0x4300) / 0x10
		reg := dmaRegister(addr & 0x0f)
		if reg == "" {
			return fmt.Sprintf("DMA%d_%02X", ch, addr&0x0f), "dma_register"
		}
		return fmt.Sprintf("DMA%d_%s", ch, reg), "dma_register"
	default:
		return "", ""
	}
}

// InputRegister returns the conventional register name for controller I/O.
func InputRegister(addr uint32) (name, category string) {
	off := addr & 0xffff
	switch {
	case off == 0x4016:
		return "JOYSER0", "controller_serial"
	case off == 0x4017:
		return "JOYSER1", "controller_serial"
	case off >= 0x4218 && off <= 0x421f:
		port := (off - 0x4218) / 2
		byteName := "L"
		if off&1 != 0 {
			byteName = "H"
		}
		return fmt.Sprintf("JOY%d%s", port+1, byteName), "auto_joypad"
	default:
		return "", ""
	}
}

func ppuRegister(addr uint32) (name, category string) {
	switch addr {
	case 0x2100:
		return "INIDISP", "display_control"
	case 0x2101:
		return "OBSEL", "oam_control"
	case 0x2102:
		return "OAMADDL", "oam_address"
	case 0x2103:
		return "OAMADDH", "oam_address"
	case 0x2104:
		return "OAMDATA", "oam_data"
	case 0x2105:
		return "BGMODE", "ppu_control"
	case 0x2106:
		return "MOSAIC", "ppu_control"
	case 0x2107, 0x2108, 0x2109, 0x210a:
		return fmt.Sprintf("BG%dSC", addr-0x2106), "ppu_tilemap"
	case 0x210b:
		return "BG12NBA", "ppu_tileset"
	case 0x210c:
		return "BG34NBA", "ppu_tileset"
	case 0x210d:
		return "BG1HOFS", "ppu_scroll"
	case 0x210e:
		return "BG1VOFS", "ppu_scroll"
	case 0x210f:
		return "BG2HOFS", "ppu_scroll"
	case 0x2110:
		return "BG2VOFS", "ppu_scroll"
	case 0x2111:
		return "BG3HOFS", "ppu_scroll"
	case 0x2112:
		return "BG3VOFS", "ppu_scroll"
	case 0x2113:
		return "BG4HOFS", "ppu_scroll"
	case 0x2114:
		return "BG4VOFS", "ppu_scroll"
	case 0x2115:
		return "VMAIN", "vram_address"
	case 0x2116:
		return "VMADDL", "vram_address"
	case 0x2117:
		return "VMADDH", "vram_address"
	case 0x2118:
		return "VMDATAL", "vram_data"
	case 0x2119:
		return "VMDATAH", "vram_data"
	case 0x211a:
		return "M7SEL", "mode7"
	case 0x211b:
		return "M7A", "mode7"
	case 0x211c:
		return "M7B", "mode7"
	case 0x211d:
		return "M7C", "mode7"
	case 0x211e:
		return "M7D", "mode7"
	case 0x211f:
		return "M7X", "mode7"
	case 0x2120:
		return "M7Y", "mode7"
	case 0x2121:
		return "CGADD", "cgram_address"
	case 0x2122:
		return "CGDATA", "cgram_data"
	case 0x2123, 0x2124, 0x2125:
		return fmt.Sprintf("W%dSEL", addr-0x2122), "window"
	case 0x2126:
		return "WH0", "window"
	case 0x2127:
		return "WH1", "window"
	case 0x2128:
		return "WH2", "window"
	case 0x2129:
		return "WH3", "window"
	case 0x212a:
		return "WBGLOG", "window"
	case 0x212b:
		return "WOBJLOG", "window"
	case 0x212c:
		return "TM", "screen_designation"
	case 0x212d:
		return "TS", "screen_designation"
	case 0x212e:
		return "TMW", "screen_designation"
	case 0x212f:
		return "TSW", "screen_designation"
	case 0x2130:
		return "CGWSEL", "color_math"
	case 0x2131:
		return "CGADSUB", "color_math"
	case 0x2132:
		return "COLDATA", "color_math"
	case 0x2133:
		return "SETINI", "display_control"
	case 0x2134:
		return "MPYL", "ppu_read"
	case 0x2135:
		return "MPYM", "ppu_read"
	case 0x2136:
		return "MPYH", "ppu_read"
	case 0x2137:
		return "SLHV", "counter_latch"
	case 0x2138:
		return "OAMDATAREAD", "oam_read"
	case 0x2139:
		return "VMDATALREAD", "vram_read"
	case 0x213a:
		return "VMDATAHREAD", "vram_read"
	case 0x213b:
		return "CGDATAREAD", "cgram_read"
	case 0x213c:
		return "OPHCT", "counter_read"
	case 0x213d:
		return "OPVCT", "counter_read"
	case 0x213e:
		return "STAT77", "ppu_status"
	case 0x213f:
		return "STAT78", "ppu_status"
	default:
		return fmt.Sprintf("PPU%04X", addr), "ppu_register"
	}
}

func dmaRegister(reg uint32) string {
	switch reg {
	case 0:
		return "DMAP"
	case 1:
		return "BBAD"
	case 2:
		return "A1T"
	case 3:
		return "A1TH"
	case 4:
		return "A1B"
	case 5:
		return "DAS"
	case 6:
		return "DASH"
	case 7:
		return "DASB"
	case 8:
		return "A2A"
	case 9:
		return "A2AH"
	case 0xa:
		return "NTRL"
	default:
		return ""
	}
}
