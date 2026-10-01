package machinebranch

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/provenance"
)

func spriteFixture(t *testing.T) (SpriteEdit, []byte, spriteCapture) {
	t.Helper()
	rom := make([]byte, 32768)
	copy(rom[0x614:], []byte{0x99, 0, 0x0a})
	rom[0x7fd5] = 0x20
	rom[0x7ffd] = 0x80
	c := spriteCapture{Schema: 1, ROMSHA256: digest(rom), Complete: true, WriterCoverage: "all_cpu_dma_hdma_wram_and_wram_port", From: 82, To: 83, Events: []provenance.Event{
		{ID: 0, Kind: "bus", Actor: "cpu", Frame: 82, PPUFrame: 82, Cycle: 10, PC: 0x8614, Op: "write", Addr: 0xa00, Value: 0xaa},
		{ID: 1, Kind: "dma", Frame: 82, PPUFrame: 82, Cycle: 20, Addr: 0xa00, Target: 4, Count: 1},
		{ID: 2, Kind: "bus", Actor: "dma_or_hdma", Frame: 82, PPUFrame: 82, Cycle: 24, Op: "read", Addr: 0xa00, Value: 0xaa},
		{ID: 3, Kind: "bus", Actor: "dma_or_hdma", Frame: 82, PPUFrame: 82, Cycle: 28, Op: "write", Addr: 0x2104, Value: 0xaa},
		{ID: 4, Kind: "ppu", Frame: 82, PPUFrame: 82, Cycle: 28, Space: "oam", Op: "write", Addr: 512, Value: 0xaa},
	}}
	edit := SpriteEdit{CapturePath: filepath.Join(t.TempDir(), "capture.gz"), Sprite: 0, ThroughFrame: 82}
	small := false
	edit.Large = &small
	return edit, rom, c
}
func writeSpriteFixture(t *testing.T, edit SpriteEdit, c spriteCapture) SpriteEdit {
	t.Helper()
	f, e := os.Create(edit.CapturePath)
	if e != nil {
		t.Fatal(e)
	}
	z := gzip.NewWriter(f)
	if e = json.NewEncoder(z).Encode(c); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(edit.CapturePath)
	if e != nil {
		t.Fatal(e)
	}
	edit.CaptureSHA256 = digest(b)
	return edit
}
func TestSpritePrepare(t *testing.T) {
	edit, rom, c := spriteFixture(t)
	edit = writeSpriteFixture(t, edit, c)
	r, e := prepareSprite(edit, rom, digest(rom))
	if e != nil {
		t.Fatal(e)
	}
	if r.Mask != 2 || r.Before != 0xaa || r.After != 0xa8 || r.After&^r.Mask != r.Before&^r.Mask || r.CapturedProofEligible {
		t.Fatalf("invalid experiment %+v", r)
	}
}
func TestSpriteCaptureRefusals(t *testing.T) {
	for _, name := range []string{"incomplete", "coverage", "ROM", "missing_writer", "actor", "alias", "selector", "stale_version", "pin"} {
		t.Run(name, func(t *testing.T) {
			edit, rom, c := spriteFixture(t)
			switch name {
			case "incomplete":
				c.Complete = false
			case "coverage":
				c.WriterCoverage = "filtered"
			case "ROM":
				c.ROMSHA256 = digest([]byte("wrong"))
			case "missing_writer":
				c.Events[0].Addr = 0xa01
			case "actor":
				c.Events[0].Actor = "dma_or_hdma"
			case "alias":
				c.Events[2].Addr = 0x7e0a00
				c.Events[1].Addr = 0x7e0a00
			case "selector":
				edit.Sprite = 128
			case "stale_version":
				c.Events[0].Value = 0xbb
			}
			edit = writeSpriteFixture(t, edit, c)
			if name == "pin" {
				edit.CaptureSHA256 = digest([]byte("wrong"))
			}
			if _, e := prepareSprite(edit, rom, digest(rom)); e == nil {
				t.Fatal("invalid capture accepted")
			}
		})
	}
}
func TestSpriteRuntimeRefusals(t *testing.T) {
	for _, name := range []string{"context", "opcode", "value", "intervening_alias", "WRAM_port", "missing_consumption"} {
		t.Run(name, func(t *testing.T) {
			edit, rom, c := spriteFixture(t)
			edit = writeSpriteFixture(t, edit, c)
			r, e := prepareSprite(edit, rom, digest(rom))
			if e != nil {
				t.Fatal(e)
			}
			s := snes.NewSystem(nil)
			if e = s.LoadROM(rom); e != nil {
				t.Fatal(e)
			}
			s.Power()
			s.PPU.FrameCount = 82
			s.CPU.P = 0x30
			s.CPU.A = 0xaa
			s.CPU.DB = 0
			s.CPU.PB = 0
			s.CPU.PC = 0x8615
			s.CPU.LastOpcodePB = 0
			s.CPU.LastOpcodePC = 0x8614
			s.CPU.LastOpcode = 0x99
			s.CPU.Cycles = 10
			if name == "context" {
				s.CPU.Y = 1
			}
			if name == "opcode" {
				s.CPU.LastOpcode = 0xea
			}
			g := attachSprite(s, r, true)
			s.CPU.BeforeExecute()
			value := uint8(0xaa)
			if name == "value" {
				value = 0xbb
			}
			s.Bus.Write(0xa00, value)
			s.CPU.AfterExecute()
			if name == "intervening_alias" {
				s.CPU.Cycles++
				s.Bus.Write(0x7e0a00, 0xaa)
			}
			if name == "WRAM_port" {
				s.CPU.Cycles++
				s.Bus.Write(0x2180, 0)
			}
			if g.complete() == nil {
				t.Fatal("invalid runtime accepted")
			}
			if r.CapturedProofEligible {
				t.Fatal("edited proof grant")
			}
		})
	}
}
