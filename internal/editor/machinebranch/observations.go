package machinebranch

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/dma"
	"github.com/tmc/snes/internal/ppu"
	"github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/trace"
)

// ObservationConfig selects an inclusive/exclusive relative host-frame window.
// At most 16 frames and two million events per branch can be recorded. A full
// event budget refuses the run, rather than publishing partial writer coverage.
type ObservationConfig struct {
	From      int                  `json:"from"`
	To        int                  `json:"to"`
	MaxEvents int                  `json:"max_events"`
	Selection provenance.Selection `json:"selection"`
}

// Observations retains raw branch windows and their independently checked report.
// Events describe runtime observations, not captured recovery proof.
type Observations struct {
	Original provenance.Window     `json:"original_window"`
	Edited   provenance.Window     `json:"edited_window"`
	Report   provenance.Comparison `json:"report"`
}

func (c ObservationConfig) validate(frames int) error {
	if c.From < 0 || c.To <= c.From || c.To > frames || c.To-c.From > 16 || c.MaxEvents < 1 || c.MaxEvents > 2000000 || c.Selection.Frame < c.From || c.Selection.Frame >= c.To || c.Selection.RoutineStart >= c.Selection.RoutineEnd || c.Selection.RoutineEnd > 1<<24 || c.Selection.Sprite < 0 || c.Selection.Sprite >= 128 {
		return fmt.Errorf("invalid observation window or selection")
	}
	return nil
}

type observer struct {
	window   provenance.Window
	config   ObservationConfig
	frame    int
	port     uint32
	overflow bool
}

func attachObserver(s *snes.System, c ObservationConfig, state []byte) (*observer, error) {
	mapper, ok := s.ROMProvenance()
	if !ok || mapper != "lorom" {
		return nil, fmt.Errorf("observations require ordinary LoROM")
	}
	// Gob field matching extracts the checkpoint's physical WRAM port pointer.
	// The complete checkpoint has already passed restore and roundtrip checks.
	var initial struct {
		Version  uint32
		WRAMAddr uint32
		Cheats   []snes.Cheat
		RunAhead bool
	}
	if err := gob.NewDecoder(bytes.NewReader(state)).Decode(&initial); err != nil {
		return nil, err
	}
	if initial.Version != 3 || initial.WRAMAddr > 0x1ffff || len(initial.Cheats) != 0 || initial.RunAhead {
		return nil, fmt.Errorf("unsupported observation checkpoint")
	}
	o := &observer{config: c, frame: -1, port: initial.WRAMAddr, window: provenance.Window{Schema: "snes-observation-window-v1", From: c.From, To: c.To, Coverage: provenance.WriterCoverage}}
	emit := func(e provenance.Event) {
		if o.frame < c.From || o.frame >= c.To {
			return
		}
		if len(o.window.Events) >= c.MaxEvents {
			o.overflow = true
			return
		}
		e.ID = uint64(len(o.window.Events))
		e.Frame = o.frame
		e.PPUFrame = s.PPU.FrameCount
		e.Cycle = s.CPU.Cycles
		e.PC = uint32(s.CPU.LastOpcodePB)<<16 | uint32(s.CPU.LastOpcodePC)
		if e.Kind == "bus" || e.Kind == "wram_port" {
			e.Actor = "cpu"
			if s.DMA.Busy() {
				e.Actor = "dma_or_hdma"
				e.Channel = s.DMA.SaveState().Execution.Channel
			}
		}
		o.window.Events = append(o.window.Events, e)
	}
	port := func(addr uint32, op string, v uint8) {
		space, a := trace.CPUSpace(addr)
		if space != "ppu" {
			return
		}
		switch a {
		case 0x2180:
			emit(provenance.Event{Kind: "wram_port", Op: op, Addr: o.port, Value: v})
			o.port = (o.port + 1) & 0x1ffff
		case 0x2181:
			if op == "write" {
				o.port = o.port&0x1ff00 | uint32(v)
			}
		case 0x2182:
			if op == "write" {
				o.port = o.port&0x100ff | uint32(v)<<8
			}
		case 0x2183:
			if op == "write" {
				o.port = o.port&0xffff | uint32(v&1)<<16
			}
		}
	}
	read, write, ppuwrite := s.Bus.ReadHook, s.Bus.WriteHook, s.PPU.WriteHook
	s.Bus.ReadHook = func(a uint32, v uint8) {
		if read != nil {
			read(a, v)
		}
		emit(provenance.Event{Kind: "bus", Op: "read", Addr: a, Value: v})
		port(a, "read", v)
	}
	s.Bus.WriteHook = func(a uint32, v uint8) {
		if write != nil {
			write(a, v)
		}
		emit(provenance.Event{Kind: "bus", Op: "write", Addr: a, Value: v})
		port(a, "write", v)
	}
	s.PPU.WriteHook = func(e ppu.WriteEvent) {
		if ppuwrite != nil {
			ppuwrite(e)
		}
		emit(provenance.Event{Kind: "ppu", Op: "write", Space: e.Space, Addr: e.Addr, Value: e.After})
	}
	transfer := func(kind string, e dma.TransferTrace) {
		emit(provenance.Event{Kind: kind, Channel: e.Channel, Mode: e.Control, Count: e.Count, Target: e.Target, Addr: uint32(e.SrcBank)<<16 | uint32(e.SrcAddr)})
	}
	dmaTrace, hdmaTrace := s.DMA.Trace, s.DMA.HDMATrace
	s.DMA.Trace = func(e dma.TransferTrace) {
		if dmaTrace != nil {
			dmaTrace(e)
		}
		transfer("dma", e)
	}
	s.DMA.HDMATrace = func(e dma.TransferTrace) {
		if hdmaTrace != nil {
			hdmaTrace(e)
		}
		transfer("hdma", e)
	}
	return o, nil
}

