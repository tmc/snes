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
		e["Package"] = "example"
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
		{name: "success", output: events("pass", "QUALIFY comparisons=3\nQUALIFY artifact_sha256="+digest([]byte("fixture"))+"\n")},
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
			err := qualify(path, out, testRunner(tt.output))
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
			err := qualify(path, filepath.Join(dir, "out"), testRunner(events("pass", "QUALIFY comparisons=1\nQUALIFY artifact_sha256="+digest(core)+"\n")))
			if (err != nil) != tt.fail {
				t.Fatalf("error = %v, want failure %v", err, tt.fail)
			}
		})
	}
}

func testRunner(output []byte) runner {
	return func(args, env []string) ([]byte, error) {
		if len(args) > 1 && args[1] == "list" {
			return []byte("example\n"), nil
		}
		return output, nil
	}
}

func TestEventAttributionAndOrder(t *testing.T) {
	valid := events("pass", "QUALIFY comparisons=3\n")
	lines := strings.Split(strings.TrimSpace(string(valid)), "\n")
	reorder := func(order ...int) []byte {
		var out []string
		for _, i := range order {
			out = append(out, lines[i])
		}
		return []byte(strings.Join(out, "\n") + "\n")
	}
	for _, tt := range []struct {
		name   string
		output []byte
		fail   bool
	}{
		{"valid", valid, false},
		{"foreign package", []byte(strings.ReplaceAll(string(valid), "example", "other")), true},
		{"foreign package pass", []byte(strings.Join(lines[:4], "\n") + "\n" + strings.ReplaceAll(lines[4], "example", "other")), true},
		{"pass before run", reorder(0, 3, 1, 2, 4), true},
		{"observation before run", reorder(0, 2, 1, 3, 4), true},
		{"duplicate run", reorder(0, 1, 1, 2, 3, 4), true},
		{"output after test pass", reorder(0, 1, 3, 2, 4), true},
		{"duplicate package pass", reorder(0, 1, 2, 3, 4, 4), true},
		{"package pass before test", reorder(0, 1, 2, 4, 3), true},
		{"missing package pass", reorder(0, 1, 2, 3), true},
		{"unrelated test", []byte(strings.ReplaceAll(string(valid), "TestWitness", "TestOther")), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseEvents(testCase{Test: "TestWitness", MinComparisons: 1}, "example", tt.output)
			if (err != nil) != tt.fail {
				t.Fatalf("error=%v want failure=%v", err, tt.fail)
			}
		})
	}
}

func TestRequiredArtifactObservation(t *testing.T) {
	for _, tt := range []struct {
		name, observed string
		fail           bool
	}{
		{"observed", digest([]byte("fixture")), false},
		{"not used", "", true},
		{"different runtime file", digest([]byte("different")), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "fixture")
			if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			m := manifest{Artifacts: []artifact{{Path: path, SHA256: digest([]byte("fixture")), Kind: "fixture"}}, Cases: []testCase{{Package: "./example", Test: "TestWitness", MinComparisons: 1}}}
			raw, _ := json.Marshal(m)
			manifestPath := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			output := "QUALIFY comparisons=1\n"
			if tt.observed != "" {
				output += "QUALIFY artifact_sha256=" + tt.observed + "\n"
			}
			err := qualify(manifestPath, filepath.Join(dir, "out"), testRunner(events("pass", output)))
			if (err != nil) != tt.fail {
				t.Fatalf("error=%v want failure=%v", err, tt.fail)
			}
		})
	}
}

func TestArtifactBindingOverridesInheritedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "selected")
	if err := os.WriteFile(path, []byte("selected"), 0600); err != nil {
		t.Fatal(err)
	}
	const key = "SNES_QUALIFY_BSNES_CORE"
	t.Setenv(key, filepath.Join(dir, "unrelated-inherited-core"))
	m := manifest{Artifacts: []artifact{{Path: path, SHA256: digest([]byte("selected")), Kind: "fixture", Env: key}}, Cases: []testCase{{Package: "./example", Test: "TestWitness", MinComparisons: 1}}}
	raw, _ := json.Marshal(m)
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := qualify(manifestPath, filepath.Join(dir, "out"), func(args, env []string) ([]byte, error) {
		calls++
		if len(env) != 1 || env[0] != key+"="+path {
			t.Fatalf("runtime bindings=%v", env)
		}
		return testRunner(events("pass", "QUALIFY comparisons=1\nQUALIFY artifact_sha256="+digest([]byte("selected"))+"\n"))(args, env)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("runner calls=%d, want package resolution and test", calls)
	}
}
