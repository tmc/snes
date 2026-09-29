package asmexport_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/asmexport"
)

func ExampleExport() {
	tmpDir, err := os.MkdirTemp("", "recovery-example-*")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	rom := make([]byte, 32768)
	ident := recovery.ComputeROMIdentity(rom, rom, "none", "lorom")
	doc := recovery.NewDocument(ident)

	outDir := filepath.Join(tmpDir, "export")
	res, err := asmexport.Export(outDir, doc, rom, asmexport.Config{
		ProjectName: "game",
	})
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Exported bytes:", res.BytesExported)
	fmt.Println("Manifest file:", filepath.Base(res.ManifestPath))

	// Output:
	// Exported bytes: 32768
	// Manifest file: game.futaba
}
