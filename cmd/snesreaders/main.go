// Snesreaders explains observed readers of a selected WRAM byte version.
package main

import (
	"bytes"
	"crypto/sha256"
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
	f, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (512<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 512<<20 || fmt.Sprintf("%x", sha256.Sum256(data)) != *filePin {
		return fmt.Errorf("window file identity or size differs")
	}
	var w provenance.Window
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return fmt.Errorf("window JSON: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing window data")
	}
	pin, err := provenance.WindowSHA256(w)
	if err != nil {
		return err
	}
	frontier, err := provenance.ReadFrontier(w, pin, id)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(frontier)
}
