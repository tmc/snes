package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

// ExperimentRunner executes the timed machine bridge. Tests may inject a runner;
// production passes machinebranch.Run. It must return owned frame pixels.
type ExperimentRunner func(context.Context, machinebranch.Config) (*machinebranch.Result, error)

// Experiments owns immutable inputs and one active bounded local job at a time.
// The zero value is unusable; use NewExperiments.
type Experiments struct {
	mu              sync.Mutex
	config          machinebranch.Config
	configSHA       string
	controllerSHA   string
	rom, state      []byte
	out             string
	run             ExperimentRunner
	jobs            map[string]*ExperimentJob
	active          bool
	gate            *sync.Mutex
	capture         []byte
	spriteID        string
	resetLarge      bool
	spriteSelection *SpriteSelection
}

// ExperimentJob separates original C agreement from intentional source changes.
type ExperimentJob struct {
	ID                    string                `json:"id"`
	Status                string                `json:"status"`
	Error                 string                `json:"error,omitempty"`
	ControllerSHA256      string                `json:"controller_sha256"`
	ConfigSHA256          string                `json:"config_sha256"`
	Kind                  string                `json:"kind"`
	SpriteID              string                `json:"sprite_id,omitempty"`
	Large                 *bool                 `json:"large,omitempty"`
	Addend                uint8                 `json:"addend"`
	BaselineAgreement     bool                  `json:"baseline_agreement"`
	CapturedProofEligible bool                  `json:"captured_proof_eligible"`
	Original              *machinebranch.Result `json:"original,omitempty"`
	Edited                *machinebranch.Result `json:"edited,omitempty"`
	FrameDifferences      int                   `json:"frame_differences"`
	dir                   string
}

func readOwned(path, pin string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || int64(len(b)) > limit || len(pin) != 64 || fmt.Sprintf("%x", sha256.Sum256(b)) != pin {
		return nil, fmt.Errorf("input identity or size mismatch")
	}
	return b, nil
}
func decodeStrict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

// NewExperiments snapshots explicitly pinned configuration, ROM and complete state.
// Out must be a new directory. The operator selects authored or recovered C.
// HTTP changes only the increment; recovered C accepts 5 and 6.
func NewExperiments(config, pin, out string, run ExperimentRunner) (*Experiments, error) {
	return newExperiments(config, pin, out, run, false)
}
func newExperiments(config, pin, out string, run ExperimentRunner, sprite bool) (*Experiments, error) {
	if run == nil || !filepath.IsAbs(out) {
		return nil, fmt.Errorf("runner and absolute output directory required")
	}
	b, err := readOwned(config, pin, 1<<20)
	if err != nil {
		return nil, err
	}
	var c machinebranch.Config
	if err := decodeStrict(b, &c); err != nil {
		return nil, err
	}
	if (!sprite && ((c.Mode != "generated_c" && c.Mode != "recovered_c") || c.Addend != 5 || c.SpriteEdit != nil)) || (sprite && (c.Mode != "sprite_data" || c.Addend != 0 || c.SpriteEdit == nil || c.SpriteEdit.Large != nil)) || c.Frames < 1 || c.Frames > 600 || len(c.Inputs) > 2*c.Frames {
		return nil, fmt.Errorf("original increment5 generated C configuration required")
	}
	for i, in := range c.Inputs {
		if in.Frame < 0 || in.Frame >= c.Frames || in.Port > 1 {
			return nil, fmt.Errorf("invalid input schedule")
		}
		if i > 0 {
			p := c.Inputs[i-1]
			if in.Frame < p.Frame || in.Frame == p.Frame && in.Port <= p.Port {
				return nil, fmt.Errorf("unordered input schedule")
			}
		}
	}
	rom, err := readOwned(c.ROMPath, c.ROMSHA256, 16<<20)
	if err != nil {
		return nil, err
	}
	if c.Mode == "recovered_c" {
		pins, err := machinebranch.PrepareRecovered(rom, 5)
		if err != nil {
			return nil, err
		}
		if c.Recovered == nil || *c.Recovered != pins {
			return nil, fmt.Errorf("operator recovered identities differ from owned ROM")
		}
	} else if c.Recovered != nil {
		return nil, fmt.Errorf("recovered identities require recovered C mode")
	}
	state, err := readOwned(c.StatePath, c.StateSHA256, 128<<20)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "operator-config.json"), b, 0600); err != nil {
		os.RemoveAll(out)
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(exe)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	_, err = io.Copy(h, file)
	file.Close()
	if err != nil {
		return nil, err
	}
	return &Experiments{config: c, configSHA: pin, controllerSHA: fmt.Sprintf("%x", h.Sum(nil)), rom: rom, state: state, out: out, run: run, jobs: map[string]*ExperimentJob{}}, nil
}

