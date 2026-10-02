package parity

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func validateAudioRMSGoldens(goldens audioRMSGoldens) error {
	if len(goldens.Cases) == 0 {
		return fmt.Errorf("cases is empty")
	}
	for key, golden := range goldens.Cases {
		if err := validateCommonReferenceManifest(key, golden.ROM, golden.ROMHash, golden.Core, golden.CoreVersion, golden.CorePathHint, golden.Comment); err != nil {
			return err
		}
		if golden.Frames <= 0 {
			return fmt.Errorf("%s: frames must be positive", key)
		}
		if math.IsNaN(golden.RMS) || math.IsInf(golden.RMS, 0) || golden.RMS < 0 {
			return fmt.Errorf("%s: rms must be finite and non-negative", key)
		}
		if math.IsNaN(golden.Tolerance) || math.IsInf(golden.Tolerance, 0) || golden.Tolerance <= 0 {
			return fmt.Errorf("%s: tolerance must be finite and positive", key)
		}
	}
	return nil
}

func validateCommonReferenceManifest(key, rom, romHash, core, coreVersion, corePathHint, comment string) error {
	parts := strings.Split(key, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("%s: key must be case/core", key)
	}
	if rom == "" {
		return fmt.Errorf("%s: rom is empty", key)
	}
	if filepath.Base(rom) != rom {
		return fmt.Errorf("%s: rom must be a basename", key)
	}
	if !isSHA256Hex(romHash) {
		return fmt.Errorf("%s: rom_sha256 is not sha256 hex", key)
	}
	if core == "" {
		return fmt.Errorf("%s: core is empty", key)
	}
	if core != parts[1] {
		return fmt.Errorf("%s: core %q does not match key", key, core)
	}
	if core != "bsnes" && core != "snes9x" && core != "ares" {
		return fmt.Errorf("%s: unknown core %q", key, core)
	}
	if coreVersion == "" {
		return fmt.Errorf("%s: core_version is empty", key)
	}
	if corePathHint == "" {
		return fmt.Errorf("%s: core_path_hint is empty", key)
	}
	if filepath.IsAbs(corePathHint) {
		return fmt.Errorf("%s: core_path_hint must be relative", key)
	}
	if strings.TrimSpace(comment) == "" {
		return fmt.Errorf("%s: comment is empty", key)
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			continue
		}
		return false
	}
	return true
}

func higanManifestCase(t *testing.T, name string) (higanTestROMCase, bool) {
	t.Helper()
	for _, tc := range readHiganTestROMManifest(t) {
		if tc.Name == name {
			return tc, true
		}
	}
	return higanTestROMCase{}, false
}
