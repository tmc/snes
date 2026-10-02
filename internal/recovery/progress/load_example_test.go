package progress_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/snes/internal/recovery"
	"github.com/tmc/snes/internal/recovery/progress"
	"github.com/tmc/snes/internal/recovery/workflow"
)

func ExampleLoad() {
	dir, err := os.MkdirTemp("", "snes-progress-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(dir)
	doc := recovery.NewDocument(recovery.ROMIdentity{NormalizedSHA256: strings.Repeat("a", 64), NormalizedSize: 32768})
	db, _ := json.Marshal(doc)
	dp := filepath.Join(dir, "recovery.json")
	os.WriteFile(dp, db, 0600)
	cfg := progress.Config{Schema: "snes-progress-config-v1", Recovery: workflow.Input{Path: dp, SHA256: fmt.Sprintf("%x", sha256.Sum256(db))}}
	cb, _ := json.Marshal(cfg)
	cp := filepath.Join(dir, "config.json")
	os.WriteFile(cp, cb, 0600)
	report, err := progress.Load(context.Background(), cp, fmt.Sprintf("%x", sha256.Sum256(cb)))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(report.Metrics[0].Count == nil, *report.Metrics[1].Count)
	// Output: true 0
}
