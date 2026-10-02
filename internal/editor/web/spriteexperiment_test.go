package web

import (
	"bytes"
	"compress/gzip"
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
	"github.com/tmc/snes/internal/provenance"
)

func spriteExperimentFixture(t *testing.T, runner ExperimentRunner) *Experiments {
	t.Helper()
	dir := t.TempDir()
	rom := []byte("testROM")
	state := []byte("checkpoint")
	cfg := machinebranch.Config{ROMPath: filepath.Join(dir, "rom"), ROMSHA256: fmt.Sprintf("%x", sha256.Sum256(rom)), StatePath: filepath.Join(dir, "state"), StateSHA256: fmt.Sprintf("%x", sha256.Sum256(state)), Frames: 1, Mode: "sprite_data", SpriteEdit: &machinebranch.SpriteEdit{CapturePath: filepath.Join(dir, "capture.gz"), Sprite: 0, ThroughFrame: 0}}
	os.WriteFile(cfg.ROMPath, rom, 0600)
	os.WriteFile(cfg.StatePath, state, 0600)
	events := []provenance.Event{{Kind: "bus", Actor: "cpu", Op: "write", Addr: 0xa00, Value: 7, PC: 0x8600}, {Kind: "dma", Channel: 0, Mode: 0, Target: 4, Count: 1, Addr: 0xa00}, {Kind: "bus", Actor: "dma_or_hdma", Op: "read", Addr: 0xa00, Value: 7}, {Kind: "bus", Actor: "dma_or_hdma", Op: "write", Addr: 0x2104, Value: 7}, {Kind: "ppu", Space: "oam", Op: "write", Addr: 512, Value: 7}}
	for i := range events {
		events[i].ID = uint64(i)
		events[i].Cycle = uint64(i)
	}
	b, _ := json.Marshal(map[string]any{"schema": 1, "complete": true, "rom_sha256": cfg.ROMSHA256, "writer_coverage": "all_cpu_dma_hdma_wram_and_wram_port", "from": 0, "to": 1, "events": events})
	var buf bytes.Buffer
	z := gzip.NewWriter(&buf)
	z.Write(b)
	z.Close()
	os.WriteFile(cfg.SpriteEdit.CapturePath, buf.Bytes(), 0600)
	cfg.SpriteEdit.CaptureSHA256 = fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
	sprites, err := LoadSprites(cfg.SpriteEdit.CapturePath, cfg.SpriteEdit.CaptureSHA256, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(cfg)
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, b, 0600)
	e, err := NewSpriteExperiments(p, fmt.Sprintf("%x", sha256.Sum256(b)), filepath.Join(dir, "jobs"), sprites, runner)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func fakeSpriteMachine(ctx context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
	cfg := c
	cfg.Addend = 5
	if c.SpriteEdit.Large != nil && !*c.SpriteEdit.Large {
		cfg.Addend = 6
	}
	r, _ := fakeMachine(ctx, cfg)
	r.Config = c
	r.Mode = "sprite_data"
	r.Compiled = nil
	r.ReplacementExecuted = false
	r.OriginalMatch = cfg.Addend == 5
	sprites, err := LoadSprites(c.SpriteEdit.CapturePath, c.SpriteEdit.CaptureSHA256, c.SpriteEdit.ThroughFrame)
	if err != nil {
		return nil, err
	}
	e := sprites.Entries[c.SpriteEdit.Sprite].Explanation
	base := machinebranch.SpriteExperiment{CaptureSHA256: c.SpriteEdit.CaptureSHA256, Explanation: e, Link: *e.Bytes[4].Link, Mask: 2, Before: 7, After: 7, CompletionCycle: 1, WriterMatched: true, DMAReadMatched: true, RegisterWriteMatched: true, OAMWriteMatched: true}
	edit := base
	edit.Applied = c.SpriteEdit.Large != nil
	if edit.Applied && !*c.SpriteEdit.Large {
		edit.After = 5
	}
	r.Sprite = &edit
	r.SpriteBaseline = &base
	return r, nil
}
func TestSpriteExperimentEditReset(t *testing.T) {
	e := spriteExperimentFixture(t, fakeSpriteMachine)
	os.WriteFile(e.config.SpriteEdit.CapturePath, []byte("changed input"), 0600)
	for _, v := range []uint8{1, 2} {
		j, err := e.start(v)
		if err != nil {
			t.Fatal(err)
		}
		j = waitExperiment(t, e, j.ID)
		if j.Status != "complete" || !j.BaselineAgreement || j.CapturedProofEligible || j.Kind != "sprite_size_data" || j.SpriteID != e.spriteID {
			t.Fatalf("job %+v", j)
		}
		want := 1
		if v == 2 {
			want = 0
		}
		if j.FrameDifferences != want {
			t.Fatal(j.FrameDifferences)
		}
		h := Handler(&Model{SpriteExperiments: e})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/sprite-job/frame?id="+j.ID+"&branch=edited&index=0", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
}
func TestSpriteExperimentRefusals(t *testing.T) {
	for _, kind := range []string{"proof", "replacement", "chain", "baseline", "byte", "evidence", "identity", "pixels"} {
		t.Run(kind, func(t *testing.T) {
			e := spriteExperimentFixture(t, func(ctx context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
				r, err := fakeSpriteMachine(ctx, c)
				if err != nil {
					return nil, err
				}
				switch kind {
				case "proof":
					r.CapturedProofEligible = true
				case "replacement":
					r.ReplacementExecuted = true
				case "chain":
					r.Sprite.DMAReadMatched = false
				case "baseline":
					r.Replica.Frames[0].StateSHA256 = strings.Repeat("9", 64)
				case "byte":
					r.Sprite.After ^= 4
				case "evidence":
					r.Sprite.Link.Read.ID++
				case "identity":
					r.Config.StateSHA256 = strings.Repeat("9", 64)
				case "pixels":
					r.Replica.Frames[0].Pixels[0] = 7
				}
				return r, nil
			})
			j, err := e.start(1)
			if err != nil {
				t.Fatal(err)
			}
			j = waitExperiment(t, e, j.ID)
			if j.Status != "refused" || j.Edited != nil || j.CapturedProofEligible {
				t.Fatalf("accepted %+v", j)
			}
		})
	}
}
func TestSpriteExperimentRequests(t *testing.T) {
	e := spriteExperimentFixture(t, fakeSpriteMachine)
	h := Handler(&Model{SpriteExperiments: e})
	for _, body := range []string{`{}`, `{"sprite_id":"wrong","large":false}`, fmt.Sprintf(`{"sprite_id":%q,"large":null}`, e.spriteID), fmt.Sprintf(`{"sprite_id":%q,"large":false,"writer_pc":123}`, e.spriteID), fmt.Sprintf(`{"sprite_id":%q,"large":false} {}`, e.spriteID)} {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("POST", "/api/sprite-experiment", strings.NewReader(body))
		q.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(w, q)
		if w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	w := httptest.NewRecorder()
	q := httptest.NewRequest("POST", "/api/sprite-experiment", strings.NewReader(fmt.Sprintf(`{"sprite_id":%q,"large":false}`, e.spriteID)))
	q.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, q)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var j ExperimentJob
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if waitExperiment(t, e, j.ID).Status != "complete" {
		t.Fatal("valid request refused")
	}
}
func TestRetainedSpriteExperiment(t *testing.T) {
	p := os.Getenv("SNES_SPRITE_EXPERIMENT_CONFIG")
	if p == "" {
		t.Skip("retained sprite config not configured")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var c machinebranch.Config
	if err = decodeStrict(b, &c); err != nil {
		t.Fatal(err)
	}
	sprites, err := LoadSprites(c.SpriteEdit.CapturePath, c.SpriteEdit.CaptureSHA256, c.SpriteEdit.ThroughFrame)
	if err != nil {
		t.Fatal(err)
	}
	out := os.Getenv("SNES_SPRITE_EXPERIMENT_OUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "jobs")
	}
	e, err := NewSpriteExperiments(p, fmt.Sprintf("%x", sha256.Sum256(b)), out, sprites, machinebranch.Run)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint8{1, 2} {
		j, err := e.start(v)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Minute)
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
		if j.Status != "complete" || !j.BaselineAgreement || j.CapturedProofEligible || j.Edited.ReplacementExecuted {
			t.Fatalf("job status=%s err=%s", j.Status, j.Error)
		}
		if v == 1 && j.FrameDifferences == 0 || v == 2 && j.FrameDifferences != 0 {
			t.Fatal("unexpected pixel comparison")
		}
		t.Logf("job=%s sprite=%s large=%v frames=%d changed=%d proof=false replacement=false", j.ID, j.SpriteID, *j.Large, len(j.Edited.Replica.Frames), j.FrameDifferences)
	}
}

func TestExperimentSharedBudget(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	parameter := experimentFixture(t, func(ctx context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
		entered <- struct{}{}
		<-release
		return fakeMachine(ctx, c)
	})
	sprite := spriteExperimentFixture(t, fakeSpriteMachine)
	Handler(&Model{Experiments: parameter, SpriteExperiments: sprite})
	j, err := parameter.start(5)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = sprite.start(1); err == nil {
		close(release)
		t.Fatal("concurrent sprite job accepted")
	}
	close(release)
	if waitExperiment(t, parameter, j.ID).Status != "complete" {
		t.Fatal("baseline refused")
	}
	j, err = sprite.start(1)
	if err != nil {
		t.Fatal(err)
	}
	if waitExperiment(t, sprite, j.ID).Status != "complete" {
		t.Fatal("sprite refused after release")
	}
}
func TestSpriteOperatorMismatch(t *testing.T) {
	e := spriteExperimentFixture(t, fakeSpriteMachine)
	b, err := os.ReadFile(filepath.Join(e.out, "operator-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c machinebranch.Config
	decodeStrict(b, &c)
	sprites, err := LoadSprites(c.SpriteEdit.CapturePath, c.SpriteEdit.CaptureSHA256, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"edited_config", "wrong_capture", "wrong_sprite", "wrong_frame", "forged_selection"} {
		t.Run(kind, func(t *testing.T) {
			cfg := c
			edit := *c.SpriteEdit
			cfg.SpriteEdit = &edit
			evidence := *sprites
			evidence.Entries = append([]SpriteSelection(nil), sprites.Entries...)
			switch kind {
			case "edited_config":
				v := false
				edit.Large = &v
			case "wrong_capture":
				edit.CaptureSHA256 = strings.Repeat("0", 64)
			case "wrong_sprite":
				edit.Sprite = 128
			case "wrong_frame":
				edit.ThroughFrame = 1
			case "forged_selection":
				evidence.Entries[0].ID = "forged"
			}
			b, _ := json.Marshal(cfg)
			dir := t.TempDir()
			p := filepath.Join(dir, "config")
			os.WriteFile(p, b, 0600)
			if _, err := NewSpriteExperiments(p, fmt.Sprintf("%x", sha256.Sum256(b)), filepath.Join(dir, "jobs"), &evidence, fakeSpriteMachine); err == nil {
				t.Fatal("invalid operator configuration accepted")
			}
		})
	}
}
