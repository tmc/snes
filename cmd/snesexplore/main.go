// snesexplore measures bounded capture gaps and checkpoint controller branches.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/tmc/snes/internal/exploration"
	"github.com/tmc/snes/internal/recovery/workflow"
	"io"
	"os"
)

func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "snesexplore:", e)
		os.Exit(1)
	}
}
func run(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("snesexplore", flag.ContinueOnError)
	path := fs.String("config", "", "pinned campaign JSON")
	sha := fs.String("config-sha256", "", "expected campaign SHA-256")
	out := fs.String("out", "", "new absolute output directory")
	execute := fs.Bool("execute", false, "run explicit bounded capture and controller campaign")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	r, e := exploration.Run(context.Background(), *out, workflow.Input{Path: *path, SHA256: *sha}, *execute)
	if e != nil {
		return e
	}
	return json.NewEncoder(w).Encode(r)
}
