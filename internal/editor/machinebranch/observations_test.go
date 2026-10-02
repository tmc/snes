package machinebranch

import (
	"context"
	"reflect"
	"testing"

	"github.com/tmc/snes/internal/provenance"
)

func TestObservationParity(t *testing.T) {
	c, rom, _ := fixture(t)
	without, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Observation = &ObservationConfig{From: 0, To: 2, MaxEvents: 2000000, Selection: provenance.Selection{Frame: 0, RoutineStart: 0x808000, RoutineEnd: 0x808002, Sprite: 0}}
	with, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(without.Baseline, with.Baseline) || !reflect.DeepEqual(without.Replica, with.Replica) || !with.OriginalMatch || with.CapturedProofEligible {
		t.Fatal("observer changed machine state, journal or pixels")
	}
	if with.Observations == nil || len(with.Observations.Original.Events) == 0 || !with.Observations.Original.Complete {
		t.Fatal("missing observation")
	}
	if err := CheckObservations(with, rom); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"report", "frame", "event", "identity"} {
		original := with.Observations
		copy := *original
		copy.Original = original.Original
		copy.Original.Events = append([]provenance.Event(nil), original.Original.Events...)
		copy.Original.Frames = append([]provenance.FrameIdentity(nil), original.Original.Frames...)
		switch kind {
		case "report":
			copy.Report.CapturedProofEligible = true
		case "frame":
			copy.Original.Frames[0].EndCycle++
		case "event":
			copy.Original.Events[0].Value++
		case "identity":
			copy.Original.Identity.RunSHA256 = "stale"
		}
		with.Observations = &copy
		if err := CheckObservations(with, rom); err == nil {
			t.Fatalf("stale %s accepted", kind)
		}
		with.Observations = original
	}
	again, err := Run(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(with, again) {
		t.Fatal("observations are nondeterministic")
	}
	c.Observation.MaxEvents = 1
	if partial, err := Run(context.Background(), c); err == nil || partial != nil {
		t.Fatal("event overflow published partial result")
	}
}

func TestObservationConfiguration(t *testing.T) {
	for _, c := range []ObservationConfig{
		{From: -1, To: 1, MaxEvents: 100},
		{From: 0, To: 17, MaxEvents: 100},
		{From: 0, To: 1, MaxEvents: 2000001},
		{From: 0, To: 1, MaxEvents: 100, Selection: provenance.Selection{Frame: 2, RoutineStart: 1, RoutineEnd: 2}},
	} {
		if err := c.validate(20); err == nil {
			t.Fatalf("accepted invalid window %+v", c)
		}
	}
}
