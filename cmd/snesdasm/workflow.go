package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/tmc/snes/internal/recovery/workflow"
)

func runWorkflow(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm workflow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm workflow [flags]",
			"Advance a pinned resumable recovery task or batch of tasks.",
			"snesdasm workflow -task ./task -config config.json -config-sha256 $SHA",
			"snesdasm workflow -batch -out ./batch -config batch.json -config-sha256 $SHA",
		)
	}

	batch := fs.Bool("batch", false, "run batch recovery mode across multiple tasks")
	out := fs.String("out", "", "batch output directory")
	manifest := fs.String("manifest-sha256", "", "externally measured readiness SHA-256 for batch")
	dir := fs.String("task", "", "durable task directory")
	config := fs.String("config", "", "pinned task configuration")
	configSHA := fs.String("config-sha256", "", "SHA-256 of configuration")
	evidence := fs.String("evidence", "", "explicit producer evidence delivery JSON")
	evidenceSHA := fs.String("evidence-sha256", "", "SHA-256 of evidence delivery")
	policy := fs.String("policy", "", "explicit operator-reviewed policy JSON")
	policySHA := fs.String("policy-sha256", "", "SHA-256 of reviewed policy")
	transitions := fs.Int("transitions", 5, "maximum journal transitions (1..10)")
	timeout := fs.Duration("timeout", 2*time.Minute, "execution deadline")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments; run 'snesdasm help workflow' for usage")
	}

	if *batch {
		if *out == "" || *config == "" || *configSHA == "" {
			return fmt.Errorf("-out, -config, and -config-sha256 are required for batch workflow")
		}
		if *timeout < time.Second || *timeout > time.Hour {
			return fmt.Errorf("batch timeout outside bounds (1s..1h)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		var r *workflow.BatchReport
		var err error
		if *manifest == "" {
			r, err = workflow.RunBatch(ctx, *out, workflow.Input{Path: *config, SHA256: *configSHA})
		} else {
			r, err = workflow.ResumeBatch(ctx, *out, workflow.Input{Path: *config, SHA256: *configSHA}, *manifest)
		}
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}

	if *dir == "" || *config == "" || *configSHA == "" {
		return fmt.Errorf("-task, -config and -config-sha256 are required; run 'snesdasm help workflow' for usage")
	}
	if (*evidence == "") != (*evidenceSHA == "") || (*policy == "") != (*policySHA == "") {
		return fmt.Errorf("delivery paths and SHA-256 must be supplied together")
	}
	if *timeout < time.Second || *timeout > 10*time.Minute {
		return fmt.Errorf("timeout outside bounds (1s..10m)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := workflow.Run(ctx, workflow.Options{
		Dir:            *dir,
		Config:         workflow.Input{Path: *config, SHA256: *configSHA},
		Evidence:       workflow.Input{Path: *evidence, SHA256: *evidenceSHA},
		Policy:         workflow.Input{Path: *policy, SHA256: *policySHA},
		MaxTransitions: *transitions,
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
