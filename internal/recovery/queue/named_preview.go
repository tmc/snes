package queue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/decomp"
)

// PreviewNamedBinding derives a digest for operator review. It does not admit
// cases or qualify generated C. Run checks this digest against policy later.
func PreviewNamedBinding(cfg Config) (string, error) {
	if cfg.ProjectDir == "" || cfg.ROMPath == "" || cfg.CasesPath == "" || cfg.CandidatePath == "" || cfg.NamedSymbolsPath == "" || cfg.MaxSteps < 1 || cfg.MaxSteps > 1000000 {
		return "", fmt.Errorf("queue: incomplete named binding preview")
	}
	var pins []pinnedInput
	docBytes, err := readInput(filepath.Join(cfg.ProjectDir, "recovery.json"), &pins)
	if err != nil {
		return "", err
	}
	doc, err := recovery.Decode(bytes.NewReader(docBytes))
	if err != nil {
		return "", err
	}
	revision := recovery.ComputeProjectRevision(cfg.ProjectDir, doc)
	romBytes, err := readInput(cfg.ROMPath, &pins)
	if err != nil {
		return "", err
	}
	rom, err := recovery.AdmitROM(bytes.NewReader(romBytes), recovery.AdmissionOptions{})
	if err != nil {
		return "", err
	}
	if rom.Identity.NormalizedSHA256 != doc.ROM.NormalizedSHA256 || doc.ROM.Mapper != "lorom" {
		return "", fmt.Errorf("queue: ROM identity or mapper differs from recovery document")
	}
	profileBytes, err := readInput(cfg.CandidatePath, &pins)
	if err != nil {
		return "", err
	}
	if len(profileBytes) > 1<<20 {
		return "", fmt.Errorf("queue: candidate profile exceeds 1 MiB")
	}
	candidate, err := readCandidateProfile(profileBytes)
	if err != nil {
		return "", err
	}
	caseBytes, err := readInput(cfg.CasesPath, &pins)
	if err != nil {
		return "", err
	}
	cases, err := readCases(caseBytes)
	if err != nil {
		return "", err
	}
	var selected *decomp.ReplayCase
	for i := range cases {
		c := &cases[i]
		if uint32(c.InitialState.PB)<<16|uint32(c.InitialState.PC) != candidate.Entry {
			continue
		}
		if c.RoutineID != candidate.ID || c.ROMSHA256 != rom.Identity.NormalizedSHA256 {
			return "", fmt.Errorf("queue: preview case identity differs from candidate or ROM")
		}
		if selected != nil && entryContext(c.InitialState) != entryContext(selected.InitialState) {
			return "", fmt.Errorf("queue: preview cases have conflicting entry widths")
		}
		selected = c
	}
	if selected == nil {
		return "", fmt.Errorf("queue: no preview case for candidate entry")
	}
	symbolBytes, err := readInput(cfg.NamedSymbolsPath, &pins)
	if err != nil {
		return "", err
	}
	if len(symbolBytes) > 1<<20 {
		return "", fmt.Errorf("queue: named symbols exceed 1 MiB")
	}
	var symbols []decomp.ByteSymbol
	d := json.NewDecoder(bytes.NewReader(symbolBytes))
	d.DisallowUnknownFields()
	if err := d.Decode(&symbols); err != nil {
		return "", fmt.Errorf("queue: decode named symbols: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF || len(symbols) == 0 {
		return "", fmt.Errorf("queue: invalid named symbols JSON")
	}
	p := candidate.Proposal
	off := int((p.Start>>16&0x7f)*0x8000 + (p.Start & 0x7fff))
	if off < 0 || off+int(p.End-p.Start) > len(rom.NormalizedROM) {
		return "", fmt.Errorf("queue: candidate region outside supplied ROM")
	}
	region, err := decodeCandidateRegion(rom.NormalizedROM, p, entryContext(selected.InitialState), cfg.MaxSteps)
	if err != nil {
		return "", fmt.Errorf("queue: preview decode: %w", err)
	}
	region.ROMBytes = nil
	digest, err := decomp.NamedRegionBindingSHA256(region, symbols)
	if err != nil {
		return "", err
	}
	if err := checkInputs(pins); err != nil {
		return "", err
	}
	if recovery.ComputeProjectRevision(cfg.ProjectDir, doc) != revision {
		return "", fmt.Errorf("queue: project changed during preview")
	}
	return digest, nil
}
