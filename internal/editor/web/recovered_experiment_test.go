package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/editor/machinebranch"
	"github.com/tmc/snes/internal/provenance"
	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

func recoveredFixture(t *testing.T) *Experiments {
	t.Helper()
	e := experimentFixture(t, machinebranch.Run)
	rom := make([]byte, 1<<20)
	copy(rom[0x6445b:], []byte{0xee, 1, 0x1e, 0xad, 1, 0x1e, 0xc9, 0x40, 0xd0, 3, 0xee, 0, 0x1e, 0xad, 5, 0x1f, 0x18, 0x69, 5, 0x8d, 5, 0x1f, 0xad, 4, 0x1f, 0x18, 0x69, 3, 0x8d, 4, 0x1f, 0x60})
	e.rom = rom
	e.config.ROMSHA256 = fmt.Sprintf("%x", sha256.Sum256(rom))
	e.config.Mode = "recovered_c"
	pins, err := machinebranch.PrepareRecovered(rom, 5)
	if err != nil {
		t.Fatal(err)
	}
	e.config.Recovered = &pins
	return e
}

func TestRecoveredExperimentOperatorPins(t *testing.T) {
	for _, kind := range []string{"valid", "source", "ir", "edited_ir", "plan", "edit", "mode", "rom"} {
		t.Run(kind, func(t *testing.T) {
			e := recoveredFixture(t)
			c := e.config
			switch kind {
			case "source":
				c.Recovered.SourceSHA256 = strings.Repeat("a", 64)
			case "ir":
				c.Recovered.IRSHA256 = strings.Repeat("a", 64)
			case "edited_ir":
				c.Recovered.EditedIRSHA256 = strings.Repeat("a", 64)
			case "plan":
				c.Recovered.PlanSHA256 = strings.Repeat("a", 64)
			case "edit":
				c.Recovered.EditSHA256 = strings.Repeat("a", 64)
			case "mode":
				c.Mode = "original_interpreter"
			case "rom":
				e.rom[0x6445b] = 0
			}
			os.WriteFile(c.ROMPath, e.rom, 0600)
			b, _ := json.Marshal(c)
			p := filepath.Join(e.out, "test-config.json")
			os.WriteFile(p, b, 0600)
			_, err := NewExperiments(p, fmt.Sprintf("%x", sha256.Sum256(b)), filepath.Join(e.out, "new"), machinebranch.Run)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("configuration %s: %v", kind, err)
			}
		})
	}
}

