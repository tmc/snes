package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Outcome represents the status of a verification run.
type Outcome string

const (
	// OutcomeMatched indicates rebuilt bytes matched original normalized bytes perfectly.
	OutcomeMatched Outcome = "matched"
	// OutcomeMismatch indicates rebuilt bytes differ from original normalized bytes.
	OutcomeMismatch Outcome = "mismatch"
	// OutcomeBuildFailed indicates the assembler toolchain failed.
	OutcomeBuildFailed Outcome = "build_failed"
	// OutcomeTimedOut indicates the verification or build process exceeded budget.
	OutcomeTimedOut Outcome = "timed_out"
	// OutcomeNotRun indicates the verification was not executed (e.g. missing toolchain).
	OutcomeNotRun Outcome = "not_run"
)

// Mismatch records a single differing byte between expected and actual ROMs.
type Mismatch struct {
	Offset      int64  `json:"offset"`
	Expected    byte   `json:"expected"`
	Actual      byte   `json:"actual"`
	SNESAddress uint32 `json:"snes_address"`
}

// Receipt is the machine-readable verification summary.
type Receipt struct {
	Outcome            Outcome    `json:"outcome"`
	TotalExpectedBytes int64      `json:"total_expected_bytes"`
	TotalActualBytes   int64      `json:"total_actual_bytes"`
	ExpectedSHA256     string     `json:"expected_sha256"`
	ActualSHA256       string     `json:"actual_sha256,omitempty"`
	MismatchCount      int64      `json:"mismatch_count"`
	Mismatches         []Mismatch `json:"mismatches,omitempty"`
	Error              string     `json:"error,omitempty"`
}

// Config specifies verification parameters.
type Config struct {
	MaxReportedMismatches int
	ToolchainCmd          []string
}

// Verify verifies a rebuilt ROM against the normalized original ROM.
func Verify(ctx context.Context, rebuiltPath string, originalROM []byte, cfg Config) (*Receipt, error) {
	if len(originalROM) == 0 {
		return nil, errors.New("verify: original rom cannot be empty")
	}
	if cfg.MaxReportedMismatches <= 0 {
		cfg.MaxReportedMismatches = 50
	}
	return verifyRebuilt(ctx, rebuiltPath, originalROM, cfg)
}

func verifyRebuilt(ctx context.Context, rebuiltPath string, originalROM []byte, cfg Config) (*Receipt, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	expSum := sha256.Sum256(originalROM)
	expSHA := hex.EncodeToString(expSum[:])

	// If a toolchain command is supplied, execute it first.
	if len(cfg.ToolchainCmd) > 0 {
		cmd := exec.CommandContext(ctx, cfg.ToolchainCmd[0], cfg.ToolchainCmd[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
				return &Receipt{
					Outcome:            OutcomeTimedOut,
					TotalExpectedBytes: int64(len(originalROM)),
					ExpectedSHA256:     expSHA,
					Error:              fmt.Sprintf("toolchain execution timed out: %v", err),
				}, nil
			}
			return &Receipt{
				Outcome:            OutcomeBuildFailed,
				TotalExpectedBytes: int64(len(originalROM)),
				ExpectedSHA256:     expSHA,
				Error:              fmt.Sprintf("toolchain failed (%v): %s", err, string(output)),
			}, nil
		}
	}

	rebuiltBytes, err := os.ReadFile(rebuiltPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Receipt{
				Outcome:            OutcomeBuildFailed,
				TotalExpectedBytes: int64(len(originalROM)),
				ExpectedSHA256:     expSHA,
				Error:              fmt.Sprintf("rebuilt rom does not exist at %s", rebuiltPath),
			}, nil
		}
		return nil, fmt.Errorf("verify: read rebuilt rom: %w", err)
	}

	return CompareBytes(originalROM, rebuiltBytes, cfg.MaxReportedMismatches), nil
}

// CompareBytes compares expected and actual byte slices and produces a Receipt.
func CompareBytes(expected, actual []byte, maxMismatches int) *Receipt {
	if maxMismatches <= 0 {
		maxMismatches = 50
	}

	expSum := sha256.Sum256(expected)
	actSum := sha256.Sum256(actual)

	receipt := &Receipt{
		TotalExpectedBytes: int64(len(expected)),
		TotalActualBytes:   int64(len(actual)),
		ExpectedSHA256:     hex.EncodeToString(expSum[:]),
		ActualSHA256:       hex.EncodeToString(actSum[:]),
	}

	minLen := len(expected)
	if len(actual) < minLen {
		minLen = len(actual)
	}

	var mismatches []Mismatch
	var mismatchCount int64

	for i := 0; i < minLen; i++ {
		if expected[i] != actual[i] {
			mismatchCount++
			if len(mismatches) < maxMismatches {
				mismatches = append(mismatches, Mismatch{
					Offset:      int64(i),
					Expected:    expected[i],
					Actual:      actual[i],
					SNESAddress: calculateLoROMSNESAddress(int64(i)),
				})
			}
		}
	}

	// Any length difference is counted as mismatches.
	if len(expected) != len(actual) {
		diff := int64(len(expected) - len(actual))
		if diff < 0 {
			diff = -diff
		}
		mismatchCount += diff
	}

	receipt.MismatchCount = mismatchCount
	receipt.Mismatches = mismatches

	if mismatchCount == 0 {
		receipt.Outcome = OutcomeMatched
	} else {
		receipt.Outcome = OutcomeMismatch
	}

	return receipt
}

func calculateLoROMSNESAddress(offset int64) uint32 {
	bank := uint32(offset / 32768)
	bankOffset := uint32(offset%32768) + 0x8000
	var bankByte uint32
	if bank < 0x7E {
		bankByte = bank
	} else if bank == 0x7E {
		bankByte = 0xFE
	} else if bank == 0x7F {
		bankByte = 0xFF
	} else {
		bankByte = bank
	}
	return (bankByte << 16) | bankOffset
}
