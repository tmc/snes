package machinebranch

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/cpu"
	"github.com/tmc/snes/internal/ppu"
	"github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/trace"
)

// SpriteEdit selects a size bit from explicit operator-pinned capture evidence.
// Large=nil is a no-edit control. This changes WRAM data, not C or ROM source.
type SpriteEdit struct {
	CapturePath   string `json:"capture_path"`
	CaptureSHA256 string `json:"capture_sha256"`
	Sprite        int    `json:"sprite"`
	ThroughFrame  int    `json:"through_frame"`
	Large         *bool  `json:"large"`
}

// SpriteExperiment separates observed capture links from experimental runtime
// intervention and current execution context. No pixel ownership is implied.
type SpriteExperiment struct {
	CaptureSHA256         string                       `json:"capture_sha256"`
	Explanation           provenance.SpriteExplanation `json:"captured_explanation"`
	Link                  provenance.Link              `json:"captured_link"`
	Mask                  uint8                        `json:"size_mask"`
	Before                uint8                        `json:"before"`
	After                 uint8                        `json:"after"`
	Applied               bool                         `json:"applied"`
	RuntimeEntry          cpu.Snapshot                 `json:"runtime_entry"`
	CompletionCycle       uint64                       `json:"completion_cycle"`
	WriterMatched         bool                         `json:"writer_matched"`
	DMAReadMatched        bool                         `json:"dma_read_matched"`
	RegisterWriteMatched  bool                         `json:"register_write_matched"`
	OAMWriteMatched       bool                         `json:"oam_write_matched"`
	CapturedProofEligible bool                         `json:"captured_proof_eligible"`
}

type spriteCapture struct {
	Schema         int                `json:"schema"`
	ROMSHA256      string             `json:"rom_sha256"`
	EndStateSHA256 string             `json:"end_state_sha256"`
	Complete       bool               `json:"complete"`
	WriterCoverage string             `json:"writer_coverage"`
	From           int                `json:"from"`
	To             int                `json:"to"`
	Events         []provenance.Event `json:"events"`
}

func prepareSprite(c SpriteEdit, rom []byte, romSHA string) (*SpriteExperiment, error) {
	raw, err := readPinned(c.CapturePath, c.CaptureSHA256, 16<<20)
	if err != nil {
		return nil, fmt.Errorf("read sprite capture: %w", err)
	}
	z, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	decoded, err := io.ReadAll(io.LimitReader(z, 32<<20+1))
	closeErr := z.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(decoded) > 32<<20 {
		return nil, fmt.Errorf("sprite capture exceeds decoded bound")
	}
	var capture spriteCapture
	d := json.NewDecoder(bytes.NewReader(decoded))
	d.DisallowUnknownFields()
	if err = d.Decode(&capture); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("sprite capture trailing data")
	}
	if capture.Schema != 1 || !capture.Complete || capture.WriterCoverage != "all_cpu_dma_hdma_wram_and_wram_port" || capture.ROMSHA256 != romSHA || c.ThroughFrame < capture.From || c.ThroughFrame >= capture.To {
		return nil, fmt.Errorf("unsupported sprite capture coverage")
	}
	explanation, err := provenance.ExplainSprite(capture.Events, c.Sprite, c.ThroughFrame, 0, 0)
	if err != nil {
		return nil, err
	}
	high := explanation.Bytes[4]
	if high.Link == nil || high.Link.Writer == nil || high.Status != "observed_last_writer" {
		return nil, fmt.Errorf("sprite high byte has no verified writer chain")
	}
	link := *high.Link
	writer := link.Writer
	if writer.Actor != "cpu" || writer.Kind != "bus" || writer.Op != "write" || writer.Frame != c.ThroughFrame || writer.PC>>16 != 0 || writer.PC&0xffff < 0x8000 {
		return nil, fmt.Errorf("unsupported sprite writer")
	}
	offset := int(writer.PC & 0x7fff)
	if offset+3 > len(rom) || rom[offset] != 0x99 {
		return nil, fmt.Errorf("sprite writer is not supported STA absolute,Y")
	}
	operand := uint32(rom[offset+1]) | uint32(rom[offset+2])<<8
	if operand != writer.Addr || writer.Addr >= 0x2000 || link.Canonical != writer.Addr || link.Read.Addr != writer.Addr || link.Read.Value != writer.Value || link.OAMWrite.Addr != uint32(512+c.Sprite/4) {
		return nil, fmt.Errorf("sprite writer source alias mismatch")
	}
	mask := uint8(2 << uint((c.Sprite%4)*2))
	after := writer.Value
	if c.Large != nil {
		if *c.Large {
			after |= mask
		} else {
			after &^= mask
		}
	}
	return &SpriteExperiment{CaptureSHA256: c.CaptureSHA256, Explanation: explanation, Link: link, Mask: mask, Before: writer.Value, After: after}, nil
}

type spriteGuard struct {
	report  *SpriteExperiment
	err     error
	pending bool
	entry   cpu.Snapshot
	edit    bool
}

