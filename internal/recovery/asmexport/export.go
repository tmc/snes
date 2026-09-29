package asmexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/snes/internal/recovery"
)

// ErrTargetExists indicates the target export directory exists and is non-empty.
var ErrTargetExists = errors.New("asmexport: target directory already exists and is non-empty")

// Config specifies assembly export configuration.
type Config struct {
	ProjectName string
	EntryAsm    string
	Overwrite   bool
}

// Result describes the generated export bundle.
type Result struct {
	ManifestPath  string            `json:"manifest_path"`
	SourcePaths   []string          `json:"source_paths"`
	SourceHashes  map[string]string `json:"source_hashes"`
	BytesExported int64             `json:"bytes_exported"`
}

// Export writes a complete Futaba assembly project to targetDir.
func Export(targetDir string, doc *recovery.Document, rom []byte, cfg Config) (*Result, error) {
	if targetDir == "" {
		return nil, errors.New("asmexport: target directory cannot be empty")
	}
	if doc == nil {
		return nil, errors.New("asmexport: document cannot be nil")
	}
	if len(rom) == 0 {
		return nil, errors.New("asmexport: rom bytes cannot be empty")
	}
	if cfg.ProjectName == "" {
		cfg.ProjectName = "recovery"
	}
	if cfg.EntryAsm == "" {
		cfg.EntryAsm = "main.asm"
	}

	return exportToDir(targetDir, doc, rom, cfg)
}

func exportToDir(targetDir string, doc *recovery.Document, rom []byte, cfg Config) (*Result, error) {
	// Guard against accidental overwrite.
	entries, err := os.ReadDir(targetDir)
	if err == nil && len(entries) > 0 && !cfg.Overwrite {
		return nil, fmt.Errorf("%w: %s", ErrTargetExists, targetDir)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("asmexport: create dir: %w", err)
	}

	bankSize := 32 * 1024
	if len(rom)%bankSize != 0 {
		return nil, fmt.Errorf("asmexport: rom length %d is not a multiple of %d", len(rom), bankSize)
	}
	numBanks := len(rom) / bankSize

	var sourcePaths []string
	sourceHashes := make(map[string]string)

	writeFile := func(relPath string, content []byte) error {
		fullPath := filepath.Join(targetDir, relPath)
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return fmt.Errorf("asmexport: write %s: %w", relPath, err)
		}
		sourcePaths = append(sourcePaths, relPath)
		sum := sha256.Sum256(content)
		sourceHashes[relPath] = hex.EncodeToString(sum[:])
		return nil
	}

	// Index instructions by offset.
	instByOffset := make(map[uint32]recovery.Instruction)
	if doc != nil {
		for _, inst := range doc.Instructions {
			instByOffset[inst.Offset] = inst
		}
	}

	// 1. Emit bank files.
	var bankIncludes []string
	for b := 0; b < numBanks; b++ {
		bankFileName := fmt.Sprintf("bank_%02x.asm", b)
		bankIncludes = append(bankIncludes, bankFileName)

		var bankByte int
		if b < 0x7E {
			bankByte = b
		} else if b == 0x7E {
			bankByte = 0xFE
		} else if b == 0x7F {
			bankByte = 0xFF
		} else {
			return nil, fmt.Errorf("asmexport: bank %d exceeds supported LoROM mapping", b)
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("; Bank $%02X\n", bankByte))
		sb.WriteString(fmt.Sprintf("org $%02X8000\n\n", bankByte))

		bankStart := uint32(b * bankSize)
		bankEnd := bankStart + uint32(bankSize)
		curr := bankStart

		for curr < bankEnd {
			if inst, ok := instByOffset[curr]; ok {
				raw, err := hex.DecodeString(inst.Bytes)
				if err == nil && len(raw) > 0 && curr+uint32(len(raw)) <= bankEnd {
					line, fmtErr := FormatInstructionASM(inst)
					if fmtErr == nil {
						sb.WriteString(fmt.Sprintf("  %s\n", line))
						curr += uint32(len(raw))
						continue
					}
				}
			}

			// Find next instruction offset (if any) or bankEnd.
			nextInst := bankEnd
			for off := curr + 1; off < bankEnd; off++ {
				if _, ok := instByOffset[off]; ok {
					nextInst = off
					break
				}
			}

			// Emit data bytes up to nextInst in chunks of at most 16 bytes.
			for curr < nextInst {
				chunkSize := int(nextInst - curr)
				if chunkSize > 16 {
					chunkSize = 16
				}
				chunk := rom[curr : curr+uint32(chunkSize)]
				hexParts := make([]string, len(chunk))
				for j, byteVal := range chunk {
					hexParts[j] = fmt.Sprintf("$%02X", byteVal)
				}
				sb.WriteString("db " + strings.Join(hexParts, ", ") + "\n")
				curr += uint32(chunkSize)
			}
		}

		if err := writeFile(bankFileName, []byte(sb.String())); err != nil {
			return nil, err
		}
	}

	// 2. Emit main.asm.
	var mainBuf strings.Builder
	mainBuf.WriteString("lang wdc65816\n\n")
	for _, inc := range bankIncludes {
		mainBuf.WriteString(fmt.Sprintf("incsrc %s\n", inc))
	}
	if err := writeFile(cfg.EntryAsm, []byte(mainBuf.String())); err != nil {
		return nil, err
	}

	// 3. Emit manifest .futaba.
	manifestName := cfg.ProjectName + ".futaba"
	var mfBuf strings.Builder
	// FUTABA REQUIREMENT: Line 1 must be [FUTABA:assemble]
	mfBuf.WriteString("[FUTABA:assemble]\n\n")
	mfBuf.WriteString("assembly:\n")
	mfBuf.WriteString(fmt.Sprintf("\tentry = %s\n", cfg.EntryAsm))
	mfBuf.WriteString("\tmapmode = lorom\n")
	if err := writeFile(manifestName, []byte(mfBuf.String())); err != nil {
		return nil, err
	}

	// 4. Emit export.json.
	exportMap := struct {
		ExporterVersion  string            `json:"exporter_version"`
		NormalizedSHA256 string            `json:"normalized_sha256"`
		SourceFiles      map[string]string `json:"source_files"`
		TotalBytes       int64             `json:"total_bytes"`
		Completed        bool              `json:"completed"`
	}{
		ExporterVersion:  "1.0.0",
		NormalizedSHA256: doc.ROM.NormalizedSHA256,
		SourceFiles:      sourceHashes,
		TotalBytes:       int64(len(rom)),
		Completed:        true,
	}

	exportJSON, err := json.MarshalIndent(exportMap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("asmexport: marshal export map: %w", err)
	}
	if err := writeFile("export.json", append(exportJSON, '\n')); err != nil {
		return nil, err
	}

	return &Result{
		ManifestPath:  filepath.Join(targetDir, manifestName),
		SourcePaths:   sourcePaths,
		SourceHashes:  sourceHashes,
		BytesExported: int64(len(rom)),
	}, nil
}
