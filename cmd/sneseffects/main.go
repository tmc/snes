// Sneseffects compares externally pinned complete runtime observation windows.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tmc/snes/internal/provenance"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sneseffects:", err)
		os.Exit(1)
	}
}

func readWindow(path, pin string) (provenance.Window, error) {
	var w provenance.Window
	f, err := os.Open(path)
	if err != nil {
		return w, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 512<<20+1))
	if err != nil {
		return w, err
	}
	if len(b) > 512<<20 || fmt.Sprintf("%x", sha256.Sum256(b)) != pin {
		return w, fmt.Errorf("window file identity or size differs")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&w); err != nil {
		return w, err
	}
	if d.Decode(new(any)) != io.EOF {
		return w, fmt.Errorf("trailing window data")
	}
	return w, nil
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("sneseffects", flag.ContinueOnError)
	original := fs.String("original", "", "original window JSON file")
	originalSHA := fs.String("original-sha256", "", "externally pinned original file SHA-256")
	edited := fs.String("edited", "", "edited window JSON file")
	editedSHA := fs.String("edited-sha256", "", "externally pinned edited file SHA-256")
	frame := fs.Int("frame", -1, "relative host frame to explain")
	start := fs.Uint("routine-start", 0, "source routine start CPU address")
	end := fs.Uint("routine-end", 0, "exclusive source routine end CPU address")
	sprite := fs.Int("sprite", 0, "selected OAM entry; no pixel ownership inferred")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *original == "" || *edited == "" || len(*originalSHA) != 64 || len(*editedSHA) != 64 || *frame < 0 || *start >= *end || *end > 1<<24 {
		return fmt.Errorf("pinned windows, frame and routine interval required")
	}
	a, err := readWindow(*original, *originalSHA)
	if err != nil {
		return fmt.Errorf("original: %w", err)
	}
	b, err := readWindow(*edited, *editedSHA)
	if err != nil {
		return fmt.Errorf("edited: %w", err)
	}
	apin, err := provenance.WindowSHA256(a)
	if err != nil {
		return err
	}
	bpin, err := provenance.WindowSHA256(b)
	if err != nil {
		return err
	}
	result, err := provenance.Compare(a, b, apin, bpin, provenance.Selection{Frame: *frame, RoutineStart: uint32(*start), RoutineEnd: uint32(*end), Sprite: *sprite})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