func attachSprite(s *snes.System, report *SpriteExperiment, edit bool) *spriteGuard {
	if !edit {
		report.After = report.Before
	}
	g := &spriteGuard{report: report, edit: edit}
	w := *report.Link.Writer
	before, after := s.CPU.BeforeExecute, s.CPU.AfterExecute
	read, write, ppuWrite := s.Bus.ReadHook, s.Bus.WriteHook, s.PPU.WriteHook
	s.CPU.BeforeExecute = func() {
		if before != nil {
			before()
		}
		if uint32(s.CPU.LastOpcodePB)<<16|uint32(s.CPU.LastOpcodePC) == w.PC {
			g.entry = s.CPU.Snapshot()
		}
	}
	s.Bus.WriteHook = func(a uint32, v uint8) {
		if write != nil {
			write(a, v)
		}
		if g.err != nil {
			return
		}
		space, off := trace.CPUSpace(a)
		if g.report.WriterMatched && !g.report.DMAReadMatched && (space == "wram" && off == report.Link.Canonical || a&0xffff == 0x2180) {
			g.err = fmt.Errorf("sprite byte version changed before DMA")
			return
		}
		if s.CPU.Cycles == w.Cycle && a == w.Addr {
			pc := uint32(s.CPU.LastOpcodePB)<<16 | uint32(s.CPU.LastOpcodePC)
			if int(s.PPU.FrameCount) != w.PPUFrame || pc != w.PC || s.CPU.LastOpcode != 0x99 || v != w.Value || s.DMA.Busy() || g.entry.PB != 0 || g.entry.PC != uint16(w.PC)+1 || g.entry.DB != 0 || g.entry.Y != 0 || g.entry.P&0x20 == 0 || uint8(g.entry.A) != w.Value {
				g.err = fmt.Errorf("sprite runtime writer context mismatch")
				return
			}
			g.pending = true
		}
	}
	s.CPU.AfterExecute = func() {
		if after != nil {
			after()
		}
		if !g.pending || g.err != nil {
			return
		}
		g.pending = false
		dev := s.Bus.GetPage(0x7e, report.Link.Canonical>>8)
		if dev == nil || s.Bus.GetPage(w.Addr>>16, (w.Addr>>8)&255) != dev || dev.Read(0x7e0000|report.Link.Canonical) != w.Value {
			g.err = fmt.Errorf("sprite WRAM alias or value mismatch")
			return
		}
		g.report.WriterMatched = true
		g.report.RuntimeEntry = g.entry
		g.report.CompletionCycle = s.CPU.Cycles
		if edit {
			dev.Write(0x7e0000|report.Link.Canonical, report.After)
			g.report.Applied = true
		}
	}
	s.Bus.ReadHook = func(a uint32, v uint8) {
		if read != nil {
			read(a, v)
		}
		if g.err != nil {
			return
		}
		r := report.Link.Read
		if s.CPU.Cycles == r.Cycle && a == r.Addr {
			want := report.Before
			if edit {
				want = report.After
			}
			state := s.DMA.SaveState().Execution
			if int(s.PPU.FrameCount) != r.PPUFrame || !g.report.WriterMatched || v != want || !s.DMA.Busy() || !state.General || state.Channel != report.Link.Channel {
				g.err = fmt.Errorf("sprite DMA consumption mismatch")
				return
			}
			g.report.DMAReadMatched = true
		}
	}
	s.Bus.WriteHook = func(previous func(uint32, uint8)) func(uint32, uint8) {
		return func(a uint32, v uint8) {
			previous(a, v)
			if g.err != nil {
				return
			}
			r := report.Link.RegisterWrite
			if s.CPU.Cycles == r.Cycle && a == r.Addr {
				want := report.Before
				if edit {
					want = report.After
				}
				if int(s.PPU.FrameCount) != r.PPUFrame || !g.report.DMAReadMatched || v != want || !s.DMA.Busy() {
					g.err = fmt.Errorf("sprite DMA register mismatch")
					return
				}
				g.report.RegisterWriteMatched = true
			}
		}
	}(s.Bus.WriteHook)
	s.PPU.WriteHook = func(e ppu.WriteEvent) {
		if ppuWrite != nil {
			ppuWrite(e)
		}
		r := report.Link.OAMWrite
		if g.err != nil {
			return
		}
		if s.CPU.Cycles == r.Cycle && e.Space == "oam" && e.Addr == r.Addr {
			want := report.Before
			if edit {
				want = report.After
			}
			if int(s.PPU.FrameCount) != r.PPUFrame || !g.report.RegisterWriteMatched || e.After != want {
				g.err = fmt.Errorf("sprite physical OAM consumption mismatch")
				return
			}
			g.report.OAMWriteMatched = true
		}
	}
	return g
}
func (g *spriteGuard) complete() error {
	if g.err != nil {
		return g.err
	}
	if !g.report.WriterMatched || !g.report.DMAReadMatched || !g.report.RegisterWriteMatched || !g.report.OAMWriteMatched {
		return fmt.Errorf("sprite writer or consumption unavailable in branch")
	}
	return nil
}
