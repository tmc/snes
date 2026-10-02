package machinebranch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/ppu"
)

// ErrGeneratedCBridge reports that architectural C replay cannot advance devices.
var ErrGeneratedCBridge = errors.New("generated C replacement requires a scheduler-aware CPU execution bridge")

// Machine is the complete-state frame adapter. The runtime owns cycle and device
// scheduling; a compiled C runner is not a Machine without an execution bridge.
type Machine interface {
	Serialize() ([]byte, error)
	RunFrame() error
	FrameBuffer() []uint16
	StateHashes() (map[string]string, error)
	SetInputState(uint, uint16) error
}

// Input changes one controller at the start of a relative frame. Changes must be
// ordered by frame then port. Unspecified controllers retain checkpoint state.
type Input struct {
	Frame   int    `json:"frame"`
	Port    uint   `json:"port"`
	Buttons uint16 `json:"buttons"`
}

// Config pins the original ROM and complete state. Frames is between 1 and 600.
// Mode is empty, original_interpreter, generated_c, recovered_c, or sprite_data. Generated C requires
// a nonzero Addend and the exact supported rotation ROM vocabulary.
type Config struct {
	Observation *ObservationConfig `json:"observation,omitempty"`
	ROMPath     string             `json:"rom_path"`
	ROMSHA256   string             `json:"rom_sha256"`
	StatePath   string             `json:"state_path"`
	StateSHA256 string             `json:"state_sha256"`
	Frames      int                `json:"frames"`
	Inputs      []Input            `json:"inputs"`
	Mode        string             `json:"mode,omitempty"`
	SpriteEdit  *SpriteEdit        `json:"sprite_edit,omitempty"`
	Addend      uint8              `json:"addend,omitempty"`
	Recovered   *RecoveredConfig   `json:"recovered,omitempty"`
}

// Frame records one completed runtime frame. Pixels are owned BGR555 values;
// the CLI publishes them as PNG separately from the JSON metadata.
type Frame struct {
	BusSHA256         string            `json:"physical_bus_sha256"`
	BusEvents         uint64            `json:"physical_bus_events"`
	FirstRenderedLine int               `json:"first_rendered_line"`
	Hires             bool              `json:"hires"`
	PseudoHires       bool              `json:"pseudo_hires"`
	RelativeFrame     int               `json:"relative_frame"`
	PPUFrame          uint64            `json:"ppu_frame"`
	StartCycle        uint64            `json:"frame_start_cycle"`
	VBlankCycle       uint64            `json:"frame_vblank_cycle"`
	EndCycle          uint64            `json:"host_frame_end_cycle"`
	Width             int               `json:"width"`
	Height            int               `json:"height"`
	FramebufferSHA256 string            `json:"framebuffer_sha256"`
	StateSHA256       string            `json:"serialized_state_sha256"`
	Components        map[string]string `json:"component_hashes"`
	Pixels            []uint16          `json:"-"`
	PNGPath           string            `json:"png_path,omitempty"`
	PNGSHA256         string            `json:"png_sha256,omitempty"`
}

// Branch records the initial state roundtrip and resulting frames.
type Branch struct {
	Name               string  `json:"name"`
	InitialStateSHA256 string  `json:"initial_state_sha256"`
	Frames             []Frame `json:"frames"`
}

