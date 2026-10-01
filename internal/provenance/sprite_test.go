package provenance

import (
	"fmt"
	"testing"
)

func TestSpritePacking(t *testing.T) {
	for sprite := 0; sprite < 128; sprite++ {
		for pair := 0; pair < 4; pair++ {
			e := []Event{
				{Kind: "ppu", Space: "oam", Addr: uint32(sprite * 4), Value: 255},
				{Kind: "ppu", Space: "oam", Addr: uint32(sprite*4 + 1), Value: 42},
				{Kind: "ppu", Space: "oam", Addr: uint32(sprite*4 + 2), Value: 23},
				{Kind: "ppu", Space: "oam", Addr: uint32(sprite*4 + 3), Value: 0xef},
				{Kind: "ppu", Space: "oam", Addr: uint32(512 + sprite/4), Value: uint8(pair) << uint((sprite%4)*2)},
				{Kind: "bus", Op: "write", Addr: 0x802101, Value: 0x40},
			}
			r, err := ExplainSprite(ordered(e), sprite, 0, 0, 0x10000)
			if err != nil {
				t.Fatal(err)
			}
			want := 255
			if pair&1 != 0 {
				want = -1
			}
			if *r.X != want || *r.Y != 42 || *r.Tile != 279 || *r.Palette != 7 || *r.Priority != 2 || !*r.HFlip || !*r.VFlip || *r.Large != (pair&2 != 0) {
				t.Fatalf("sprite %d pair %d: %+v", sprite, pair, r)
			}
			width := 8
			if pair&2 != 0 {
				width = 64
			}
			if *r.Width != width {
				t.Fatal("size selection", r.Width)
			}
		}
	}
}

func TestSpriteVersion(t *testing.T) {
	for _, tt := range []struct {
		name            string
		appendOverwrite bool
		missingWriter   bool
		wantLink        bool
	}{
		{"alias", false, false, true}, {"overwrite OAM", true, false, false}, {"missing WRAM writer", false, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := fixture()
			for i := range e {
				e[i].Frame = 82
			}
			if tt.missingWriter {
				e = e[1:]
			}
			if tt.appendOverwrite {
				e = append(e, Event{Frame: 82, Kind: "ppu", Space: "oam", Addr: 512, Value: 0})
			}
			r, err := ExplainSprite(ordered(e), 0, 82, 0x85fc, 0x8781)
			if err != nil {
				t.Fatal(err)
			}
			b := r.Bytes[4]
			if (b.Link != nil) != tt.wantLink {
				t.Fatalf("stale version link: %+v", b)
			}
			if tt.missingWriter && b.Link.Writer != nil {
				t.Fatal("invented writer")
			}
			if r.X != nil || r.Y != nil || r.Tile != nil || r.Width != nil {
				t.Fatal("invented initial state")
			}
		})
	}
}

func TestSpriteBoundary(t *testing.T) {
	e := ordered([]Event{{Frame: 1, Kind: "ppu", Space: "oam", Addr: 512, Value: 3}, {Frame: 2, Kind: "ppu", Space: "oam", Addr: 512, Value: 0}})
	r, err := ExplainSprite(e, 0, 1, 0, 0x10000)
	if err != nil || !*r.Large {
		t.Fatal(r, err)
	}
	e[1].Frame = 0
	if _, err := ExplainSprite(e, 0, 1, 0, 0x10000); err == nil {
		t.Fatal("accepted reordered frames")
	}
	for _, sprite := range []int{-1, 128} {
		if _, err := ExplainSprite(nil, sprite, 0, 0, 0x10000); err == nil {
			t.Fatal("accepted invalid sprite")
		}
	}
}

func ExampleExplainSprite() {
	events := []Event{{Kind: "ppu", Space: "oam", Addr: 512, Value: 2}}
	r, _ := ExplainSprite(events, 0, 0, 0, 0x10000)
	fmt.Println(*r.Large, r.X == nil)
	// Output: true true
}
