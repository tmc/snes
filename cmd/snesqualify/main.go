// snesqualify runs explicitly named tests and records the evidence they produce.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type manifest struct {
	Artifacts []artifact `json:"artifacts"`
	Cases     []testCase `json:"cases"`
}

type artifact struct {
	Path   string         `json:"path"`
	SHA256 string         `json:"sha256"`
	Kind   string         `json:"kind"`
	Env    string         `json:"env,omitempty"`
	Build  *buildIdentity `json:"build,omitempty"`
}

type buildIdentity struct {
	Path           string `json:"path"`
	SHA256         string `json:"sha256"`
	SourceRevision string `json:"source_revision"`
}

type testCase struct {
	Package        string `json:"package"`
	Test           string `json:"test"`
	MinComparisons uint64 `json:"min_comparisons"`
}

type caseReceipt struct {
	Package     string   `json:"package"`
	Artifacts   []string `json:"observed_artifacts,omitempty"`
	Case        testCase `json:"case"`
	Command     []string `json:"command"`
	Ran         bool     `json:"ran"`
	Passed      bool     `json:"passed"`
	Comparisons uint64   `json:"comparisons"`
	Skips       []string `json:"skips,omitempty"`
	Error       string   `json:"error,omitempty"`
	Output      string   `json:"output"`
}

