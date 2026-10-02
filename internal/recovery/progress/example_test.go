package progress_test

import (
	"fmt"
	"github.com/tmc/snes/internal/recovery/progress"
)

func ExampleReport_Evidence() {
	report := new(progress.Report)
	_, ok := report.Evidence(0)
	fmt.Println(ok)
	// Output: false
}