// Result separates repeatability from recovery qualification. This baseline
// records compiled execution separately and grants no captured proof to either branch.
type Result struct {
	Observations          *Observations     `json:"observations,omitempty"`
	Checkpoint            []byte            `json:"-"`
	Schema                string            `json:"schema"`
	Config                Config            `json:"config"`
	InputsSHA256          string            `json:"inputs_sha256"`
	Mode                  string            `json:"mode"`
	ReplacementExecuted   bool              `json:"replacement_executed"`
	CapturedProofEligible bool              `json:"captured_proof_eligible"`
	OriginalMatch         bool              `json:"original_match"`
	Baseline              Branch            `json:"baseline"`
	Replica               Branch            `json:"replica"`
	Sprite                *SpriteExperiment `json:"sprite_experiment,omitempty"`
	SpriteBaseline        *SpriteExperiment `json:"sprite_baseline,omitempty"`
	Compiled              *Compiled         `json:"compiled,omitempty"`
	Limitations           []string          `json:"limitations"`
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func readPinned(path, want string, limit int64) ([]byte, error) {
	h, err := hex.DecodeString(want)
	if err != nil || len(h) != sha256.Size {
		return nil, fmt.Errorf("invalid SHA-256 identity")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || int64(len(b)) > limit {
		return nil, fmt.Errorf("input size outside bound")
	}
	if digest(b) != want {
		return nil, fmt.Errorf("input SHA-256 mismatch")
	}
	return b, nil
}

func validate(c Config) error {
	if c.Observation != nil {
		if err := c.Observation.validate(c.Frames); err != nil {
			return err
		}
		if c.Mode == "sprite_data" && c.SpriteEdit != nil && c.SpriteEdit.Large != nil {
			return fmt.Errorf("observation coverage excludes direct sprite data intervention")
		}
	}
	if c.Mode == "generated_c" && c.Addend == 0 {
		return ErrGeneratedCBridge
	}
	if c.Mode != "" && c.Mode != "original_interpreter" && c.Mode != "generated_c" && c.Mode != "recovered_c" && c.Mode != "sprite_data" {
		return fmt.Errorf("unsupported execution mode %q", c.Mode)
	}
	if c.Mode != "generated_c" && c.Mode != "recovered_c" && c.Addend != 0 {
		return fmt.Errorf("addend requires generated_c or recovered_c mode")
	}
	if c.Mode == "recovered_c" {
		if c.Recovered == nil {
			return fmt.Errorf("missing recovered source identity")
		}
		if c.Addend != 5 && c.Addend != 6 {
			return fmt.Errorf("recovered addend must be 5 or 6")
		}
		if err := c.Recovered.validate(); err != nil {
			return err
		}
	} else if c.Recovered != nil {
		return fmt.Errorf("recovered identity requires recovered_c mode")
	}
	if c.Mode == "sprite_data" && c.SpriteEdit == nil {
		return fmt.Errorf("missing sprite edit configuration")
	}
	if c.Mode != "sprite_data" && c.SpriteEdit != nil {
		return fmt.Errorf("sprite edit requires sprite_data mode")
	}
	if c.Frames < 1 || c.Frames > 600 {
		return fmt.Errorf("frames must be between 1 and 600")
	}
	if len(c.Inputs) > c.Frames*2 {
		return fmt.Errorf("too many input changes")
	}
	for i, in := range c.Inputs {
		if in.Frame < 0 || in.Frame >= c.Frames || in.Port > 1 {
			return fmt.Errorf("invalid input change")
		}
		if i > 0 {
			prev := c.Inputs[i-1]
			if in.Frame < prev.Frame || (in.Frame == prev.Frame && in.Port <= prev.Port) {
				return fmt.Errorf("input changes must be ordered and unique")
			}
		}
	}
	return nil
}

func restore(rom, state []byte) (*snes.System, string, error) {
	s := snes.NewSystem(nil)
	if err := s.LoadROM(append([]byte(nil), rom...)); err != nil {
		return nil, "", err
	}
	if err := s.Unserialize(append([]byte(nil), state...)); err != nil {
		return nil, "", err
	}
	if s.FrameSkip() != 0 || s.RunAhead() {
		return nil, "", fmt.Errorf("frame skip and run-ahead are unsupported")
	}
	b, err := s.Serialize()
	if err != nil {
		return nil, "", err
	}
	if digest(b) != digest(state) {
		return nil, "", fmt.Errorf("complete state does not roundtrip exactly")
	}
	return s, digest(b), nil
}

type frameCapture struct {
	frame *Frame
	count int
}

func captureFrames(s *snes.System) *frameCapture {
	c := new(frameCapture)
	s.PPU.FrameHook = func(info *ppu.FrameInfo) {
		pixels := s.PPU.CopyFrame(nil, info)
		width := 256
		if info.AnyHires() {
			width = 512
		}
		c.frame = &Frame{PPUFrame: uint64(info.Number), Width: width, Height: info.Height, Pixels: pixels, StartCycle: info.Start, VBlankCycle: info.VBlank, FirstRenderedLine: info.FirstLine, Hires: info.AnyHires(), PseudoHires: info.PseudoHires}
		c.count++
	}
	return c
}
func sample(s *snes.System, relative int, c *frameCapture) (Frame, error) {
	if c.count != 1 || c.frame == nil {
		return Frame{}, fmt.Errorf("host frame must produce exactly one completed PPU frame, got %d", c.count)
	}
	frame := *c.frame
	c.frame = nil
	c.count = 0
	frame.RelativeFrame = relative
	if len(frame.Pixels) != frame.Width*frame.Height {
		return Frame{}, fmt.Errorf("invalid frame geometry")
	}
	data := make([]byte, len(frame.Pixels)*2)
	for i, p := range frame.Pixels {
		data[i*2] = byte(p)
		data[i*2+1] = byte(p >> 8)
	}
	frame.FramebufferSHA256 = digest(data)
	if s.CPU.Fault != nil {
		return Frame{}, fmt.Errorf("CPU fault: %w", s.CPU.Fault)
	}
	state, err := s.Serialize()
	if err != nil {
		return Frame{}, err
	}
	frame.StateSHA256 = digest(state)
	hashes, err := s.StateHashes()
	if err != nil {
		return Frame{}, err
	}
	frame.Components = hashes
	frame.EndCycle = s.CPU.Cycles
	return frame, nil
}

// Run restores two independent machines and repeats the same input schedule.
// It returns no partial result when restoration, input, execution or capture fails.
func Run(ctx context.Context, c Config) (*Result, error) {
	if c.Observation != nil {
		owned := *c.Observation
		c.Observation = &owned
	}
	if ctx == nil {
		return nil, fmt.Errorf("missing context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Recovered != nil {
		owned := *c.Recovered
		c.Recovered = &owned
	}
	if err := validate(c); err != nil {
		return nil, err
	}
	c.Inputs = append([]Input(nil), c.Inputs...)
	if c.SpriteEdit != nil {
		owned := *c.SpriteEdit
		if owned.Large != nil {
			value := *owned.Large
			owned.Large = &value
		}
		c.SpriteEdit = &owned
	}
	rom, err := readPinned(c.ROMPath, c.ROMSHA256, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("read ROM: %w", err)
	}
	state, err := readPinned(c.StatePath, c.StateSHA256, 64<<20)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}
	a, ah, err := restore(rom, state)
	if err != nil {
		return nil, fmt.Errorf("restore baseline: %w", err)
	}
	b, bh, err := restore(rom, state)
	if err != nil {
		return nil, fmt.Errorf("restore replica: %w", err)
	}
	r := &Result{Checkpoint: append([]byte(nil), state...), Schema: "snes-machine-branch-v1", Config: c, Mode: "original_interpreter", OriginalMatch: true, Baseline: Branch{Name: "baseline", InitialStateSHA256: ah}, Replica: Branch{Name: "replica", InitialStateSHA256: bh}, Limitations: []string{"original interpreter repeated; generated C replacement is unavailable", "repeatability is same-runtime evidence, not an independent hardware oracle", "no edited frame or captured recovery qualification is claimed", "rendering follows current runtime; pseudo-hires and restored pre-capture hires metadata remain qualified"}}
	if c.Mode == "generated_c" {
		session, err := startCompiled(ctx, rom, c.Addend)
		if err != nil {
			return nil, err
		}
		defer session.close()
		b.CPU.ReplaceInstruction = session.selectInstruction
		r.Mode = "generated_c"
		r.Compiled = session.report
		r.Limitations = []string{"narrow template-emitted C executes selected rotation instructions through runtime timing", "edited execution has no captured recovery proof", "same runtime devices; no independent hardware equivalence claim"}
	}
	if c.Mode == "recovered_c" {
		session, err := startRecovered(ctx, rom, c.Addend, *c.Recovered)
		if err != nil {
			return nil, err
		}
		defer session.close()
		b.CPU.ReplaceInstruction = session.selectInstruction
		r.Mode = "recovered_c"
		r.Compiled = session.report
		r.Limitations = []string{"automatically lifted machine IR C executes selected rotation instructions with a reviewed timed bus plan", "whole-machine comparison is same-runtime experimental evidence, not captured recovery qualification", "edited execution is counterfactual and never captured-proof eligible", "timing plan supports only the pinned native 8-bit low-WRAM rotation vocabulary"}
	}
	schedule := make([]byte, 0, len(c.Inputs)*12)
	for _, in := range c.Inputs {
		schedule = fmt.Appendf(schedule, "%d:%d:%d\n", in.Frame, in.Port, in.Buttons)
	}
	r.InputsSHA256 = digest(schedule)
	ac, bc := captureFrames(a), captureFrames(b)
	aj, bj := journal(a), journal(b)
	var ag, bg *spriteGuard
	if c.Mode == "sprite_data" {
		base, err := prepareSprite(*c.SpriteEdit, rom, c.ROMSHA256)
		if err != nil {
			return nil, err
		}
		edited, err := prepareSprite(*c.SpriteEdit, rom, c.ROMSHA256)
		if err != nil {
			return nil, err
		}
		r.Mode = "sprite_data"
		r.SpriteBaseline = base
		r.Sprite = edited
		r.Limitations = []string{"experimental WRAM size-bit substitution at observed instruction completion; not C source execution", "capture link proves observed DMA/OAM consumption, not visible pixel ownership", "runtime writer context is observed now, not recovered from capture", "no captured recovery eligibility is granted"}
		ag = attachSprite(a, base, false)
		bg = attachSprite(b, edited, c.SpriteEdit.Large != nil)
	}
	var ao, bo *observer
	if c.Observation != nil {
		ao, err = attachObserver(a, *c.Observation, state)
		if err != nil {
			return nil, err
		}
		bo, err = attachObserver(b, *c.Observation, state)
		if err != nil {
			return nil, err
		}
	}
	next := 0
	for frame := 0; frame < c.Frames; frame++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for next < len(c.Inputs) && c.Inputs[next].Frame == frame {
			in := c.Inputs[next]
			for _, m := range []Machine{a, b} {
				if err := m.SetInputState(in.Port, in.Buttons); err != nil {
					return nil, fmt.Errorf("input frame %d: %w", frame, err)
				}
			}
			next++
		}
		if ao != nil {
			ao.frame = frame
			bo.frame = frame
		}
		if err := a.RunFrame(); err != nil {
			return nil, fmt.Errorf("baseline frame %d: %w", frame, err)
		}
		if err := b.RunFrame(); err != nil {
			return nil, fmt.Errorf("replica frame %d: %w", frame, err)
		}
		af, err := sample(a, frame, ac)
		if err != nil {
			return nil, err
		}
		bf, err := sample(b, frame, bc)
		if err != nil {
			return nil, err
		}
		af.BusSHA256, af.BusEvents = aj.finish()
		bf.BusSHA256, bf.BusEvents = bj.finish()
		if ao != nil {
			ao.sample(af)
			bo.sample(bf)
			if ao.overflow || bo.overflow {
				return nil, fmt.Errorf("observation event budget exhausted; partial window refused")
			}
		}
		if !sameFrame(af, bf) {
			r.OriginalMatch = false
		}
		r.Baseline.Frames = append(r.Baseline.Frames, af)
		r.Replica.Frames = append(r.Replica.Frames, bf)
	}
	if ag != nil {
		if err := ag.complete(); err != nil {
			return nil, err
		}
		if err := bg.complete(); err != nil {
			return nil, err
		}
	}
	if r.Compiled != nil {
		r.ReplacementExecuted = r.Compiled.Instructions > 0
	}
	if ao != nil {
		if err := finishObservations(ao, bo, c, r, rom); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// sameFrame compares runtime evidence before the CLI adds artifact paths.
func sameFrame(a, b Frame) bool {
	return a.RelativeFrame == b.RelativeFrame && a.PPUFrame == b.PPUFrame &&
		a.StartCycle == b.StartCycle && a.VBlankCycle == b.VBlankCycle && a.EndCycle == b.EndCycle &&
		a.Width == b.Width && a.Height == b.Height && a.FirstRenderedLine == b.FirstRenderedLine &&
		a.Hires == b.Hires && a.PseudoHires == b.PseudoHires &&
		a.BusSHA256 == b.BusSHA256 && a.BusEvents == b.BusEvents &&
		a.StateSHA256 == b.StateSHA256 && a.FramebufferSHA256 == b.FramebufferSHA256 &&
		reflect.DeepEqual(a.Components, b.Components)
}
