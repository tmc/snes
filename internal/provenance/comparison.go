package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/tmc/snes/internal/trace"
	"reflect"
)

// WriterCoverage names the local producer contract for ordinary LoROM captures.
// It does not establish coverage outside the recorded frame window.
const WriterCoverage = "all_cpu_dma_hdma_wram_and_wram_port"

// Identity binds observations to a machine run and its source material.
// Source and IR identities describe generated material, not captured proof.
type Identity struct {
	ROMSHA256      string `json:"rom_sha256"`
	StateSHA256    string `json:"checkpoint_sha256"`
	InputsSHA256   string `json:"inputs_sha256"`
	RunSHA256      string `json:"run_sha256"`
	Mode           string `json:"mode"`
	SourceSHA256   string `json:"source_sha256,omitempty"`
	IRSHA256       string `json:"ir_sha256,omitempty"`
	EditedIRSHA256 string `json:"edited_ir_sha256,omitempty"`
	PlanSHA256     string `json:"plan_sha256,omitempty"`
	EditSHA256     string `json:"edit_sha256,omitempty"`
}

// FrameIdentity records the completed machine frame surrounding observations.
type FrameIdentity struct {
	Frame       int    `json:"relative_frame"`
	PPUFrame    uint64 `json:"ppu_frame"`
	StartCycle  uint64 `json:"start_cycle"`
	VBlankCycle uint64 `json:"vblank_cycle"`
	EndCycle    uint64 `json:"end_cycle"`
	StateSHA256 string `json:"state_sha256"`
	BusSHA256   string `json:"bus_sha256"`
	PixelSHA256 string `json:"pixel_sha256"`
}

// Window contains all bus accesses, physical PPU writes and transfer starts in
// [From, To). The producer must account for every WRAM writer, including ports.
// Initial memory versions and dependencies through CPU arithmetic remain unknown.
type Window struct {
	Schema   string          `json:"schema"`
	Identity Identity        `json:"identity"`
	From     int             `json:"from"`
	To       int             `json:"to"`
	Complete bool            `json:"complete"`
	Coverage string          `json:"writer_coverage"`
	Events   []Event         `json:"events"`
	Frames   []FrameIdentity `json:"frames"`
}

