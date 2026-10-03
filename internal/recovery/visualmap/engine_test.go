package visualmap

import (
	"context"
	"fmt"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

func TestEngineSpriteHitAndProvenance(t *testing.T) {
	// Construct test basic block writing to WRAM $0A00 (STA $0A00)
	block := &structure.BasicBlock{
		ID:           "bb-test-spr",
		StartAddress: 0x008000,
		EndAddress:   0x008006,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "a964",
				Opcode:   0xA9,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
			},
			{
				ID:       "inst:8002",
				Address:  0x008002,
				Bytes:    "8d000a",
				Opcode:   0x8D,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
			},
		},
	}

	doc := &recovery.Document{}
	engine := NewEngine(doc, []*structure.BasicBlock{block})

	// Configure frame bounds for frame 10: VBLANK ends and active display starts at cycle 200,000
	engine.SetFrameBounds(10, FrameBounds{
		StartCycle:  200000,
		VBlankCycle: 190000,
		EndCycle:    250000,
	})

	// Configure OAM for frame 10: Sprite 0 at X=100, Y=50, Tile=20, Attrs=0
	var oam [544]uint8
	oam[0] = 100 // X low
	oam[1] = 50  // Y
	oam[2] = 20  // Tile
	oam[3] = 0   // Attr
	engine.SetOAMSnapshot(10, oam)

	// Ingest CPU write event to WRAM $0A00 before DMA
	engine.IngestEvent(trace.Event{
		Kind:  "bus",
		Op:    "write",
		Addr:  0x7E0A00,
		Value: 100,
		Cycle: 150000,
		Frame: 9,
		PC:    &trace.PC{Bank: 0x00, Addr: 0x8002},
	})

	// Ingest DMA transfer uploading WRAM $0A00..$0A03 to OAM $0000..$0003 during VBLANK
	engine.IngestEvent(trace.Event{
		Kind:  "dma",
		Cycle: 195000,
		Frame: 9,
		DMA: &trace.DMAContext{
			Channel: 0,
			Target:  0x04,
			Count:   4,
		},
		Source: trace.Range{Space: "wram", Start: 0x0A00, End: 0x0A03},
		Dest:   trace.Range{Space: "oam", Start: 0x0000, End: 0x0003},
	})

	tests := []struct {
		name      string
		x         int
		y         int
		wantKind  string
		wantSprID int
		wantWrite bool
	}{
		{
			name:      "Sprite center hit",
			x:         104,
			y:         54,
			wantKind:  "sprite",
			wantSprID: 0,
			wantWrite: true,
		},
		{
			name:      "Sprite top-left corner",
			x:         100,
			y:         50,
			wantKind:  "sprite",
			wantSprID: 0,
			wantWrite: true,
		},
		{
			name:      "Sprite miss outside bounding box",
			x:         120,
			y:         70,
			wantKind:  "backdrop",
			wantSprID: 0,
			wantWrite: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := engine.Query(context.Background(), 10, tt.x, tt.y)
			if err != nil {
				t.Fatalf("Query failed: %v", err)
			}
			if res.VisualEntity.Kind != tt.wantKind {
				t.Errorf("got kind %q, want %q", res.VisualEntity.Kind, tt.wantKind)
			}
			if tt.wantKind == "sprite" {
				if res.VisualEntity.SpriteIndex != tt.wantSprID {
					t.Errorf("got sprite index %d, want %d", res.VisualEntity.SpriteIndex, tt.wantSprID)
				}
				if tt.wantWrite {
					if res.DMATransfer == nil {
						t.Errorf("expected DMA transfer info, got nil")
					} else if res.DMATransfer.Channel != 0 {
						t.Errorf("got DMA channel %d, want 0", res.DMATransfer.Channel)
					}
					if res.CPUWrite == nil {
						t.Errorf("expected CPU write info, got nil")
					} else if res.CPUWrite.StoredValue != 100 {
						t.Errorf("got stored value %d, want 100", res.CPUWrite.StoredValue)
					}
					if res.CodeProvenance == nil {
						t.Errorf("expected code provenance, got nil")
					} else if res.CodeProvenance.BlockID != "bb-test-spr" {
						t.Errorf("got block ID %q, want 'bb-test-spr'", res.CodeProvenance.BlockID)
					}
				}
			}
		})
	}
}

func ExampleEngine_Query() {
	engine := NewEngine(&recovery.Document{}, nil)

	var oam [544]uint8
	oam[0] = 50 // X
	oam[1] = 60 // Y
	engine.SetOAMSnapshot(1, oam)

	res, err := engine.Query(context.Background(), 1, 52, 62)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("Kind: %s, Sprite: %d, Box: (%d, %d)\n",
		res.VisualEntity.Kind,
		res.VisualEntity.SpriteIndex,
		res.VisualEntity.BoundingBox.X,
		res.VisualEntity.BoundingBox.Y,
	)
	// Output: Kind: sprite, Sprite: 0, Box: (50, 60)
}