func (o *observer) sample(f Frame) {
	if f.RelativeFrame < o.config.From || f.RelativeFrame >= o.config.To {
		return
	}
	o.window.Frames = append(o.window.Frames, provenance.FrameIdentity{Frame: f.RelativeFrame, PPUFrame: f.PPUFrame, StartCycle: f.StartCycle, VBlankCycle: f.VBlankCycle, EndCycle: f.EndCycle, StateSHA256: f.StateSHA256, BusSHA256: f.BusSHA256, PixelSHA256: f.FramebufferSHA256})
}

func observationIdentity(c Config, r *Result, branch Branch, mode string, pins *RecoveredConfig) (provenance.Identity, error) {
	branch.Frames = append([]Frame(nil), branch.Frames...)
	for i := range branch.Frames {
		branch.Frames[i].PNGPath = ""
		branch.Frames[i].PNGSHA256 = ""
	}
	material, err := json.Marshal(struct {
		Config Config
		Branch Branch
	}{c, branch})
	if err != nil {
		return provenance.Identity{}, err
	}
	id := provenance.Identity{ROMSHA256: c.ROMSHA256, StateSHA256: c.StateSHA256, InputsSHA256: r.InputsSHA256, RunSHA256: digest(material), Mode: mode}
	if pins != nil {
		id.SourceSHA256 = pins.SourceSHA256
		id.IRSHA256 = pins.IRSHA256
		id.EditedIRSHA256 = pins.EditedIRSHA256
		id.PlanSHA256 = pins.PlanSHA256
		id.EditSHA256 = pins.EditSHA256
	}
	return id, nil
}

func finishObservations(a, b *observer, c Config, r *Result, rom []byte) error {
	if a.overflow || b.overflow {
		return fmt.Errorf("observation event budget exhausted; partial window refused")
	}
	var originalPins *RecoveredConfig
	if c.Mode == "recovered_c" {
		p, err := PrepareRecovered(rom, 5)
		if err != nil {
			return err
		}
		originalPins = &p
	}
	var err error
	a.window.Identity, err = observationIdentity(c, r, r.Baseline, "original_interpreter", originalPins)
	if err != nil {
		return err
	}
	b.window.Identity, err = observationIdentity(c, r, r.Replica, r.Mode, c.Recovered)
	if err != nil {
		return err
	}
	a.window.Complete = true
	b.window.Complete = true
	apin, err := provenance.WindowSHA256(a.window)
	if err != nil {
		return err
	}
	bpin, err := provenance.WindowSHA256(b.window)
	if err != nil {
		return err
	}
	report, err := provenance.Compare(a.window, b.window, apin, bpin, c.Observation.Selection)
	if err != nil {
		return err
	}
	r.Observations = &Observations{Original: a.window, Edited: b.window, Report: report}
	return nil
}

// CheckObservations verifies that raw windows and the report describe this result.
// ROM must be the caller's owned ROM bytes. It returns no recovery qualification.
func CheckObservations(r *Result, rom []byte) error {
	if r == nil || r.Config.Observation == nil || r.Observations == nil || digest(rom) != r.Config.ROMSHA256 {
		return fmt.Errorf("missing or stale machine observations")
	}
	c := r.Config
	o := r.Observations
	var originalPins *RecoveredConfig
	if c.Mode == "recovered_c" {
		pins, err := PrepareRecovered(rom, 5)
		if err != nil {
			return err
		}
		originalPins = &pins
	}
	a, err := observationIdentity(c, r, r.Baseline, "original_interpreter", originalPins)
	if err != nil {
		return err
	}
	b, err := observationIdentity(c, r, r.Replica, r.Mode, c.Recovered)
	if err != nil {
		return err
	}
	if o.Original.Identity != a || o.Edited.Identity != b || o.Original.From != c.Observation.From || o.Original.To != c.Observation.To || o.Edited.From != c.Observation.From || o.Edited.To != c.Observation.To || len(o.Original.Events) > c.Observation.MaxEvents || len(o.Edited.Events) > c.Observation.MaxEvents {
		return fmt.Errorf("observation run identity or coverage differs")
	}
	for _, pair := range []struct {
		window provenance.Window
		branch Branch
	}{{o.Original, r.Baseline}, {o.Edited, r.Replica}} {
		if len(pair.window.Frames) != c.Observation.To-c.Observation.From || len(pair.branch.Frames) != c.Frames {
			return fmt.Errorf("observation frame count differs")
		}
		for i, f := range pair.window.Frames {
			n := c.Observation.From + i
			g := pair.branch.Frames[n]
			if f.Frame != g.RelativeFrame || f.PPUFrame != g.PPUFrame || f.StartCycle != g.StartCycle || f.VBlankCycle != g.VBlankCycle || f.EndCycle != g.EndCycle || f.StateSHA256 != g.StateSHA256 || f.BusSHA256 != g.BusSHA256 || f.PixelSHA256 != g.FramebufferSHA256 {
				return fmt.Errorf("observation frame identity differs")
			}
		}
	}
	report, err := provenance.Compare(o.Original, o.Edited, o.Report.OriginalSHA256, o.Report.EditedSHA256, c.Observation.Selection)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(report, o.Report) {
		return fmt.Errorf("observation report differs from pinned windows")
	}
	return nil
}
