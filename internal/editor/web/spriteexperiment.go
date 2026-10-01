package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

// NewSpriteExperiments snapshots a no-edit operator configuration and its capture.
// Only its selected sprite's size bit may change; it grants no captured proof.
func NewSpriteExperiments(config, pin, out string, sprites *Sprites, run ExperimentRunner) (*Experiments, error) {
	b, err := readOwned(config, pin, 1<<20)
	if err != nil {
		return nil, err
	}
	var c machinebranch.Config
	if err = decodeStrict(b, &c); err != nil {
		return nil, err
	}
	if c.SpriteEdit == nil || sprites == nil || c.ROMSHA256 != sprites.ROMSHA256 || c.SpriteEdit.CaptureSHA256 != sprites.CaptureSHA256 || c.SpriteEdit.ThroughFrame != sprites.ThroughFrame {
		return nil, fmt.Errorf("sprite operator capture differs from selected evidence")
	}
	var selected *SpriteSelection
	for i := range sprites.Entries {
		if sprites.Entries[i].Explanation.Sprite == c.SpriteEdit.Sprite {
			selected = &sprites.Entries[i]
			break
		}
	}
	if selected == nil || selected.Explanation.Large == nil || len(selected.Explanation.Bytes) != 5 || selected.Explanation.Bytes[4].Link == nil || selected.Explanation.Bytes[4].Link.Writer == nil {
		return nil, fmt.Errorf("selected sprite size provenance unavailable")
	}
	capture, err := readOwned(c.SpriteEdit.CapturePath, c.SpriteEdit.CaptureSHA256, 16<<20)
	if err != nil {
		return nil, err
	}
	// Re-derive from the pinned bytes, rather than accepting caller-supplied links.
	measured, err := LoadSprites(c.SpriteEdit.CapturePath, c.SpriteEdit.CaptureSHA256, c.SpriteEdit.ThroughFrame)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(measured, sprites) {
		return nil, fmt.Errorf("selected sprite evidence differs from capture")
	}
	e, err := newExperiments(config, pin, out, run, true)
	if err != nil {
		return nil, err
	}
	owned := measured.Entries[c.SpriteEdit.Sprite]
	e.capture = capture
	e.spriteID = selected.ID
	e.resetLarge = *selected.Explanation.Large
	e.spriteSelection = &owned
	return e, nil
}

func (e *Experiments) executeSpriteJob(j *ExperimentJob) error {
	c := e.config
	c.ROMPath = filepath.Join(j.dir, "rom.bin")
	c.StatePath = filepath.Join(j.dir, "checkpoint.state")
	edit := *c.SpriteEdit
	edit.CapturePath = filepath.Join(j.dir, "capture.json.gz")
	c.SpriteEdit = &edit
	for _, f := range []struct {
		path string
		b    []byte
	}{{c.ROMPath, e.rom}, {c.StatePath, e.state}, {edit.CapturePath, e.capture}} {
		if err := os.WriteFile(f.path, f.b, 0600); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	run := func(large *bool) (*machinebranch.Result, error) {
		cfg := c
		cfg.Inputs = append([]machinebranch.Input(nil), c.Inputs...)
		selected := edit
		selected.Large = large
		cfg.SpriteEdit = &selected
		r, err := e.run(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if err = checkSpriteResult(r, cfg); err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(r.Sprite.Explanation, e.spriteSelection.Explanation) || !reflect.DeepEqual(r.Sprite.Link, *e.spriteSelection.Explanation.Bytes[4].Link) {
			return nil, fmt.Errorf("sprite result differs from pinned evidence")
		}
		return r, nil
	}
	original, err := run(nil)
	if err != nil {
		return err
	}
	if !original.OriginalMatch || !reflect.DeepEqual(original.Baseline.Frames, original.Replica.Frames) {
		return fmt.Errorf("sprite no-edit baseline disagrees")
	}
	edited, err := run(j.Large)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(original.Baseline.Frames, edited.Baseline.Frames) {
		return fmt.Errorf("sprite interpreter baseline changed")
	}
	if !reflect.DeepEqual(original.Sprite.Link, edited.Sprite.Link) || !reflect.DeepEqual(original.Sprite.Explanation, edited.Sprite.Explanation) {
		return fmt.Errorf("sprite captured byte version changed")
	}
	if *j.Large == e.resetLarge && (!edited.OriginalMatch || !reflect.DeepEqual(edited.Baseline.Frames, edited.Replica.Frames)) {
		return fmt.Errorf("captured sprite size reset disagrees with baseline")
	}
	for i, f := range original.Replica.Frames {
		if f.FramebufferSHA256 != edited.Replica.Frames[i].FramebufferSHA256 {
			j.FrameDifferences++
		}
	}
	if err = publishBranchImages(j.dir, "original", original); err != nil {
		return err
	}
	if err = publishBranchImages(j.dir, "edited", edited); err != nil {
		return err
	}
	j.Original = original
	j.Edited = edited
	j.BaselineAgreement = true
	j.Status = "complete"
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(j.dir, "result.json"), b, 0600)
}
func checkSpriteResult(r *machinebranch.Result, c machinebranch.Config) error {
	if r == nil || r.Schema != "snes-machine-branch-v1" || r.Mode != "sprite_data" || r.ReplacementExecuted || r.Compiled != nil || r.CapturedProofEligible || !reflect.DeepEqual(r.Config, c) || r.Sprite == nil || r.SpriteBaseline == nil {
		return fmt.Errorf("unsupported sprite machine identity")
	}
	for _, s := range []*machinebranch.SpriteExperiment{r.Sprite, r.SpriteBaseline} {
		if s.CaptureSHA256 != c.SpriteEdit.CaptureSHA256 || s.Explanation.Sprite != c.SpriteEdit.Sprite || s.Explanation.ThroughFrame != c.SpriteEdit.ThroughFrame || s.CapturedProofEligible || !s.WriterMatched || !s.DMAReadMatched || !s.RegisterWriteMatched || !s.OAMWriteMatched || s.Link.Writer == nil || s.CompletionCycle == 0 {
			return fmt.Errorf("sprite runtime chain incomplete")
		}
	}
	if r.SpriteBaseline.Applied || r.SpriteBaseline.Before != r.SpriteBaseline.After {
		return fmt.Errorf("sprite baseline was edited")
	}
	if !reflect.DeepEqual(r.Sprite.Link, r.SpriteBaseline.Link) || !reflect.DeepEqual(r.Sprite.Explanation, r.SpriteBaseline.Explanation) {
		return fmt.Errorf("sprite baseline evidence differs")
	}
	s := r.Sprite
	if s.Before != s.Link.Writer.Value {
		return fmt.Errorf("sprite source byte differs from writer")
	}
	want := s.Before
	if c.SpriteEdit.Large != nil {
		if *c.SpriteEdit.Large {
			want |= s.Mask
		} else {
			want &^= s.Mask
		}
	}
	if s.After != want || s.Applied != (c.SpriteEdit.Large != nil) || s.Mask != uint8(2<<uint((c.SpriteEdit.Sprite%4)*2)) {
		return fmt.Errorf("sprite size intervention differs")
	}
	return checkFrames(r, c)
}

// SpriteID returns the single operator-selected OAM object identity.
func (e *Experiments) SpriteID() string { return e.spriteID }
