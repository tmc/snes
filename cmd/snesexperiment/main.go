// Command snesexperiment compares a pinned original routine with an edited copy.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tmc/snes/internal/editor/replay"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "snesexperiment: %v\n", err)
		os.Exit(1)
	}
}
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesexperiment", flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", "", "explicit hashed experiment JSON configuration (required)")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall compile/replay timeout (0 < timeout <= 10m)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: snesexperiment -config experiment.json [-timeout 2m]\n\nRun a headless original-vs-edited source experiment. Original qualification is\nreported separately; edited C never inherits captured proof. No live replacement.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *config == "" {
		return fmt.Errorf("-config flag is required")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *timeout <= 0 || *timeout > 10*time.Minute {
		return fmt.Errorf("timeout out of range")
	}
	f, err := os.Open(*config)
	if err != nil {
		return err
	}
	cfg, err := replay.LoadConfig(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := replay.Run(ctx, cfg)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
