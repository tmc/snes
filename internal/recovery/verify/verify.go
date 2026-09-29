package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
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
	Outcome            Outcome           `json:"outcome"`
	TotalExpectedBytes int64             `json:"total_expected_bytes"`
	TotalActualBytes   int64             `json:"total_actual_bytes"`
	ExpectedSHA256     string            `json:"expected_sha256"`
	ActualSHA256       string            `json:"actual_sha256,omitempty"`
	MismatchCount      int64             `json:"mismatch_count"`
	Mismatches         []Mismatch        `json:"mismatches,omitempty"`
	AssemblerPath      string            `json:"assembler_path,omitempty"`
	AssemblerSHA256    string            `json:"assembler_sha256,omitempty"`
	AssemblerVersion   string            `json:"assembler_version,omitempty"`
	SourceHashes       map[string]string `json:"source_hashes,omitempty"`
	DurationMs         int64             `json:"duration_ms,omitempty"`
	Error              string            `json:"error,omitempty"`
}

// Config specifies verification parameters.
type Config struct {
	MaxReportedMismatches int
	AssemblerPath         string
	AssemblerArgs         []string
	SourceHashes          map[string]string
	ToolchainCmd          []string
	WorkingDir            string
	LogWriter             io.Writer
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

	receipt := &Receipt{
		TotalExpectedBytes: int64(len(originalROM)),
		ExpectedSHA256:     expSHA,
		SourceHashes:       cfg.SourceHashes,
	}

	// Determine command to run.
	var binPath string
	var args []string
	if cfg.AssemblerPath != "" {
		binPath = cfg.AssemblerPath
		args = cfg.AssemblerArgs
	} else if len(cfg.ToolchainCmd) > 0 {
		binPath = cfg.ToolchainCmd[0]
		args = cfg.ToolchainCmd[1:]
	}

	// Delete any existing/stale rebuilt file before running assembler.
	if binPath != "" && rebuiltPath != "" {
		_ = os.Remove(rebuiltPath)
	}

	if binPath != "" {
		// Resolve binary and compute its identity.
		resolvedPath, err := exec.LookPath(binPath)
		if err != nil {
			receipt.Outcome = OutcomeBuildFailed
			receipt.Error = fmt.Sprintf("assembler not found: %v", err)
			return receipt, nil
		}
		receipt.AssemblerPath = resolvedPath

		binBytes, err := os.ReadFile(resolvedPath)
		if err == nil {
			sum := sha256.Sum256(binBytes)
			receipt.AssemblerSHA256 = hex.EncodeToString(sum[:])
		}

		// Query version.
		vCmd := exec.CommandContext(ctx, resolvedPath, "-version")
		if vOut, err := vCmd.Output(); err == nil {
			receipt.AssemblerVersion = strings.TrimSpace(string(vOut))
		}

		// Run assembler toolchain without shell.
		startTime := time.Now()
		cmd := exec.CommandContext(ctx, resolvedPath, args...)
		if cfg.WorkingDir != "" {
			cmd.Dir = cfg.WorkingDir
		}

		var outputBuf bytes.Buffer
		var outWriter io.Writer = &outputBuf
		if cfg.LogWriter != nil {
			outWriter = io.MultiWriter(&outputBuf, cfg.LogWriter)
		}
		cmd.Stdout = outWriter
		cmd.Stderr = outWriter

		runErr := cmd.Run()
		receipt.DurationMs = time.Since(startTime).Milliseconds()

		if runErr != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
				receipt.Outcome = OutcomeTimedOut
				receipt.Error = fmt.Sprintf("assembler timed out: %v", runErr)
				return receipt, nil
			}
			receipt.Outcome = OutcomeBuildFailed
			receipt.Error = fmt.Sprintf("assembler failed (%v): %s", runErr, outputBuf.String())
			return receipt, nil
		}
	}

	// Read and verify rebuilt file.
	rebuiltBytes, err := os.ReadFile(rebuiltPath)
	if err != nil {
		if os.IsNotExist(err) {
			receipt.Outcome = OutcomeBuildFailed
			receipt.Error = fmt.Sprintf("rebuilt rom does not exist at %s", rebuiltPath)
			return receipt, nil
		}
		return nil, fmt.Errorf("verify: read rebuilt rom: %w", err)
	}

	compReceipt := CompareBytes(originalROM, rebuiltBytes, cfg.MaxReportedMismatches)
	compReceipt.AssemblerPath = receipt.AssemblerPath
	compReceipt.AssemblerSHA256 = receipt.AssemblerSHA256
	compReceipt.AssemblerVersion = receipt.AssemblerVersion
	compReceipt.SourceHashes = receipt.SourceHashes
	compReceipt.DurationMs = receipt.DurationMs

	return compReceipt, nil
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
