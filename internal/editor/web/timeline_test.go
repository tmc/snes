package web

import (
	"context"
	"strings"
	"testing"

	"github.com/tmc/snes/internal/editor/machinebranch"
)

func TestExperimentTimeline(t *testing.T) {
	e := experimentFixture(t, func(ctx context.Context, c machinebranch.Config) (*machinebranch.Result, error) {
		if !c.InstructionTimeline {
			t.Error("editor did not request measured timeline")
		}
		r, err := fakeMachine(ctx, c)
		r.Timeline = &machinebranch.InstructionTimeline{Frames: []machinebranch.InstructionFrame{{RelativeFrame: 0, Sites: []machinebranch.InstructionSite{{Address: 0x008008, Count: 1}}}}}
		return r, err
	})
	j, err := e.start(6)
	if err != nil {
		t.Fatal(err)
	}
	j = waitExperiment(t, e, j.ID)
	if j.Status != "complete" || j.Edited.Timeline.Frames[0].Sites[0].Count != 1 {
		t.Fatalf("timeline not published: %+v", j)
	}
	for _, kind := range []string{"count", "frame", "address", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			cfg := e.config
			cfg.InstructionTimeline = true
			r, _ := fakeMachine(context.Background(), cfg)
			r.Timeline = &machinebranch.InstructionTimeline{Frames: []machinebranch.InstructionFrame{{RelativeFrame: 0, Sites: []machinebranch.InstructionSite{{Address: 0x008008, Count: 1}}}}}
			switch kind {
			case "missing":
				r.Timeline = nil
			case "count":
				r.Timeline.Frames[0].Sites[0].Count++
			case "frame":
				r.Timeline.Frames[0].RelativeFrame = 1
			case "address":
				r.Timeline.Frames[0].Sites[0].Address = 0x1000000
			case "duplicate":
				r.Timeline.Frames[0].Sites = append(r.Timeline.Frames[0].Sites, r.Timeline.Frames[0].Sites[0])
			}
			if checkMachineResult(r, cfg) == nil {
				t.Fatal("accepted inconsistent timeline")
			}
		})
	}
}

func TestTimelinePageBoundaries(t *testing.T) {
	for _, want := range []string{"id=\"jobslider\"", "id=\"heatstrip\"", "signal.aborted||epoch!==viewEpoch", "imageCache.size>12", "row.textContent=line", "Known zero compiled executions", "Frame not captured", "Execution capture unavailable", "inspectJob(j,'/api/sprite-job/frame')"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s", want)
		}
	}
}
