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
