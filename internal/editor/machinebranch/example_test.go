package machinebranch_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

func ExampleRun() {
	_, err := machinebranch.Run(context.Background(), machinebranch.Config{Mode: "generated_c"})
	fmt.Println(errors.Is(err, machinebranch.ErrGeneratedCBridge))
	// Output: true
}

func ExamplePrepareRecovered() {
	_, err := machinebranch.PrepareRecovered([]byte{0}, 5)
	fmt.Println(err != nil)
	// Output: true
}

func ExampleRecoveredConfig() {
	var pins machinebranch.RecoveredConfig
	fmt.Println(pins.SourceSHA256 == "")
	// Output: true
}