type receipt struct {
	GoEnv          json.RawMessage `json:"go_env"`
	Bindings       []string        `json:"bindings,omitempty"`
	Started        time.Time       `json:"started"`
	Elapsed        string          `json:"elapsed"`
	Head           string          `json:"head"`
	DirtyIdentity  string          `json:"dirty_identity"`
	Go             string          `json:"go"`
	OS             string          `json:"os"`
	Arch           string          `json:"arch"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	Artifacts      []artifact      `json:"artifacts"`
	Cases          []caseReceipt   `json:"cases"`
	Passed         bool            `json:"passed"`
	Error          string          `json:"error,omitempty"`
}

type runner func(args, env []string) ([]byte, error)

func main() {
	manifestPath := flag.String("manifest", "", "qualification manifest")
	out := flag.String("out", "", "receipt directory")
	flag.Parse()
	if *manifestPath == "" || *out == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: snesqualify -manifest file -out directory")
		os.Exit(2)
	}
	if err := qualify(*manifestPath, *out, runCommand); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCommand(args, env []string) ([]byte, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), env...)
	if len(args) > 1 && args[0] == "go" && args[1] == "list" {
		return cmd.Output()
	}
	return cmd.CombinedOutput()
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func readJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func checkArtifact(path, want string) error {
	if len(want) != 64 {
		return fmt.Errorf("%s: invalid sha256", path)
	}
	if _, err := hex.DecodeString(want); err != nil {
		return fmt.Errorf("%s: invalid sha256: %w", path, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s: empty artifact", path)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: sha256 %s, want %s", path, got, want)
	}
	return nil
}

func qualify(path, out string, run runner) (retErr error) {
	r := receipt{Started: time.Now().UTC(), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	defer func() {
		r.Elapsed = time.Since(r.Started).String()
		r.Passed = retErr == nil
		if retErr != nil {
			r.Error = retErr.Error()
		}
		b, err := json.MarshalIndent(r, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(out, "receipt.json"), append(b, '\n'), 0644)
		}
		retErr = errors.Join(retErr, err)
	}()
	// Git identity includes unstaged, staged, and untracked content.
	head, err := runCommand([]string{"git", "rev-parse", "HEAD"}, nil)
	if err != nil {
		return fmt.Errorf("read HEAD: %w", err)
	}
	r.Head = strings.TrimSpace(string(head))
	goenv, err := runCommand([]string{"go", "env", "-json", "GOOS", "GOARCH", "GOVERSION", "GOFLAGS", "CGO_ENABLED", "GOTOOLCHAIN", "GOWORK", "GOENV"}, nil)
	if err != nil {
		return fmt.Errorf("read Go environment: %w", err)
	}
	r.GoEnv = goenv
	patch, err := runCommand([]string{"git", "diff", "--binary", "HEAD"}, nil)
	if err != nil {
		return fmt.Errorf("read dirty patch: %w", err)
	}
	root, err := runCommand([]string{"git", "rev-parse", "--show-toplevel"}, nil)
	if err != nil {
		return err
	}
	repoRoot := strings.TrimSpace(string(root))
	paths, err := runCommand([]string{"git", "-C", repoRoot, "ls-files", "--others", "--exclude-standard", "-z"}, nil)
	if err != nil {
		return err
	}
	for _, p := range strings.Split(string(paths), "\x00") {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repoRoot, p))
		if err != nil {
			return err
		}
		patch = append(patch, []byte("\x00"+p+"\x00"+digest(b))...)
	}
	r.DirtyIdentity = digest(patch)
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	r.ManifestSHA256 = digest(raw)
	var m manifest
	if err := readJSON(raw, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if len(m.Cases) == 0 {
		return fmt.Errorf("manifest has no cases")
	}
	r.Artifacts = m.Artifacts
	env := []string{}
	seenEnv := map[string]bool{}
	for _, a := range m.Artifacts {
		if a.Kind != "rom" && a.Kind != "firmware" && a.Kind != "core" && a.Kind != "fixture" {
			return fmt.Errorf("%s: unknown artifact kind %q", a.Path, a.Kind)
		}
		if err := checkArtifact(a.Path, a.SHA256); err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
		if a.Kind == "core" {
			if a.Build == nil || a.Build.SourceRevision == "" {
				return fmt.Errorf("%s: core build identity required", a.Path)
			}
			if err := checkArtifact(a.Build.Path, a.Build.SHA256); err != nil {
				return fmt.Errorf("build metadata: %w", err)
			}
			b, err := os.ReadFile(a.Build.Path)
			if err != nil {
				return err
			}
			var meta struct {
				BinarySHA256   string   `json:"binary_sha256"`
				SourceRevision string   `json:"source_revision"`
				Compiler       string   `json:"compiler"`
				Flags          []string `json:"flags"`
			}
			if err := readJSON(b, &meta); err != nil {
				return err
			}
			if meta.BinarySHA256 != a.SHA256 || meta.SourceRevision != a.Build.SourceRevision || meta.Compiler == "" {
				return fmt.Errorf("%s: conflicting or incomplete build metadata", a.Path)
			}
		}
		if a.Env != "" {
			if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(a.Env) || seenEnv[a.Env] {
				return fmt.Errorf("invalid or duplicate environment binding %q", a.Env)
			}
			seenEnv[a.Env] = true
			abs, err := filepath.Abs(a.Path)
			if err != nil {
				return err
			}
			env = append(env, a.Env+"="+abs)
		}
	}
	r.Bindings = env
	observed := map[string]bool{}
	seen := map[string]bool{}
	for _, c := range m.Cases {
		key := c.Package + "/" + c.Test
		if c.Package == "" || strings.HasPrefix(c.Package, "-") || strings.Contains(c.Package, "...") || !strings.HasPrefix(c.Test, "Test") || c.MinComparisons == 0 || seen[key] {
			return fmt.Errorf("invalid or duplicate case %q", key)
		}
		seen[key] = true
		parts := strings.Split(c.Test, "/")
		for i, p := range parts {
			if p == "" {
				return fmt.Errorf("empty test path segment")
			}
			parts[i] = "^" + regexp.QuoteMeta(p) + "$"
		}
		listed, listErr := run([]string{"go", "list", "-mod=readonly", "-f", "{{.ImportPath}}", c.Package}, env)
		if listErr != nil {
			return fmt.Errorf("resolve package %s: %w: %s", c.Package, listErr, listed)
		}
		packagePath := strings.TrimSpace(string(listed))
		if packagePath == "" || strings.ContainsAny(packagePath, " \t\r\n") {
			return fmt.Errorf("resolve package %s: expected one import path, got %q", c.Package, packagePath)
		}
		args := []string{"go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=120s", "-run", strings.Join(parts, "/"), c.Package}
		output, runErr := run(args, env)
		cr, parseErr := parseEvents(c, packagePath, output)
		cr.Command = args
		cr.Output = string(output)
		err := errors.Join(runErr, parseErr)
		if err != nil {
			cr.Error = err.Error()
		}
		r.Cases = append(r.Cases, cr)
		for _, hash := range cr.Artifacts {
			observed[hash] = true
		}
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	for _, a := range m.Artifacts {
		if err := checkArtifact(a.Path, a.SHA256); err != nil {
			return fmt.Errorf("postflight: %w", err)
		}
		if !observed[a.SHA256] {
			return fmt.Errorf("artifact %s was not observed by a selected test", a.Path)
		}
	}
	return nil
}

func parseEvents(c testCase, packagePath string, b []byte) (caseReceipt, error) {
	r := caseReceipt{Case: c, Package: packagePath}
	dec := json.NewDecoder(bytes.NewReader(b))
	started, packagePass := false, false
	states := map[string]string{}
	for {
		var e struct{ Action, Package, Test, Output string }
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return r, fmt.Errorf("parse test events: %w", err)
		}
		if e.Package != packagePath {
			return r, fmt.Errorf("event package %q, want %q", e.Package, packagePath)
		}
		if packagePass {
			return r, fmt.Errorf("event after package pass")
		}
		if e.Action == "start" {
			if started || e.Test != "" {
				return r, fmt.Errorf("duplicate or malformed package start")
			}
			started = true
			continue
		}
		if !started {
			return r, fmt.Errorf("event before package start")
		}
		if e.Action == "fail" || e.Action == "build-fail" {
			return r, fmt.Errorf("failed test %s", e.Test)
		}
		if e.Action == "skip" {
			r.Skips = append(r.Skips, e.Test)
			return r, fmt.Errorf("skipped test %s", e.Test)
		}
		if e.Test == "" {
			switch e.Action {
			case "output":
			case "pass":
				if !r.Passed {
					return r, fmt.Errorf("package passed before selected test")
				}
				for name, state := range states {
					if state != "pass" {
						return r, fmt.Errorf("package passed with unfinished test %s", name)
					}
				}
				packagePass = true
			default:
				return r, fmt.Errorf("invalid package action %q", e.Action)
			}
			continue
		}
		if e.Test != c.Test && !strings.HasPrefix(e.Test, c.Test+"/") && !strings.HasPrefix(c.Test, e.Test+"/") {
			return r, fmt.Errorf("unexpected test %q", e.Test)
		}
		state := states[e.Test]
		switch e.Action {
		case "run":
			if state != "" {
				return r, fmt.Errorf("duplicate run for %s", e.Test)
			}
			states[e.Test] = "run"
			if e.Test == c.Test {
				r.Ran = true
			}
		case "pause":
			if state != "run" {
				return r, fmt.Errorf("pause outside running test %s", e.Test)
			}
			states[e.Test] = "pause"
		case "cont":
			if state != "pause" {
				return r, fmt.Errorf("continue outside paused test %s", e.Test)
			}
			states[e.Test] = "run"
		case "pass":
			if state != "run" {
				return r, fmt.Errorf("pass outside running test %s", e.Test)
			}
			states[e.Test] = "pass"
			if e.Test == c.Test {
				r.Passed = true
			}
		case "output":
			if state != "run" && state != "pause" {
				return r, fmt.Errorf("output outside running test %s", e.Test)
			}
			if e.Test != c.Test {
				continue
			}
			for _, line := range strings.Split(e.Output, "\n") {
				line = strings.TrimSpace(line)
				if value, ok := strings.CutPrefix(line, "QUALIFY comparisons="); ok {
					n, err := strconv.ParseUint(value, 10, 64)
					if err != nil || n > ^uint64(0)-r.Comparisons {
						return r, fmt.Errorf("invalid comparison observation %q", line)
					}
					r.Comparisons += n
				} else if hash, ok := strings.CutPrefix(line, "QUALIFY artifact_sha256="); ok {
					decoded, err := hex.DecodeString(hash)
					if err != nil || len(decoded) != 32 {
						return r, fmt.Errorf("invalid artifact observation %q", line)
					}
					r.Artifacts = append(r.Artifacts, hash)
				}
			}
		default:
			return r, fmt.Errorf("invalid test action %q", e.Action)
		}
	}
	if !r.Ran || !r.Passed || !packagePass || r.Comparisons < c.MinComparisons {
		return r, fmt.Errorf("insufficient evidence: run=%v pass=%v package_pass=%v comparisons=%d (need %d)", r.Ran, r.Passed, packagePass, r.Comparisons, c.MinComparisons)
	}
	return r, nil
}
