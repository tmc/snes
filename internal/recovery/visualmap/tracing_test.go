package visualmap

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/structure"
	"github.com/tmc/snes/internal/trace"
)

func TestTraceFrame333_SpriteAndCoordinate(t *testing.T) {
	// Construct test basic block writing to WRAM $7E:0200
	block := &structure.BasicBlock{
		ID:           "bb-trace-oam",
		StartAddress: 0x008000,
		EndAddress:   0x008008,
		Instructions: []recovery.Instruction{
			{
				ID:       "inst:8000",
				Address:  0x008000,
				Bytes:    "a978",
				Opcode:   0xA9,
				Mnemonic: "lda",
				Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
			},
			{
				ID:       "inst:8002",
				Address:  0x008002,
				Bytes:    "8d0002",
				Opcode:   0x8D,
				Mnemonic: "sta",
				Context:  recovery.Context{E: "clear", M: "set", X: "set", C: "clear"},
			},
		},
	}

	doc := &recovery.Document{}
	engine := NewEngine(doc, []*structure.BasicBlock{block})

	// Frame bounds for PPU frame 333
	engine.SetFrameBounds(333, FrameBounds{
		StartCycle:  3330000,
		VBlankCycle: 3320000,
		EndCycle:    3340000,
	})

	// Setup OAM table for frame 333:
	// Sprite 0 at X=120, Y=80, Tile=42, Palette=3, Priority=2, HFlip=false, VFlip=true, Large=true
	var oam [544]uint8
	oam[0] = 120                      // X low
	oam[1] = 80                       // Y
	oam[2] = 42                       // Tile low 8 bits
	oam[3] = (3 << 1) | (2 << 4) | 0x80 // Palette=3, Priority=2, VFlip=1
	// High table byte for sprite 0: bit 0 = X high (0), bit 1 = Large (1) -> 0x02
	oam[512] = 0x02

	engine.SetOAMSnapshot(333, oam)

	// Preceding CPU retirement: instruction at 00:8000 (LDA #$78)
	engine.IngestEvent(trace.Event{
		ID:    100,
		Kind:  "cpu_insn",
		Cycle: 3300000,
		Frame: 332,
		Insn: &trace.Insn{
			Seq: 500,
			Entry: trace.Registers{
				PC: 0x8000, PB: 0x00, Cycles: 3299990,
			},
			Exit: trace.Registers{
				PC: 0x8002, PB: 0x00, A: 0x0078, Cycles: 3300000,
			},
			Status: "retired",
		},
	})

	// CPU write event populating shadow buffer at $7E:0200
	engine.IngestEvent(trace.Event{
		ID:    101,
		Kind:  "bus",
		Op:    "write",
		Addr:  0x7E0200,
		Value: 120,
		Cycle: 3300050,
		Frame: 332,
		PC:    &trace.PC{Bank: 0x00, Addr: 0x8002},
	})

	// Owning CPU retirement: instruction at 00:8002 (STA $0200)
	engine.IngestEvent(trace.Event{
		ID:    102,
		Kind:  "cpu_insn",
		Cycle: 3300060,
		Frame: 332,
		Insn: &trace.Insn{
			Seq: 501,
			Entry: trace.Registers{
				PC: 0x8002, PB: 0x00, A: 0x0078, Cycles: 3300010,
			},
			Exit: trace.Registers{
				PC: 0x8005, PB: 0x00, Cycles: 3300060,
			},
			Fetches: []trace.FetchRecord{
				{Addr: 0x8002, Value: 0x8D, Role: "opcode"},
			},
			Status: "retired",
		},
	})

	// DMA register configuration writes: Channel 0 ($4300-$4306)
	engine.IngestEvent(trace.Event{
		ID: 103, Kind: "bus", Op: "write", Addr: 0x4300, Value: 0x00, Cycle: 3310000, // DMAP0 = 0
	})
	engine.IngestEvent(trace.Event{
		ID: 104, Kind: "bus", Op: "write", Addr: 0x4301, Value: 0x04, Cycle: 3310010, // BBAD0 = $04 ($2104)
	})
	engine.IngestEvent(trace.Event{
		ID: 105, Kind: "bus", Op: "write", Addr: 0x4302, Value: 0x00, Cycle: 3310020, // A1T0L = $00
	})
	engine.IngestEvent(trace.Event{
		ID: 106, Kind: "bus", Op: "write", Addr: 0x4303, Value: 0x02, Cycle: 3310030, // A1T0H = $02 -> $0200
	})
	engine.IngestEvent(trace.Event{
		ID: 107, Kind: "bus", Op: "write", Addr: 0x4304, Value: 0x7E, Cycle: 3310040, // A1B0 = $7E
	})
	engine.IngestEvent(trace.Event{
		ID: 108, Kind: "bus", Op: "write", Addr: 0x4305, Value: 0x20, Cycle: 3310050, // DAS0L = $20
	})
	engine.IngestEvent(trace.Event{
		ID: 109, Kind: "bus", Op: "write", Addr: 0x4306, Value: 0x02, Cycle: 3310060, // DAS0H = $02 -> 544 bytes
	})

	// DMA transfer: uploads WRAM $7E:0200..$7E:041F to OAM $0000..$021F
	engine.IngestEvent(trace.Event{
		ID:    110,
		Kind:  "dma",
		Cycle: 3325000,
		Frame: 332,
		DMA: &trace.DMAContext{
			Channel: 0,
			Target:  0x04,
			Count:   544,
		},
		Source: trace.Range{Space: "wram", Start: 0x0200, End: 0x041F},
		Dest:   trace.Range{Space: "oam", Start: 0x0000, End: 0x021F},
	})

	t.Run("Trace by sprite index 0 at frame 333", func(t *testing.T) {
		res, err := engine.TraceSprite(context.Background(), 333, 0)
		if err != nil {
			t.Fatalf("TraceSprite: %v", err)
		}
		if res.Status != "candidate_correlated" {
			t.Fatalf("status = %q, want 'candidate_correlated'", res.Status)
		}
		if res.CandidateType != "named_sprite" {
			t.Errorf("candidate_type = %q, want 'named_sprite'", res.CandidateType)
		}
		if res.PPUFrame != 333 {
			t.Errorf("PPUFrame = %d, want 333", res.PPUFrame)
		}

		// Verify OAM entry
		oam := res.OAM
		if oam.Index != 0 {
			t.Errorf("OAM Index = %d, want 0", oam.Index)
		}
		if oam.X != 120 || oam.Y != 80 {
			t.Errorf("OAM (X, Y) = (%d, %d), want (120, 80)", oam.X, oam.Y)
		}
		if oam.Tile != 42 {
			t.Errorf("OAM Tile = %d, want 42", oam.Tile)
		}
		if oam.Palette != 3 {
			t.Errorf("OAM Palette = %d, want 3", oam.Palette)
		}
		if oam.Priority != 2 {
			t.Errorf("OAM Priority = %d, want 2", oam.Priority)
		}
		if !oam.VFlip || oam.HFlip {
			t.Errorf("OAM Flip V/H = %v/%v, want true/false", oam.VFlip, oam.HFlip)
		}
		if !oam.Large {
			t.Errorf("OAM Large = %v, want true", oam.Large)
		}

		// Verify DMA registers
		if res.DMARegisters == nil {
			t.Fatalf("DMARegisters is nil")
		}
		regs := res.DMARegisters
		if regs.Channel != 0 || regs.BaseRegister != "$4300" {
			t.Errorf("DMA Channel/Base = %d/%s, want 0/$4300", regs.Channel, regs.BaseRegister)
		}
		if regs.BBAD != 0x04 {
			t.Errorf("DMA BBAD = %#x, want 0x04", regs.BBAD)
		}
		if regs.A1T != 0x0200 || regs.A1B != 0x7E {
			t.Errorf("DMA A1T/A1B = %#x/%#x, want 0x0200/0x7E", regs.A1T, regs.A1B)
		}
		if regs.DAS != 544 {
			t.Errorf("DMA DAS = %d, want 544", regs.DAS)
		}
		if len(regs.Writes) == 0 {
			t.Errorf("expected observed register writes, got none")
		}

		// Verify WRAM shadow buffer
		if res.ShadowBuffer == nil {
			t.Fatalf("ShadowBuffer is nil")
		}
		shadow := res.ShadowBuffer
		if shadow.BaseAddressHex != "$7E:0200" {
			t.Errorf("ShadowBuffer base = %s, want $7E:0200", shadow.BaseAddressHex)
		}
		if shadow.SpriteLowHex != "$7E:0200" {
			t.Errorf("ShadowBuffer sprite low = %s, want $7E:0200", shadow.SpriteLowHex)
		}

		// Verify CPU write
		if res.CPUWrite == nil {
			t.Fatalf("CPUWrite is nil")
		}
		write := res.CPUWrite
		if write.StoredValue != 120 {
			t.Errorf("CPUWrite stored value = %d, want 120", write.StoredValue)
		}
		if write.PC != "00:8002" {
			t.Errorf("CPUWrite PC = %s, want 00:8002", write.PC)
		}

		// Verify Retirement sequence
		if res.Retirement == nil {
			t.Fatalf("Retirement is nil")
		}
		ret := res.Retirement
		if ret.Seq != 501 {
			t.Errorf("Retirement Seq = %d, want 501", ret.Seq)
		}
		if ret.PC != "00:8002" {
			t.Errorf("Retirement PC = %s, want 00:8002", ret.PC)
		}
		if ret.Status != "retired" {
			t.Errorf("Retirement Status = %s, want retired", ret.Status)
		}
		if ret.Preceding == nil || ret.Preceding.Seq != 500 {
			t.Errorf("Preceding retirement seq = %+v, want 500", ret.Preceding)
		}

		// Verify Code provenance
		if res.Code == nil || res.Code.BlockID != "bb-trace-oam" {
			t.Errorf("Code block ID = %+v, want 'bb-trace-oam'", res.Code)
		}
	})

	t.Run("Trace by screen coordinate (125, 85) hit at frame 333", func(t *testing.T) {
		res, err := engine.TracePixel(context.Background(), 333, 125, 85)
		if err != nil {
			t.Fatalf("TracePixel: %v", err)
		}
		if res.Status != "candidate_correlated" {
			t.Fatalf("status = %q, want 'candidate_correlated'", res.Status)
		}
		if res.CandidateType != "geometric_candidate" {
			t.Errorf("candidate_type = %q, want 'geometric_candidate'", res.CandidateType)
		}
		if !strings.Contains(res.OwnershipQualification, "candidate_geometric_only") {
			t.Errorf("ownership_qualification = %q, want candidate_geometric_only", res.OwnershipQualification)
		}
		if res.OAM.Index != 0 {
			t.Errorf("OAM Index = %d, want 0", res.OAM.Index)
		}
		if res.CPUWrite == nil || res.CPUWrite.StoredValue != 120 {
			t.Errorf("CPUWrite unexpected: %+v", res.CPUWrite)
		}
	})

	t.Run("Retained overlap coordinate (105, 51) returns qualified geometric candidate without claiming winning pixel", func(t *testing.T) {
		var oamOverlap [544]uint8
		oamOverlap[0] = 100 // Sprite 0 X
		oamOverlap[1] = 50  // Sprite 0 Y
		oamOverlap[2] = 1   // Sprite 0 Tile
		oamOverlap[4] = 102 // Sprite 1 X
		oamOverlap[5] = 50  // Sprite 1 Y
		oamOverlap[6] = 2   // Sprite 1 Tile
		engine.SetOAMSnapshot(1, oamOverlap)

		res, err := engine.TracePixel(context.Background(), 1, 105, 51)
		if err != nil {
			t.Fatalf("TracePixel: %v", err)
		}
		if res.CandidateType != "geometric_candidate" {
			t.Errorf("candidate_type = %q, want 'geometric_candidate'", res.CandidateType)
		}
		if res.WinningPixelWitness != "absent: compositor arbitration and tile transparency not modeled" {
			t.Errorf("winning_pixel_witness = %q, want absent", res.WinningPixelWitness)
		}
		if !strings.Contains(res.OwnershipQualification, "winning pixel ownership unknown") {
			t.Errorf("ownership_qualification = %q, want winning pixel ownership unknown", res.OwnershipQualification)
		}
	})

	t.Run("Trace by screen coordinate miss outside sprite", func(t *testing.T) {
		res, err := engine.TracePixel(context.Background(), 333, 200, 200)
		if err != nil {
			t.Fatalf("TracePixel: %v", err)
		}
		if res.Status != "candidate_unmatched" {
			t.Errorf("status = %q, want 'candidate_unmatched'", res.Status)
		}
	})

	t.Run("Trace invalid sprite index fails", func(t *testing.T) {
		_, err := engine.TraceSprite(context.Background(), 333, 128)
		if err == nil {
			t.Fatal("expected error for sprite index 128, got nil")
		}
	})
}

func ExampleEngine_Trace() {
	engine := NewEngine(&recovery.Document{}, nil)

	var oam [544]uint8
	oam[0] = 50 // X
	oam[1] = 60 // Y
	oam[2] = 10 // Tile
	engine.SetOAMSnapshot(333, oam)

	res, err := engine.TraceSprite(context.Background(), 333, 0)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("PPU Frame: %d, Sprite: %d, OAM: (X=%d, Y=%d, Tile=%d), Status: %s\n",
		res.PPUFrame,
		res.OAM.Index,
		res.OAM.X,
		res.OAM.Y,
		res.OAM.Tile,
		res.Status,
	)
	// Output: PPU Frame: 333, Sprite: 0, OAM: (X=50, Y=60, Tile=10), Status: no_dma_transfer
}