// WindowSHA256 returns the identity of canonical JSON for a capture window.
func WindowSHA256(w Window) (string, error) {
	b, err := json.Marshal(w)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// Selection identifies the source interval, host frame and OAM entry to inspect.
// RoutineEnd is exclusive. Sprite is an OAM entry, not a rendered object owner.
type Selection struct {
	Frame        int    `json:"frame"`
	RoutineStart uint32 `json:"routine_start"`
	RoutineEnd   uint32 `json:"routine_end"`
	Sprite       int    `json:"sprite"`
}

// Observation describes one branch without inferring transitive dependencies.
type Observation struct {
	Identity               Identity          `json:"identity"`
	SourceWrites           []Event           `json:"source_instruction_writes"`
	SourceConsumed         []Link            `json:"direct_source_dma_oam_links"`
	UnconsumedSourceWrites []Event           `json:"source_writes_without_supported_consumer"`
	OAMLinks               []Link            `json:"observed_dma_oam_links"`
	Sprite                 SpriteExplanation `json:"selected_sprite"`
	Unknown                []string          `json:"unknown"`
}

// WriteDifference compares corresponding versions by frame, PC, address and
// ordinal. A missing version is retained; correspondence is not a causal link.
type WriteDifference struct {
	Original *Event `json:"original,omitempty"`
	Edited   *Event `json:"edited,omitempty"`
}

// FrameDifference compares actual completed frames without pixel attribution.
type FrameDifference struct {
	Original      FrameIdentity `json:"original"`
	Edited        FrameIdentity `json:"edited"`
	PixelsChanged bool          `json:"pixels_changed"`
	BusChanged    bool          `json:"bus_changed"`
	StateChanged  bool          `json:"state_changed"`
}

// Comparison separates observed source writes, PPU consumption and frame deltas.
// It never grants captured proof or connects arithmetic dependencies by inference.
type Comparison struct {
	PPURegisterDifferences []WriteDifference `json:"ppu_register_write_differences"`
	Schema                 string            `json:"schema"`
	OriginalSHA256         string            `json:"original_window_sha256"`
	EditedSHA256           string            `json:"edited_window_sha256"`
	Selection              Selection         `json:"selection"`
	Original               Observation       `json:"original"`
	Edited                 Observation       `json:"edited"`
	SourceWriteDifferences []WriteDifference `json:"source_write_differences"`
	PPUWriteDifferences    []WriteDifference `json:"physical_ppu_write_differences"`
	Frames                 []FrameDifference `json:"frames"`
	Dependency             string            `json:"source_to_render_dependency"`
	CapturedProofEligible  bool              `json:"captured_proof_eligible"`
	Limitations            []string          `json:"limitations"`
}

func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func validateWindow(w Window, pin string) error {
	got, err := WindowSHA256(w)
	if err != nil {
		return err
	}
	if !validSHA(pin) || got != pin {
		return fmt.Errorf("window identity mismatch")
	}
	if w.Schema != "snes-observation-window-v1" || !w.Complete || w.Coverage != WriterCoverage || w.From < 0 || w.To <= w.From || w.To-w.From > 16 || len(w.Frames) != w.To-w.From || len(w.Events) > 2000000 {
		return fmt.Errorf("unsupported or partial observation coverage")
	}
	for _, s := range []string{w.Identity.ROMSHA256, w.Identity.StateSHA256, w.Identity.InputsSHA256, w.Identity.RunSHA256} {
		if !validSHA(s) {
			return fmt.Errorf("invalid observation identity")
		}
	}
	if w.Identity.Mode == "recovered_c" {
		for _, s := range []string{w.Identity.SourceSHA256, w.Identity.IRSHA256, w.Identity.EditedIRSHA256, w.Identity.PlanSHA256, w.Identity.EditSHA256} {
			if !validSHA(s) {
				return fmt.Errorf("missing recovered source identity")
			}
		}
	}
	switch w.Identity.Mode {
	case "original_interpreter", "generated_c", "recovered_c", "sprite_data":
	default:
		return fmt.Errorf("unsupported observation mode")
	}
	for i, f := range w.Frames {
		if f.Frame != w.From+i || f.StartCycle >= f.VBlankCycle || f.EndCycle < f.VBlankCycle || !validSHA(f.StateSHA256) || !validSHA(f.BusSHA256) || !validSHA(f.PixelSHA256) || i > 0 && f.PPUFrame != w.Frames[i-1].PPUFrame+1 {
			return fmt.Errorf("invalid observation frame")
		}
	}
	for i, e := range w.Events {
		if e.ID != uint64(i) || e.Frame < w.From || e.Frame >= w.To || i > 0 && (e.Frame < w.Events[i-1].Frame || e.Cycle < w.Events[i-1].Cycle) {
			return fmt.Errorf("invalid observation event order")
		}
		f := w.Frames[e.Frame-w.From]
		if e.Cycle < f.StartCycle || e.Cycle > f.EndCycle || e.PPUFrame < int(f.PPUFrame) || uint64(e.PPUFrame)-f.PPUFrame > 1 {
			return fmt.Errorf("observation event outside declared frame")
		}
	}
	return nil
}

func observe(w Window, s Selection) (Observation, error) {
	result, err := Analyze(w.Events, s.Frame, s.RoutineStart, s.RoutineEnd)
	if err != nil {
		return Observation{}, err
	}
	for i := range result.Links {
		l := &result.Links[i]
		if l.Writer != nil && l.Writer.Actor != "cpu" && l.Writer.Actor != "dma_or_hdma" {
			l.Writer = nil
			l.Status = "unknown"
			l.Routine = false
			result.Unknown = append(result.Unknown, "WRAM writer actor unavailable")
		}
	}
	sprite, err := ExplainSprite(w.Events, s.Sprite, s.Frame, s.RoutineStart, s.RoutineEnd)
	if err != nil {
		return Observation{}, err
	}
	for i := range sprite.Bytes {
		l := sprite.Bytes[i].Link
		if l != nil && l.Writer != nil && l.Writer.Actor != "cpu" && l.Writer.Actor != "dma_or_hdma" {
			l.Writer = nil
			l.Status = "unknown"
			l.Routine = false
			sprite.Bytes[i].Status = "unknown"
		}
	}
	out := Observation{Identity: w.Identity, SourceWrites: result.RoutineWrites, UnconsumedSourceWrites: result.UnconsumedRoutineWrites, OAMLinks: result.Links, Sprite: sprite, Unknown: result.Unknown}
	for _, l := range result.Links {
		if l.Routine && l.Writer != nil && l.Writer.Actor == "cpu" {
			out.SourceConsumed = append(out.SourceConsumed, l)
		}
	}
	out.Unknown = append(out.Unknown, "byte versions preceding the window are unknown", "CPU arithmetic and transitive dataflow from source writes to rendering are not tracked")
	return out, nil
}

type writeKey struct {
	frame    int
	pc, addr uint32
	space    string
	ordinal  int
}

func differingWrites(a, b []Event, physical bool) []WriteDifference {
	collect := func(events []Event) ([]writeKey, map[writeKey]Event) {
		counts := map[writeKey]int{}
		m := map[writeKey]Event{}
		var order []writeKey
		for _, e := range events {
			k := writeKey{frame: e.PPUFrame, pc: e.PC, addr: e.Addr, space: e.Space}
			if physical {
				k.pc = 0
			}
			k.ordinal = counts[k]
			base := k
			base.ordinal = 0
			counts[base]++
			m[k] = e
			order = append(order, k)
		}
		return order, m
	}
	order, am := collect(a)
	other, bm := collect(b)
	var out []WriteDifference
	add := func(k writeKey) {
		x, xok := am[k]
		y, yok := bm[k]
		if xok && yok && x.Value == y.Value {
			return
		}
		d := WriteDifference{}
		if xok {
			v := x
			d.Original = &v
		}
		if yok {
			v := y
			d.Edited = &v
		}
		out = append(out, d)
	}
	for _, k := range order {
		add(k)
	}
	for _, k := range other {
		if _, ok := am[k]; !ok {
			add(k)
		}
	}
	return out
}

// Compare checks both externally pinned complete windows before explaining them.
// Observed last-writer DMA/OAM links apply to exact byte versions only. A changed
// source write and changed pixels do not establish the missing transitive path.
func Compare(a, b Window, aSHA, bSHA string, s Selection) (Comparison, error) {
	var out Comparison
	if err := validateWindow(a, aSHA); err != nil {
		return out, fmt.Errorf("original: %w", err)
	}
	if err := validateWindow(b, bSHA); err != nil {
		return out, fmt.Errorf("edited: %w", err)
	}
	if a.From != b.From || a.To != b.To || a.Identity.ROMSHA256 != b.Identity.ROMSHA256 || a.Identity.StateSHA256 != b.Identity.StateSHA256 || a.Identity.InputsSHA256 != b.Identity.InputsSHA256 || s.Frame < a.From || s.Frame >= a.To || s.RoutineStart >= s.RoutineEnd || s.RoutineEnd > 1<<24 || s.Sprite < 0 || s.Sprite >= 128 {
		return out, fmt.Errorf("comparison inputs or selection differ")
	}
	original, err := observe(a, s)
	if err != nil {
		return out, err
	}
	edited, err := observe(b, s)
	if err != nil {
		return out, err
	}
	out = Comparison{Schema: "snes-effect-comparison-v1", OriginalSHA256: aSHA, EditedSHA256: bSHA, Selection: s, Original: original, Edited: edited, Dependency: "transitive source-to-render dataflow unavailable", Limitations: []string{"observed DMA/OAM byte-version links do not establish visible pixel ownership", "low-OAM latch origins, unsupported DMA shapes and HDMA consumption remain unknown", "changed physical PPU writes and pixels are observations, not attribution to the source instruction", "window pins bind local producer bytes; they are not authentication or captured proof"}}
	out.SourceWriteDifferences = differingWrites(original.SourceWrites, edited.SourceWrites, false)
	ppuEvents := func(w Window) []Event {
		var out []Event
		for _, e := range w.Events {
			if e.Kind == "ppu" && e.Op == "write" {
				out = append(out, e)
			}
		}
		return out
	}
	out.PPUWriteDifferences = differingWrites(ppuEvents(a), ppuEvents(b), true)
	registerEvents := func(w Window) []Event {
		var out []Event
		for _, e := range w.Events {
			space, addr := trace.CPUSpace(e.Addr)
			if e.Kind == "bus" && e.Op == "write" && space == "ppu" && addr >= 0x2100 && addr <= 0x213f {
				out = append(out, e)
			}
		}
		return out
	}
	out.PPURegisterDifferences = differingWrites(registerEvents(a), registerEvents(b), false)
	for i, f := range a.Frames {
		g := b.Frames[i]
		if f.PPUFrame != g.PPUFrame || f.StartCycle != g.StartCycle || f.VBlankCycle != g.VBlankCycle {
			return Comparison{}, fmt.Errorf("PPU observation boundaries differ")
		}
		out.Frames = append(out.Frames, FrameDifference{Original: f, Edited: g, PixelsChanged: f.PixelSHA256 != g.PixelSHA256, BusChanged: f.BusSHA256 != g.BusSHA256, StateChanged: f.StateSHA256 != g.StateSHA256})
	}
	// Structural equality is useful for no-edit controls; no source proof follows.
	if reflect.DeepEqual(a.Events, b.Events) && reflect.DeepEqual(a.Frames, b.Frames) {
		out.Dependency = "identical observed events and frames; transitive dataflow unavailable"
	}
	return out, nil
}
