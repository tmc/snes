package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentCachedBatchBindings(t *testing.T) {
	for _, kind := range []string{"accepted status", "task identity", "directory traversal"} {
		t.Run(kind, func(t *testing.T) {
			in, dir := batchSetup(t)
			r, err := RunBatch(context.Background(), dir, in)
			if err != nil {
				t.Fatal(err)
			}
			row := &r.Rows[0]
			switch kind {
			case "accepted status":
				row.Status = "accepted"
				r.Accepted++
				r.Unexecuted--
			case "task identity":
				row.CandidateID = "unrelated"
				row.Entry = 0x0cc47b
				row.ROMSHA256 = digest([]byte("unrelated ROM"))
			case "directory traversal":
				outside := filepath.Join(filepath.Dir(dir), "external-task")
				if err = os.CopyFS(outside, os.DirFS(filepath.Join(dir, row.Directory))); err != nil {
					t.Fatal(err)
				}
				row.Directory = "../external-task"
			}
			b, err := json.MarshalIndent(row, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			name := "row-01.json"
			if err = os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
				t.Fatal(err)
			}
			r.Artifacts[name] = digest(b)
			b, err = json.MarshalIndent(r, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			retained, err := ResumeBatch(context.Background(), dir, in, digest(b))
			if err == nil {
				t.Fatalf("forged cached %s returned accepted=%d status=%s entry=%06x phase=%s directory=%s", kind, retained.Accepted, retained.Rows[0].Status, retained.Rows[0].Entry, retained.Rows[0].State.Phase, retained.Rows[0].Directory)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func TestBatchExternalReadinessPin(t *testing.T) {
	in, dir := batchSetup(t)
	report, err := RunBatch(context.Background(), dir, in)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "manifest.json")
	original, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	expected := digest(original)
	if _, err := RunBatch(context.Background(), dir, in); err == nil {
		t.Fatal("accepted unpinned existing output")
	}
	report.Rows[0].Status = "accepted"
	report.Accepted = 1
	report.Unexecuted = 9
	forged, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, forged, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResumeBatch(context.Background(), dir, in, expected); err == nil {
		t.Fatal("accepted replacement under original external readiness pin")
	}
	after, _ := os.ReadFile(manifest)
	if string(after) != string(forged) {
		t.Fatal("failed read mutated retained output")
	}
}

func TestBatchFullStateAndArtifactInventory(t *testing.T) {
	for _, kind := range []string{"full state", "missing artifact pin"} {
		t.Run(kind, func(t *testing.T) {
			in, dir := batchSetup(t)
			report, err := RunBatch(context.Background(), dir, in)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "full state" {
				report.Rows[0].State.Reason = "unrelated state"
				b, _ := json.MarshalIndent(report.Rows[0], "", "  ")
				os.WriteFile(filepath.Join(dir, "row-01.json"), b, 0600)
				report.Artifacts["row-01.json"] = digest(b)
			} else {
				delete(report.Artifacts, "row-01.json")
			}
			b, _ := json.MarshalIndent(report, "", "  ")
			os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600)
			// Even a caller that accidentally pins a malformed original aggregation
			// cannot use it to bypass the semantic and artifact cross-bindings.
			if _, err := ResumeBatch(context.Background(), dir, in, digest(b)); err == nil {
				t.Fatalf("accepted inconsistent %s", kind)
			}
		})
	}
}
