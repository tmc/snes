// snessemantic emits opt-in executable local-value C from explicitly pinned ROM.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/snes/internal/recovery/decomp"
)

type config struct {
	ROM       string                 `json:"rom"`
	ROMSHA256 string                 `json:"rom_sha256"`
	Region    decomp.ConnectedConfig `json:"region"`
}

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: snessemantic -config pinned.json -out new-directory")
		flag.PrintDefaults()
	}
	path := flag.String("config", "", "pinned ROM and connected-region configuration")
	out := flag.String("out", "", "new output directory")
	flag.Parse()
	if err := run(*path, *out); err != nil {
		fmt.Fprintln(os.Stderr, "snessemantic:", err)
		os.Exit(1)
	}
}

func run(path, out string) error {
	if path == "" || out == "" {
		return fmt.Errorf("config and out are required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c config
	if err = json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if c.ROMSHA256 == "" {
		return fmt.Errorf("missing ROM sha256")
	}
	rom, err := os.ReadFile(c.ROM)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(rom)) != c.ROMSHA256 {
		return fmt.Errorf("ROM sha256 differs")
	}
	c.Region.ROM = rom
	region, err := decomp.DecodeConnected(c.Region)
	if err != nil {
		return err
	}
	source, err := decomp.GenerateSemanticRegionC(region)
	if err != nil {
		return err
	}
	original, err := decomp.GenerateRegionC(region)
	if err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(struct {
		ROMSHA256      string                `json:"rom_sha256"`
		ConfigSHA256   string                `json:"config_sha256"`
		Transformation decomp.SemanticSource `json:"transformation"`
		Qualification  string                `json:"qualification"`
	}{c.ROMSHA256, fmt.Sprintf("%x", sha256.Sum256(b)), source, "generated only; requires fresh execution qualification"}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return fmt.Errorf("new output directory: %w", err)
	}
	for _, f := range []struct{ name, body string }{{"original.c", original}, {"semantic.c", source.Source}, {"manifest.json", string(manifest)}} {
		if err = os.WriteFile(filepath.Join(out, f.name), []byte(f.body), 0600); err != nil {
			return err
		}
	}
	return nil
}