func TestRecoveredExperimentDerivesEditedPins(t *testing.T) {
	e := recoveredFixture(t)
	calls := 0
	e.run = func(_ context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
		calls++
		want, err := machinebranch.PrepareRecovered(e.rom, c.Addend)
		if err != nil || c.Recovered == nil || *c.Recovered != want {
			t.Error("derivative pins differ")
		}
		if c.Addend == 5 {
			return fakeRecoveredMachine(t, e.rom, c)
		}
		return nil, fmt.Errorf("deliberate runner refusal")
	}
	h := Handler(&Model{Experiments: e})
	for _, body := range []string{`{"addend":4}`, `{"addend":7}`, `{"addend":6,"mode":"generated_c"}`, `{"addend":6,"source":"C"}`} {
		r := httptest.NewRequest("POST", "/api/experiment", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 202 {
			t.Fatal("unsupported request accepted")
		}
	}
	j, err := e.start(6)
	if err != nil {
		t.Fatal(err)
	}
	j = waitExperiment(t, e, j.ID)
	if calls != 2 || j.Status != "refused" || j.Original != nil || j.Edited != nil {
		t.Fatalf("failure published: %+v", j)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/job/frame?id="+j.ID+"&branch=edited&index=0", nil))
	if w.Code != 404 {
		t.Fatal("failed frame published")
	}
}

// The retained gate also exercises the production compiler and pixel publication.
func TestRetainedRecoveredResultRefusals(t *testing.T) {
	dir := os.Getenv("SNES_RECOVERED_RECEIPTS")
	if dir == "" {
		t.Skip("retained recovered runtime receipts not selected")
	}
	b, err := os.ReadFile(filepath.Join(dir, "baseline", "result.json"))
	if err != nil {
		t.Fatal(err)
	}

	originalIR, err := os.ReadFile(filepath.Join(dir, "baseline", "original-ir.json"))
	if err != nil {
		t.Fatal(err)
	}
	editedIR, err := os.ReadFile(filepath.Join(dir, "baseline", "edited-ir.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"source", "ir", "edited_ir", "plan", "edit", "origin", "mode", "rom", "config", "clock", "inputs", "proof", "agreement"} {
		t.Run(kind, func(t *testing.T) {
			var e struct {
				Result machinebranch.Result `json:"result"`
			}
			json.Unmarshal(b, &e)
			r := &e.Result
			c := r.Config
			r.Compiled.OriginalIRJSON = originalIR
			r.Compiled.EditedIRJSON = editedIR
			for _, branch := range []*machinebranch.Branch{&r.Baseline, &r.Replica} {
				for i := range branch.Frames {
					f := &branch.Frames[i]
					file, err := os.Open(filepath.Join(dir, "baseline", f.PNGPath))
					if err != nil {
						t.Fatal(err)
					}
					im, err := png.Decode(file)
					file.Close()
					if err != nil {
						t.Fatal(err)
					}
					f.Pixels = make([]uint16, f.Width*f.Height)
					for y := 0; y < f.Height; y++ {
						for x := 0; x < f.Width; x++ {
							red, green, blue, _ := im.At(x, y).RGBA()
							f.Pixels[y*f.Width+x] = uint16(red>>11) | uint16(green>>11)<<5 | uint16(blue>>11)<<10
						}
					}
					// The runtime validates raw frames before PNG publication.
					f.PNGPath = ""
					f.PNGSHA256 = ""
				}
			}
			if err := checkMachineResult(r, c); err != nil {
				t.Fatalf("positive retained result refused: %v", err)
			}
			switch kind {
			case "source":
				r.Compiled.Source += "changed"
			case "ir":
				r.Compiled.IRSHA256 = strings.Repeat("a", 64)
			case "edited_ir":
				r.Compiled.EditedIRSHA256 = strings.Repeat("a", 64)
			case "plan":
				r.Compiled.PlanSHA256 = strings.Repeat("a", 64)
			case "edit":
				r.Compiled.EditSHA256 = strings.Repeat("a", 64)
			case "origin":
				r.Compiled.SemanticsOrigin = "authored_rotation_template"
			case "mode":
				r.Mode = "generated_c"
			case "rom":
				r.Compiled.ROMSHA256 = strings.Repeat("a", 64)
			case "config":
				r.Config.Frames++
			case "clock":
				r.Replica.Frames[0].VBlankCycle++
			case "inputs":
				r.InputsSHA256 = strings.Repeat("a", 64)
			case "proof":
				r.CapturedProofEligible = true
			case "agreement":
				r.OriginalMatch = false
			}
			if err := checkMachineResult(r, c); err == nil {
				t.Fatal("stale recovered identity accepted")
			}
		})
	}
}

func fakeRecoveredMachine(t *testing.T, rom []byte, c machinebranch.Config) (*machinebranch.Result, error) {
	t.Helper()
	region, err := decomp.DecodeRegionFromBytes(rom[0x6445b:0x6447c], 0xcc45b, recovery.Context{E: "clear", M: "set", X: "set", C: "unknown"}, rom, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	plan := decomp.TimedPlan{Instructions: []decomp.TimedInstruction{
		{Opcode: 0xee, OperandBytes: 2, IdleBefore: 6}, {Opcode: 0xad, OperandBytes: 2}, {Opcode: 0xc9, OperandBytes: 1}, {Opcode: 0xd0, OperandBytes: 1, TakenIdle: 6}, {Opcode: 0x18, IdleBefore: 6}, {Opcode: 0x69, OperandBytes: 1}, {Opcode: 0x8d, OperandBytes: 2}, {Opcode: 0x60, IdleBefore: 12, IdleAfter: 6},
	}}
	var edit *decomp.TimedImmediateEdit
	if c.Addend == 6 {
		edit = &decomp.TimedImmediateEdit{Address: 0xcc46c, Expected: 5, Replacement: 6}
	}
	source, err := decomp.GenerateTimedRegionC(region, rom, plan, edit)
	if err != nil {
		t.Fatal(err)
	}
	r, err := fakeMachine(context.Background(), c)
	r.Mode = c.Mode
	r.OriginalMatch = c.Addend == 5
	r.InputsSHA256 = fmt.Sprintf("%x", sha256.Sum256(nil))
	v := r.Compiled
	v.Source = source.Source
	v.SourceSHA256 = source.SourceSHA256
	v.SemanticsOrigin = "generic_machine_ir"
	v.IRSHA256 = source.IRSHA256
	v.EditedIRSHA256 = source.EditedIRSHA256
	v.PlanSHA256 = source.PlanSHA256
	v.EditSHA256 = source.EditSHA256
	v.ROMSHA256 = c.ROMSHA256
	v.OriginalIRJSON = source.OriginalIRJSON
	v.EditedIRJSON = source.EditedIRJSON
	for _, b := range []*machinebranch.Branch{&r.Baseline, &r.Replica} {
		b.Frames[0].StartCycle = 1
		b.Frames[0].VBlankCycle = 2
		b.Frames[0].EndCycle = 3
	}
	return r, err
}

func TestRecoveredExperimentRunRepeatAndRestore(t *testing.T) {
	e := recoveredFixture(t)
	e.run = func(_ context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
		return fakeRecoveredMachine(t, e.rom, c)
	}
	var edited string
	for _, addend := range []uint8{6, 6, 5} {
		j, err := e.start(addend)
		if err != nil {
			t.Fatal(err)
		}
		j = waitExperiment(t, e, j.ID)
		if j.Status != "complete" || !j.BaselineAgreement || j.CapturedProofEligible {
			t.Fatalf("job: %+v", j)
		}
		if addend == 6 {
			if j.FrameDifferences != 1 || j.Original.Compiled.SourceSHA256 == j.Edited.Compiled.SourceSHA256 || j.Original.Compiled.IRSHA256 == j.Edited.Compiled.EditedIRSHA256 {
				t.Fatal("edited source/IR identities or frames unchanged")
			}
			if edited != "" && edited != j.Edited.Compiled.SourceSHA256 {
				t.Fatal("repeat source differs")
			}
			edited = j.Edited.Compiled.SourceSHA256
		} else if j.FrameDifferences != 0 || j.Original.Compiled.SourceSHA256 != j.Edited.Compiled.SourceSHA256 {
			t.Fatal("restore differs")
		}
	}
}

func TestExperimentRefusesMissingObservationReport(t *testing.T) {
	e := recoveredFixture(t)
	e.config.Observation = &machinebranch.ObservationConfig{From: 0, To: 1, MaxEvents: 100, Selection: provenance.Selection{Frame: 0, RoutineStart: 0xcc45b, RoutineEnd: 0xcc47c, Sprite: 0}}
	e.run = func(_ context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
		return fakeRecoveredMachine(t, e.rom, c)
	}
	j, err := e.start(6)
	if err != nil {
		t.Fatal(err)
	}
	j = waitExperiment(t, e, j.ID)
	if j.Status != "refused" || j.Original != nil || j.Edited != nil {
		t.Fatal("missing observations published")
	}
}

func TestOperatorBackendMetadata(t *testing.T) {
	for _, mode := range []string{"generated_c", "recovered_c"} {
		e := experimentFixture(t, fakeMachine)
		e.config.Mode = mode
		m := &Model{Experiments: e, ExperimentEnabled: true}
		w := httptest.NewRecorder()
		Handler(m).ServeHTTP(w, httptest.NewRequest("GET", "/api/target", nil))
		var target Model
		if err := json.Unmarshal(w.Body.Bytes(), &target); err != nil {
			t.Fatal(err)
		}
		if target.ExperimentMode != mode || m.ExperimentMode != "" {
			t.Fatal("operator mode missing or caller model changed")
		}
	}
}
