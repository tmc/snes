// snesrecover advances a pinned, resumable recovery task.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tmc/snes/internal/recovery/workflow"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "snesrecover: %v\n", err)
		os.Exit(1)
	}
}
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesrecover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("task", "", "durable task directory")
	config := fs.String("config", "", "pinned task configuration")
	configSHA := fs.String("config-sha256", "", "SHA-256 of configuration")
	evidence := fs.String("evidence", "", "explicit producer evidence delivery JSON")
	evidenceSHA := fs.String("evidence-sha256", "", "SHA-256 of evidence delivery")
	policy := fs.String("policy", "", "explicit operator-reviewed policy JSON")
	policySHA := fs.String("policy-sha256", "", "SHA-256 of reviewed policy")
	transitions := fs.Int("transitions", 5, "maximum journal transitions (1..10)")
	timeout := fs.Duration("timeout", 2*time.Minute, "deadline checked between extraction phases (1s..10m)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *dir == "" || *config == "" || *configSHA == "" {
		return fmt.Errorf("-task, -config and -config-sha256 are required")
	}
	if (*evidence == "") != (*evidenceSHA == "") || (*policy == "") != (*policySHA == "") {
		return fmt.Errorf("delivery paths and SHA-256 must be supplied together")
	}
	if *timeout < time.Second || *timeout > 10*time.Minute {
		return fmt.Errorf("timeout outside bounds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := workflow.Run(ctx, workflow.Options{Dir: *dir, Config: workflow.Input{Path: *config, SHA256: *configSHA}, Evidence: workflow.Input{Path: *evidence, SHA256: *evidenceSHA}, Policy: workflow.Input{Path: *policy, SHA256: *policySHA}, MaxTransitions: *transitions})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