func (e *Experiments) start(addend uint8) (*ExperimentJob, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if addend < 1 || addend > 12 || e.config.Mode == "sprite_data" && addend > 2 {
		return nil, fmt.Errorf("increment outside1..12")
	}
	if e.config.Mode == "recovered_c" && addend != 5 && addend != 6 {
		return nil, fmt.Errorf("recovered increment must be 5 or 6")
	}
	if e.active {
		return nil, fmt.Errorf("experiment already running")
	}
	if len(e.jobs) >= 32 {
		return nil, fmt.Errorf("session job budget exhausted")
	}
	dir, err := os.MkdirTemp(e.out, "job-")
	if err != nil {
		return nil, err
	}
	if e.gate != nil && !e.gate.TryLock() {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("experiment already running")
	}
	j := &ExperimentJob{ID: filepath.Base(dir), Status: "running", Addend: addend, ConfigSHA256: e.configSHA, ControllerSHA256: e.controllerSHA, dir: dir}
	j.Kind = "source_increment"
	if e.config.Mode == "sprite_data" {
		j.Kind = "sprite_size_data"
		j.SpriteID = e.spriteID
		large := addend == 2
		j.Large = &large
	}
	e.jobs[j.ID] = j
	e.active = true
	go e.execute(j.ID)
	copy := *j
	return &copy, nil
}
func (e *Experiments) execute(id string) {
	e.mu.Lock()
	j := *e.jobs[id]
	e.mu.Unlock()
	err := e.executeJob(&j)
	if err != nil {
		j.Status = "refused"
		j.Error = err.Error()
		j.Original = nil
		j.Edited = nil
	} else {
		j.Status = "complete"
	}
	e.mu.Lock()
	e.jobs[id] = &j
	e.active = false
	if e.gate != nil {
		e.gate.Unlock()
	}
	e.mu.Unlock()
}
func (e *Experiments) executeJob(j *ExperimentJob) error {
	if e.config.Mode == "sprite_data" {
		return e.executeSpriteJob(j)
	}
	c := e.config
	c.ROMPath = filepath.Join(j.dir, "rom.bin")
	c.StatePath = filepath.Join(j.dir, "checkpoint.state")
	if err := os.WriteFile(c.ROMPath, e.rom, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(c.StatePath, e.state, 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	run := func(addend uint8) (*machinebranch.Result, error) {
		cfg := c
		cfg.Addend = addend
		if cfg.Mode == "recovered_c" {
			pins, err := machinebranch.PrepareRecovered(e.rom, addend)
			if err != nil {
				return nil, err
			}
			cfg.Recovered = &pins
		}
		cfg.Inputs = append([]machinebranch.Input(nil), c.Inputs...)
		r, err := e.run(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if err := checkMachineResult(r, cfg); err != nil {
			return nil, err
		}
		return r, nil
	}
	original, err := run(5)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(original.Baseline.Frames, original.Replica.Frames) {
		return fmt.Errorf("original generated C disagrees with interpreter baseline")
	}
	edited := original
	if j.Addend != 5 {
		edited, err = run(j.Addend)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(original.Baseline.Frames, edited.Baseline.Frames) {
			return fmt.Errorf("edited run interpreter baseline changed")
		}
	}
	for i, f := range original.Baseline.Frames {
		if f.FramebufferSHA256 != edited.Replica.Frames[i].FramebufferSHA256 {
			j.FrameDifferences++
		}
	}
	if err := publishBranchImages(j.dir, "original", original); err != nil {
		return err
	}
	if j.Addend == 5 {
		b, _ := json.Marshal(original)
		var cloned machinebranch.Result
		json.Unmarshal(b, &cloned)
		edited = &cloned
		for i := range edited.Baseline.Frames {
			edited.Baseline.Frames[i].PNGPath = original.Baseline.Frames[i].PNGPath
			edited.Replica.Frames[i].PNGPath = original.Replica.Frames[i].PNGPath
		}
	} else if err := publishBranchImages(j.dir, "edited", edited); err != nil {
		return err
	}
	j.BaselineAgreement = true
	j.Original = original
	j.Edited = edited
	j.Status = "complete"
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(j.dir, "result.json"), b, 0600); err != nil {
		return err
	}
	return nil
}
func checkMachineResult(r *machinebranch.Result, c machinebranch.Config) error {
	if r == nil || r.Schema != "snes-machine-branch-v1" || r.Mode != c.Mode || (c.Mode != "generated_c" && c.Mode != "recovered_c") || !r.ReplacementExecuted || r.CapturedProofEligible || !reflect.DeepEqual(r.Config, c) || r.Compiled == nil || r.Compiled.Addend != c.Addend || r.Compiled.Instructions == 0 || r.Compiled.Compiler == "" || len(r.Compiled.RunnerSHA256) != 64 || fmt.Sprintf("%x", sha256.Sum256([]byte(r.Compiled.Source))) != r.Compiled.SourceSHA256 {
		return fmt.Errorf("unsupported machine result or identity")
	}
	if c.Mode == "recovered_c" {
		schedule := make([]byte, 0, len(c.Inputs)*12)
		for _, in := range c.Inputs {
			schedule = fmt.Appendf(schedule, "%d:%d:%d\n", in.Frame, in.Port, in.Buttons)
		}
		if r.InputsSHA256 != fmt.Sprintf("%x", sha256.Sum256(schedule)) || r.OriginalMatch != reflect.DeepEqual(r.Baseline.Frames, r.Replica.Frames) {
			return fmt.Errorf("recovered input or agreement identity differs")
		}
		p, v := c.Recovered, r.Compiled
		if p == nil || v.SemanticsOrigin != "generic_machine_ir" || v.ROMSHA256 != c.ROMSHA256 || v.SourceSHA256 != p.SourceSHA256 || v.IRSHA256 != p.IRSHA256 || v.EditedIRSHA256 != p.EditedIRSHA256 || v.PlanSHA256 != p.PlanSHA256 || v.EditSHA256 != p.EditSHA256 || fmt.Sprintf("%x", sha256.Sum256(v.OriginalIRJSON)) != p.IRSHA256 || fmt.Sprintf("%x", sha256.Sum256(v.EditedIRJSON)) != p.EditedIRSHA256 {
			return fmt.Errorf("recovered machine provenance differs")
		}
		for i, a := range r.Baseline.Frames {
			if i >= len(r.Replica.Frames) {
				return fmt.Errorf("recovered frame count differs")
			}
			b := r.Replica.Frames[i]
			if a.StartCycle >= a.VBlankCycle || a.VBlankCycle > a.EndCycle || a.PPUFrame != b.PPUFrame || a.StartCycle != b.StartCycle || a.VBlankCycle != b.VBlankCycle || b.VBlankCycle > b.EndCycle || b.EndCycle-b.VBlankCycle > 100000 || a.EndCycle-a.VBlankCycle > 100000 || i > 0 && (a.StartCycle <= r.Baseline.Frames[i-1].StartCycle || a.PPUFrame != r.Baseline.Frames[i-1].PPUFrame+1) {
				return fmt.Errorf("recovered frame clocks differ")
			}
		}
	}
	return checkFrames(r, c)
}
func checkFrames(r *machinebranch.Result, c machinebranch.Config) error {
	if r.Baseline.InitialStateSHA256 != c.StateSHA256 || r.Replica.InitialStateSHA256 != c.StateSHA256 || len(r.Baseline.Frames) != c.Frames || len(r.Replica.Frames) != c.Frames {
		return fmt.Errorf("machine branch input or frame count differs")
	}
	for _, b := range []machinebranch.Branch{r.Baseline, r.Replica} {
		for i, f := range b.Frames {
			data := make([]byte, 2*len(f.Pixels))
			for n, p := range f.Pixels {
				data[2*n] = byte(p)
				data[2*n+1] = byte(p >> 8)
			}
			if fmt.Sprintf("%x", sha256.Sum256(data)) != f.FramebufferSHA256 {
				return fmt.Errorf("frame pixels differ from reported hash")
			}
			if f.RelativeFrame != i || f.Width != 256 || f.Height != 224 || len(f.Pixels) != f.Width*f.Height || len(f.FramebufferSHA256) != 64 || len(f.StateSHA256) != 64 || len(f.BusSHA256) != 64 {
				return fmt.Errorf("unsupported machine frame profile")
			}
		}
	}
	return nil
}
func publishBranchImages(dir, kind string, r *machinebranch.Result) error {
	for _, b := range []*machinebranch.Branch{&r.Baseline, &r.Replica} {
		branch := "baseline"
		if b == &r.Replica {
			branch = "replica"
		}
		name := kind + "-" + branch
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			return err
		}
		for i := range b.Frames {
			f := &b.Frames[i]
			im := image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
			expand := func(v uint16) uint8 { return uint8(v<<3 | v>>2) }
			for n, p := range f.Pixels {
				im.SetRGBA(n%f.Width, n/f.Width, color.RGBA{expand(p & 31), expand(p >> 5 & 31), expand(p >> 10 & 31), 255})
			}
			var out bytes.Buffer
			if err := png.Encode(&out, im); err != nil {
				return err
			}
			f.PNGPath = name + fmt.Sprintf("/%06d.png", i)
			f.PNGSHA256 = fmt.Sprintf("%x", sha256.Sum256(out.Bytes()))
			if err := os.WriteFile(filepath.Join(dir, f.PNGPath), out.Bytes(), 0600); err != nil {
				return err
			}
			f.Pixels = nil
		}
	}
	return nil
}

func (e *Experiments) routes(mux *http.ServeMux) { e.registerRoutes(mux, "", false) }
func (e *Experiments) registerRoutes(mux *http.ServeMux, prefix string, sprite bool) {
	mux.HandleFunc("/api/"+prefix+"experiment", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", 405)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "http" || u.Host != r.Host {
				http.Error(w, "origin differs", 403)
				return
			}
		}
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "JSON required", 415)
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 1025))
		if err != nil || len(b) > 1024 {
			http.Error(w, "request too large", 400)
			return
		}
		var q struct {
			Addend uint8 `json:"addend"`
		}
		if sprite {
			var request struct {
				SpriteID string `json:"sprite_id"`
				Large    *bool  `json:"large"`
			}
			if err := decodeStrict(b, &request); err != nil || request.SpriteID != e.spriteID || request.Large == nil {
				http.Error(w, "unsupported sprite selection", 400)
				return
			}
			q.Addend = 1
			if *request.Large {
				q.Addend = 2
			}
		} else if err := decodeStrict(b, &q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		j, err := e.start(q.Addend)
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(j)
	})
	mux.HandleFunc("/api/"+prefix+"job", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", 405)
			return
		}
		e.mu.Lock()
		j, ok := e.jobs[r.URL.Query().Get("id")]
		if ok {
			copy := *j
			j = &copy
		}
		e.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(j)
	})
	mux.HandleFunc("/api/"+prefix+"job/frame", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", 405)
			return
		}
		n, err := strconv.Atoi(r.URL.Query().Get("index"))
		if err != nil || n < 0 {
			http.NotFound(w, r)
			return
		}
		e.mu.Lock()
		j, ok := e.jobs[r.URL.Query().Get("id")]
		var f machinebranch.Frame
		dir := ""
		if ok && j.Status == "complete" {
			var branch *machinebranch.Branch
			switch r.URL.Query().Get("branch") {
			case "original":
				branch = &j.Original.Replica
			case "edited":
				branch = &j.Edited.Replica
			}
			if branch != nil && n < len(branch.Frames) {
				f = branch.Frames[n]
				dir = j.dir
			}
		}
		e.mu.Unlock()
		if dir == "" {
			http.NotFound(w, r)
			return
		}
		b, err := readOwned(filepath.Join(dir, f.PNGPath), f.PNGSHA256, 2<<20)
		if err != nil {
			http.Error(w, "frame artifact changed", 409)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(b)
	})
}

// ROMSHA256 returns the immutable experiment ROM identity.
func (e *Experiments) ROMSHA256() string { return e.config.ROMSHA256 }
