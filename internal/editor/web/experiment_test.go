package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

func experimentFixture(t *testing.T, runner ExperimentRunner) *Experiments {
	t.Helper()
	dir := t.TempDir()
	rom := []byte("originalROM")
	state := []byte("completecheckpoint")
	cfg := machinebranch.Config{ROMPath: filepath.Join(dir, "rom"), ROMSHA256: fmt.Sprintf("%x", sha256.Sum256(rom)), StatePath: filepath.Join(dir, "state"), StateSHA256: fmt.Sprintf("%x", sha256.Sum256(state)), Frames: 1, Mode: "generated_c", Addend: 5}
	os.WriteFile(cfg.ROMPath, rom, 0600)
	os.WriteFile(cfg.StatePath, state, 0600)
	b, _ := json.Marshal(cfg)
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, b, 0600)
	e, err := NewExperiments(p, fmt.Sprintf("%x", sha256.Sum256(b)), filepath.Join(dir, "jobs"), runner)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func fakeMachine(_ context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
	frame := machinebranch.Frame{RelativeFrame: 0, PPUFrame: 1, Width: 256, Height: 224, Pixels: make([]uint16, 256*224), StateSHA256: strings.Repeat("1", 64), BusSHA256: strings.Repeat("2", 64), BusEvents: 1, Components: map[string]string{"cpu": strings.Repeat("1", 64)}}
	frame.FramebufferSHA256 = fmt.Sprintf("%x", sha256.Sum256(make([]byte, 2*len(frame.Pixels))))
	b := machinebranch.Branch{Name: "baseline", InitialStateSHA256: c.StateSHA256, Frames: []machinebranch.Frame{frame}}
	r := b
	r.Name = "replica"
	r.Frames = append([]machinebranch.Frame(nil), b.Frames...)
	r.Frames[0].Pixels = append([]uint16(nil), frame.Pixels...)
	if c.Addend != 5 {
		r.Frames[0].Pixels[0] = 1
		data := make([]byte, 2*len(frame.Pixels))
		data[0] = 1
		r.Frames[0].FramebufferSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
		r.Frames[0].StateSHA256 = strings.Repeat("3", 64)
	}
	return &machinebranch.Result{Schema: "snes-machine-branch-v1", Mode: "generated_c", Config: c, ReplacementExecuted: true, Baseline: b, Replica: r, Compiled: &machinebranch.Compiled{Source: "test source", SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("test source"))), RunnerSHA256: strings.Repeat("4", 64), Compiler: "fake test compiler", Addend: c.Addend, Instructions: 1}}, nil
}
func waitExperiment(t *testing.T, e *Experiments, id string) *ExperimentJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		j := *e.jobs[id]
		e.mu.Unlock()
		if j.Status != "running" {
			return &j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job timeout")
	return nil
}
func TestExperimentRunAndReset(t *testing.T) {
	e := experimentFixture(t, fakeMachine)
	os.WriteFile(e.config.ROMPath, []byte("callerchangedROM"), 0600)
	for _, n := range []uint8{6, 5} {
		j, err := e.start(n)
		if err != nil {
			t.Fatal(err)
		}
		j = waitExperiment(t, e, j.ID)
		if j.Status != "complete" || !j.BaselineAgreement || j.CapturedProofEligible {
			t.Fatalf("job %+v", j)
		}
		want := 1
		if n == 5 {
			want = 0
		}
		if j.FrameDifferences != want {
			t.Fatal(j.FrameDifferences)
		}
		if _, err := os.Stat(filepath.Join(j.dir, "result.json")); err != nil {
			t.Fatal(err)
		}
		h := Handler(&Model{Experiments: e})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/job/frame?id="+j.ID+"&branch=edited&index=0", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
}
func TestExperimentRefusals(t *testing.T) {
	for _, kind := range []string{"proof", "baseline", "identity", "pixels"} {
		t.Run(kind, func(t *testing.T) {
			e := experimentFixture(t, func(ctx context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
				r, err := fakeMachine(ctx, c)
				switch kind {
				case "proof":
					r.CapturedProofEligible = true
				case "baseline":
					r.Replica.Frames[0].StateSHA256 = strings.Repeat("9", 64)
				case "identity":
					r.Config.ROMSHA256 = strings.Repeat("9", 64)
				case "pixels":
					r.Replica.Frames[0].Pixels[0] = 7
				}
				return r, err
			})
			j, err := e.start(6)
			if err != nil {
				t.Fatal(err)
			}
			j = waitExperiment(t, e, j.ID)
			if j.Status != "refused" || j.Edited != nil || j.BaselineAgreement {
				t.Fatalf("accepted %s", kind)
			}
		})
	}
}
func TestExperimentRequests(t *testing.T) {
	gate := make(chan struct{})
	e := experimentFixture(t, func(ctx context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
		<-gate
		return fakeMachine(ctx, c)
	})
	h := Handler(&Model{Experiments: e})
	rBad := httptest.NewRequest("POST", "/api/experiment", strings.NewReader(`{"addend":6}`))
	rBad.Header.Set("Content-Type", "application/json")
	rBad.Header.Set("Origin", "http://elsewhere.test")
	wBad := httptest.NewRecorder()
	h.ServeHTTP(wBad, rBad)
	if wBad.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
	for _, body := range []string{`{"addend":0}`, `{"addend":13}`, `{"addend":6,"rom_path":"elsewhere"}`, `{"addend":6} {}`} {
		r := httptest.NewRequest("POST", "/api/experiment", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 202 {
			t.Fatal("invalid request accepted")
		}
	}
	r := httptest.NewRequest("POST", "/api/experiment", strings.NewReader(`{"addend":6}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	if _, err := e.start(5); err == nil {
		t.Fatal("overlapping run accepted")
	}
	close(gate)
	var j ExperimentJob
	json.Unmarshal(w.Body.Bytes(), &j)
	waitExperiment(t, e, j.ID)
}

func TestRetainedMachineExperiment(t *testing.T) {
	config := os.Getenv("SNES_MACHINE_EXPERIMENT_CONFIG")
	if config == "" {
		t.Skip("retained complete-machine config not selected")
	}
	e, err := NewExperiments(config, os.Getenv("SNES_MACHINE_EXPERIMENT_SHA256"), os.Getenv("SNES_MACHINE_EXPERIMENT_OUT"), machinebranch.Run)
	if err != nil {
		t.Fatal(err)
	}
	for _, addend := range []uint8{6, 5} {
		j, err := e.start(addend)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(9 * time.Minute)
		for time.Now().Before(deadline) {
			e.mu.Lock()
			copy := *e.jobs[j.ID]
			e.mu.Unlock()
			j = &copy
			if j.Status != "running" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if j.Status != "complete" || !j.BaselineAgreement || j.CapturedProofEligible {
			t.Fatalf("job status=%s error=%s", j.Status, j.Error)
		}
		if addend == 6 && j.FrameDifferences == 0 {
			t.Fatal("edited result has no actual pixel divergence")
		}
		if addend == 5 && j.FrameDifferences != 0 {
			t.Fatal("reset fails original frame agreement")
		}
		t.Logf("job=%s addend=%d frames=%d differences=%d originalCagreement=%v proof=%v", j.ID, j.Addend, len(j.Edited.Replica.Frames), j.FrameDifferences, j.BaselineAgreement, j.CapturedProofEligible)
	}
}
