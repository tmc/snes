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
		args := []string{"go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=120s", "-run", strings.Join(parts, "/"), c.Package}
		output, runErr := run(args, env)
		cr, parseErr := parseEvents(c, output)
		cr.Command = args
		cr.Output = string(output)
		err := errors.Join(runErr, parseErr)
		if err != nil {
			cr.Error = err.Error()
		}
		r.Cases = append(r.Cases, cr)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func parseEvents(c testCase, b []byte) (caseReceipt, error) {
	r := caseReceipt{Case: c}
	dec := json.NewDecoder(bytes.NewReader(b))
	packagePass := false
	for {
		var e struct{ Action, Package, Test, Output string }
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return r, fmt.Errorf("parse test events: %w", err)
		}
		switch e.Action {
		case "start", "run", "pause", "cont", "pass", "bench", "fail", "output", "skip", "build-output", "build-fail":
		default:
			return r, fmt.Errorf("unknown test action %q", e.Action)
		}
		if e.Action == "fail" || e.Action == "build-fail" {
			return r, fmt.Errorf("failed test %s", e.Test)
		}
		if e.Action == "skip" {
			r.Skips = append(r.Skips, e.Test)
		}
		if e.Test == "" && e.Action == "pass" {
			packagePass = true
		}
		if e.Test != c.Test {
			continue
		}
		switch e.Action {
		case "run":
			r.Ran = true
		case "pass":
			r.Passed = true
		case "output":
			// The token is emitted after successful comparisons by the named test.
			for _, line := range strings.Split(e.Output, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "QUALIFY comparisons=") {
					continue
				}
				n, err := strconv.ParseUint(strings.TrimPrefix(line, "QUALIFY comparisons="), 10, 64)
				if err != nil || n > ^uint64(0)-r.Comparisons {
					return r, fmt.Errorf("invalid comparison observation %q", line)
				}
				r.Comparisons += n
			}
		}
	}
	if !r.Ran || !r.Passed || !packagePass || len(r.Skips) > 0 || r.Comparisons < c.MinComparisons {
		return r, fmt.Errorf("insufficient evidence: run=%v pass=%v package_pass=%v skips=%d comparisons=%d (need %d)", r.Ran, r.Passed, packagePass, len(r.Skips), r.Comparisons, c.MinComparisons)
	}
	return r, nil
}
