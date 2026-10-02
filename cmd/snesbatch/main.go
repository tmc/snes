// snesbatch runs ten pinned recovery tasks and retains their raw evidence.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tmc/snes/internal/recovery/workflow"
)

func main() {
	fs := flag.NewFlagSet("snesbatch", flag.ExitOnError)
	out := fs.String("out", "", "absent output directory, or identical retained batch")
	config := fs.String("config", "", "pinned ten-task batch JSON")
	sha := fs.String("config-sha256", "", "exact SHA-256 of batch JSON")
	timeout := fs.Duration("timeout", 10*time.Minute, "batch deadline (1s..1h)")
	fs.Parse(os.Args[1:])
	if fs.NArg() != 0 || *timeout < time.Second || *timeout > time.Hour {
		fmt.Fprintln(os.Stderr, "snesbatch: invalid arguments or deadline")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	r, err := workflow.RunBatch(ctx, *out, workflow.Input{Path: *config, SHA256: *sha})
	if err != nil {
		fmt.Fprintf(os.Stderr, "snesbatch: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintf(os.Stderr, "snesbatch: %v\n", err)
		os.Exit(1)
	}
}
