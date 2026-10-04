package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/tmc/snes/internal/recovery/analysis/planner"
)

func runPlan(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("snesdasm plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectDir = fs.String("project", "", "path to project directory with recovery.json (required)")
		format     = fs.String("format", "text", "output format: text|json")
		limit      = fs.Int("limit", 20, "maximum experiments to return (0 for all)")
		minScore   = fs.Float64("min-score", 0, "minimum priority score threshold")
	)
	fs.Usage = func() {
		subcommandUsage(fs,
			"snesdasm plan -project dir [flags]",
			"Plan and prioritize executable experiments to resolve recovery frontiers.",
			"snesdasm plan -project game_dasm",
			"snesdasm plan -project game_dasm -format json",
			"snesdasm plan -project game_dasm -limit 10",
		)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectDir == "" {
		return fmt.Errorf("-project flag is required; run 'snesdasm help plan' for usage")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("invalid format %q: expected text or json", *format)
	}
	if *limit < 0 {
		return fmt.Errorf("invalid -limit %d: must be >= 0", *limit)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}

	doc, err := loadDoc(*projectDir)
	if err != nil {
		return err
	}

	plan, err := planner.Plan(doc, planner.Options{
		MaxExperiments: *limit,
		MinScore:       *minScore,
	})
	if err != nil {
		return err
	}

	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(plan)
	}

	fmt.Fprintf(stdout, "Recovery experiment plan (%d total frontiers, %d high priority):\n", plan.TotalFrontiers, plan.HighPriorityCount)
	for i, exp := range plan.Experiments {
		fmt.Fprintf(stdout, "  #%d [%s] $%06X (score: %.1f, gain: %.1f, cost: %.1f)\n",
			i+1, exp.Classification, exp.Address, exp.PriorityScore, exp.InformationGain, exp.VerificationCost)
		fmt.Fprintf(stdout, "     action: %s\n", exp.RecommendedAction)
		if exp.Reason != "" {
			fmt.Fprintf(stdout, "     reason: %s\n", exp.Reason)
		}
	}
	return nil
}
