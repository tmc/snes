package provenance

import (
	"fmt"
	"github.com/tmc/snes/internal/trace"
)

// SpriteByte describes the latest observed physical OAM byte at a boundary.
// A missing value is unknown, not zero. Link applies only to this byte version.
type SpriteByte struct {
	Address uint32 `json:"address"`
	Mask    uint8  `json:"used_mask"`
	Value   *uint8 `json:"value,omitempty"`
	Write   *Event `json:"physical_write,omitempty"`
	Link    *Link  `json:"dma_provenance,omitempty"`
	Status  string `json:"status"`
}

// SpriteExplanation describes an OAM entry, not a rendered pixel attribution.
// Decoded fields are interpretations of observed bytes; nil means unknown.
type SpriteExplanation struct {
	Sprite       int          `json:"sprite"`
	ThroughFrame int          `json:"through_host_frame"`
	Bytes        []SpriteByte `json:"bytes"`
	X            *int         `json:"x_signed,omitempty"`
	Y            *int         `json:"y,omitempty"`
	Tile         *int         `json:"tile_9bit,omitempty"`
	Palette      *int         `json:"palette,omitempty"`
	Priority     *int         `json:"priority,omitempty"`
	HFlip        *bool        `json:"hflip,omitempty"`
	VFlip        *bool        `json:"vflip,omitempty"`
	Large        *bool        `json:"large,omitempty"`
	OBSELWrite   *Event       `json:"obsel_write,omitempty"`
	SizeSelect   *int         `json:"size_select,omitempty"`
	Width        *int         `json:"noninterlaced_width,omitempty"`
	Height       *int         `json:"noninterlaced_height,omitempty"`
	Unknown      []string     `json:"unknown"`
}

// ExplainSprite reconstructs the latest observed OAM entry through a host frame.
// Events must include all physical OAM writes in the bounded window. No initial
// OAM state, scanline selection, visibility or pixel ownership is inferred.
func ExplainSprite(events []Event, sprite, throughFrame int, routineStart, routineEnd uint32) (SpriteExplanation, error) {
	out := SpriteExplanation{Sprite: sprite, ThroughFrame: throughFrame}
	if sprite < 0 || sprite >= 128 || throughFrame < 0 {
		return out, fmt.Errorf("invalid sprite or frame")
	}
	for i, e := range events {
		if e.ID != uint64(i) || (i > 0 && (e.Frame < events[i-1].Frame || e.Cycle < events[i-1].Cycle)) {
			return out, fmt.Errorf("event %d: invalid order", i)
		}
	}
	var bounded []Event
	for _, e := range events {
		if e.Frame <= throughFrame {
			bounded = append(bounded, e)
		}
	}
	result, err := Analyze(bounded, throughFrame, routineStart, routineEnd)
	if err != nil {
		return out, err
	}
	latest := map[uint32]Event{}
	var obsel *uint8
	for _, e := range bounded {
		if e.Kind == "ppu" && e.Space == "oam" {
			if e.Addr >= 544 {
				return out, fmt.Errorf("invalid physical OAM address %d", e.Addr)
			}
			latest[e.Addr] = e
		}
		if e.Kind == "bus" && e.Op == "write" {
			space, a := trace.CPUSpace(e.Addr)
			if space == "ppu" && a == 0x2101 {
				v := e.Value
				obsel = &v
				w := e
				out.OBSELWrite = &w
			}
		}
	}
	for i, addr := range []uint32{uint32(sprite * 4), uint32(sprite*4 + 1), uint32(sprite*4 + 2), uint32(sprite*4 + 3), uint32(512 + sprite/4)} {
		b := SpriteByte{Address: addr, Mask: 255, Status: "unknown_initial_or_uncaptured"}
		if i == 4 {
			b.Mask = 3 << uint((sprite%4)*2)
		}
		if w, ok := latest[addr]; ok {
			v := w.Value
			b.Value = &v
			ww := w
			b.Write = &ww
			b.Status = "observed_physical_write_origin_unknown"
			for _, link := range result.Links {
				if link.OAMWrite.ID == w.ID {
					l := link
					b.Link = &l
					b.Status = link.Status
				}
			}
		}
		out.Bytes = append(out.Bytes, b)
	}
	low, high := out.Bytes, out.Bytes[4].Value
	if high != nil {
		large := (*high>>uint((sprite%4)*2+1))&1 != 0
		out.Large = &large
	}
	if low[0].Value != nil && high != nil {
		x := int(*low[0].Value) | int((*high>>uint((sprite%4)*2))&1)<<8
		if x >= 256 {
			x -= 512
		}
		out.X = &x
	}
	if low[1].Value != nil {
		y := int(*low[1].Value)
		out.Y = &y
	}
	if a := low[3].Value; a != nil {
		pal, pri := int((*a>>1)&7), int((*a>>4)&3)
		hf, vf := *a&64 != 0, *a&128 != 0
		out.Palette = &pal
		out.Priority = &pri
		out.HFlip = &hf
		out.VFlip = &vf
		if low[2].Value != nil {
			tile := int(*low[2].Value) | int(*a&1)<<8
			out.Tile = &tile
		}
	}
	if obsel != nil {
		sel := int(*obsel >> 5)
		out.SizeSelect = &sel
		if out.Large != nil {
			sizes := [8][4]int{{8, 8, 16, 16}, {8, 8, 32, 32}, {8, 8, 64, 64}, {16, 16, 32, 32}, {16, 16, 64, 64}, {32, 32, 64, 64}, {16, 32, 32, 64}, {16, 32, 32, 32}}
			off := 0
			if *out.Large {
				off = 2
			}
			w, h := sizes[sel][off], sizes[sel][off+1]
			out.Width = &w
			out.Height = &h
		}
	} else {
		out.Unknown = append(out.Unknown, "OBSEL before the capture window is unknown")
	}
	for _, b := range out.Bytes {
		if b.Value == nil {
			out.Unknown = append(out.Unknown, fmt.Sprintf("OAM byte %d not observed", b.Address))
		}
	}
	out.Unknown = append(out.Unknown, "render-time OAM selection, interlace context and pixel visibility not established", "writer event IDs are bus-event IDs; instruction sequence and recovered routine names unavailable")
	return out, nil
}
