// Package main provides the snesreaders CLI tool.
//
// Deprecated: Use 'snesdasm readers' instead.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/tmc/snes/internal/provenance"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "snesreaders:", err)
		os.Exit(1)
	}
}

func run(args []string, out, diagnostics io.Writer) error {
	fmt.Fprintln(diagnostics, "warning: snesreaders is deprecated; use 'snesdasm readers' instead")
	fs := flag.NewFlagSet("snesreaders", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	path := fs.String("window", "", "complete observation window JSON file")
	filePin := fs.String("window-sha256", "", "externally pinned raw file SHA-256")
	writer := fs.String("writer", "", "WRAM write event ID, including zero")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *path == "" || len(*filePin) != 64 || *writer == "" {
		return fmt.Errorf("window, file SHA-256 and writer event ID required")
	}
	id, err := strconv.ParseUint(*writer, 10, 64)
	if err != nil {
		return fmt.Errorf("writer event ID: %w", err)
	}
	frontier, err := provenance.ReadFrontierFile(*path, *filePin, id)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(frontier)
}
