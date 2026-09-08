package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyntheticComparison(t *testing.T) {
	a, b := []byte{1, 2, 3}, []byte{1, 2, 3}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("snapshot mismatch")
		}
	}
	b[1]++
	if a[1] == b[1] {
		t.Fatal("mutant not detected")
	}
	fmt.Println("QUALIFY comparisons=4")
}

func events(action, output string) []byte {
	var b strings.Builder
	for _, e := range []map[string]string{
		{"Action": "start", "Package": "example"},
		{"Action": "run", "Test": "TestWitness"},
		{"Action": "output", "Test": "TestWitness", "Output": output},
		{"Action": action, "Test": "TestWitness"},
		{"Action": "pass", "Package": "example"},
	} {
		raw, _ := json.Marshal(e)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
func TestQualification(t *testing.T) {
	for _, tt := range []struct {
		name           string
		output         []byte
		missing, wrong bool
		wantError      bool
	}{
		{name: "success", output: events("pass", "QUALIFY comparisons=3\n")},
		{name: "failed test", output: events("fail", "QUALIFY comparisons=3\n"), wantError: true},
		{name: "zero matches", output: []byte(`{"Action":"pass"}`), wantError: true},
		{name: "unexpected skip", output: events("skip", "QUALIFY comparisons=3\n"), wantError: true},
		{name: "missing artifact", missing: true, wantError: true},
		{name: "wrong hash", wrong: true, wantError: true},
		{name: "malformed output", output: []byte("bad JSON"), wantError: true},
		{name: "zero comparisons", output: events("pass", "QUALIFY comparisons=0\n"), wantError: true},
		{name: "forged subtest count", output: []byte(strings.ReplaceAll(string(events("pass", "QUALIFY comparisons=3\n")), "TestWitness", "TestWitness/child")), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			artifactPath := filepath.Join(dir, "fixture")
			if !tt.missing {
				if err := os.WriteFile(artifactPath, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			hash := digest([]byte("fixture"))
			if tt.wrong {
				hash = digest([]byte("wrong"))
			}
			m := manifest{Artifacts: []artifact{{Path: artifactPath, SHA256: hash, Kind: "fixture"}}, Cases: []testCase{{Package: "./example", Test: "TestWitness", MinComparisons: 1}}}
			raw, _ := json.Marshal(m)
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out")
			err := qualify(path, out, func(args, env []string) ([]byte, error) { return tt.output, nil })
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %v", err, tt.wantError)
			}
			raw, err = os.ReadFile(filepath.Join(out, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			var r receipt
			if err = json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			if r.Passed == tt.wantError {
				t.Fatalf("receipt passed=%v", r.Passed)
			}
			if r.Head == "" || r.DirtyIdentity == "" {
				t.Fatal("missing source identity")
			}
		})
	}
}

func TestArtifactIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	for _, tt := range []struct {
		name, body, hash string
		fail             bool
	}{
		{"valid", "abc", digest([]byte("abc")), false},
		{"empty", "", digest(nil), true},
		{"invalid digest", "abc", "no", true},
		{"mismatch", "abc", digest([]byte("def")), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := checkArtifact(path, tt.hash); (err != nil) != tt.fail {
				t.Fatalf("error = %v, want failure %v", err, tt.fail)
			}
		})
	}
}

func TestCoreBuildIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, revision string
		missing        bool
		fail           bool
	}{
		{"consistent", "revision1", false, false}, {"conflicting", "revision2", false, true}, {"missing", "", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			corePath := filepath.Join(dir, "core")
			buildPath := filepath.Join(dir, "build.json")
			core := []byte("test core identity")
			if err := os.WriteFile(corePath, core, 0600); err != nil {
				t.Fatal(err)
			}
			metadata, _ := json.Marshal(map[string]any{"binary_sha256": digest(core), "source_revision": tt.revision, "compiler": "test compiler", "flags": []string{}})
			if err := os.WriteFile(buildPath, metadata, 0600); err != nil {
				t.Fatal(err)
			}
			a := artifact{Path: corePath, SHA256: digest(core), Kind: "core"}
			if !tt.missing {
				a.Build = &buildIdentity{Path: buildPath, SHA256: digest(metadata), SourceRevision: "revision1"}
			}
			m := manifest{Artifacts: []artifact{a}, Cases: []testCase{{Package: "./example", Test: "TestWitness", MinComparisons: 1}}}
			raw, _ := json.Marshal(m)
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			err := qualify(path, filepath.Join(dir, "out"), func(args, env []string) ([]byte, error) { return events("pass", "QUALIFY comparisons=1\n"), nil })
			if (err != nil) != tt.fail {
				t.Fatalf("error = %v, want failure %v", err, tt.fail)
			}
		})
	}
}
