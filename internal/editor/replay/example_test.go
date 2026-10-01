package replay_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/tmc/snes/internal/editor/replay"
)

func ExampleLoadConfig() {
	cfg, err := replay.LoadConfig(strings.NewReader(`{"max_steps":100}`))
	fmt.Println(cfg.MaxSteps, err)
	// Output: 100 <nil>
}
func ExampleRun() {
	_, err := replay.Run(context.Background(), replay.Config{})
	fmt.Println(err)
	// Output: missing project, corpus, output, or revision
}
