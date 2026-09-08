package snesprobe

import (
	"fmt"
	"strings"
	"testing"
)

func TestRejectedCheckpointPreservesNextName(t *testing.T) {
	for _, method := range []string{"snapshot", "fork"} {
		t.Run(method, func(t *testing.T) {
			s := loadedService(t)
			if _, err := s.Snapshot("source"); err != nil {
				t.Fatal(err)
			}
			for i := 1; i < maxCheckpointCount; i++ {
				s.checkpoint[fmt.Sprint(i)] = nil
			}
			create := func() (Checkpoint, error) {
				if method == "fork" {
					return s.Fork("source", "")
				}
				return s.Snapshot("")
			}
			if _, err := create(); err == nil {
				t.Fatal("full checkpoint store accepted entry")
			}
			delete(s.checkpoint, "1")
			got, err := create()
			if err != nil {
				t.Fatal(err)
			}
			want := "checkpoint-1"
			if method == "fork" {
				want = "source-fork-1"
			}
			if got.Name != want {
				t.Fatalf("name after rejection = %q, want %q", got.Name, want)
			}
		})
	}
}

func TestAutomaticCheckpointNamePreservesExplicitCheckpoint(t *testing.T) {
	for _, method := range []string{"snapshot", "fork"} {
		t.Run(method, func(t *testing.T) {
			s := loadedService(t)
			name, wantName := "checkpoint-1", "checkpoint-2"
			if method == "fork" {
				name, wantName = "source-fork-1", "source-fork-2"
			}
			saved, err := s.Snapshot(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Step(StepRequest{Frames: 1}); err != nil {
				t.Fatal(err)
			}
			var got Checkpoint
			if method == "fork" {
				if _, err := s.Snapshot("source"); err != nil {
					t.Fatal(err)
				}
				got, err = s.Fork("source", "")
			} else {
				got, err = s.Snapshot("")
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != wantName {
				t.Fatalf("automatic name = %q, want %q", got.Name, wantName)
			}
			if got.Hash == saved.Hash {
				t.Fatal("fixture did not produce different machine states")
			}
			kept, err := s.Fork(name, "verify-original")
			if err != nil {
				t.Fatal(err)
			}
			if kept.Hash != saved.Hash {
				t.Fatal("automatic checkpoint replaced explicit checkpoint data")
			}
			// Explicit names retain their replacement semantics.
			replacement, err := s.Snapshot(name)
			if err != nil {
				t.Fatal(err)
			}
			if replacement.Hash == saved.Hash {
				t.Fatal("explicit checkpoint replacement did not update data")
			}
		})
	}
}

func TestRejectedForkNamePreservesNextName(t *testing.T) {
	s := loadedService(t)
	name := strings.Repeat("x", 256)
	if _, err := s.Snapshot(name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fork(name, ""); err == nil {
		t.Fatal("oversized generated name accepted")
	}
	got, err := s.Snapshot("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "checkpoint-1" {
		t.Fatalf("name after rejected fork = %q, want checkpoint-1", got.Name)
	}
}
