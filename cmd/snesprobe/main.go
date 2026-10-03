package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/localsocket"
	"github.com/tmc/snes/internal/sneslive"
	"github.com/tmc/snes/internal/snesprobe"
	"google.golang.org/grpc"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: snesprobe serve|smoke")
		return 2
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdin, stdout, stderr)
	case "smoke":
		return runSmoke(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func runServe(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", "", "Unix socket path; serve stdio when empty")
	grpcAddr := fs.String("grpc", "", "gRPC listen address; for example 127.0.0.1:50051")
	grpcNetwork := fs.String("grpc-network", "tcp", "gRPC listen network: tcp or unix")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *socketPath != "" && *grpcAddr != "" {
		fmt.Fprintln(stderr, "serve: --socket and --grpc are mutually exclusive")
		return 2
	}
	svc := snesprobe.New()
	if *grpcAddr != "" {
		if err := serveGRPC(context.Background(), *grpcNetwork, *grpcAddr, svc); err != nil {
			fmt.Fprintf(stderr, "serve: %v\n", err)
			return 1
		}
		return 0
	}
	if *socketPath == "" {
		if err := svc.Serve(context.Background(), stdin, stdout); err != nil {
			fmt.Fprintf(stderr, "serve: %v\n", err)
			return 1
		}
		return 0
	}
	if err := serveSocket(context.Background(), *socketPath, svc); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

func serveGRPC(ctx context.Context, network, addr string, svc *snesprobe.Service) error {
	if network == "" {
		network = "tcp"
	}
	var l net.Listener
	var err error
	if network == "unix" {
		l, err = localsocket.Listen(addr)
	} else {
		l, err = net.Listen(network, addr)
	}
	if err != nil {
		return err
	}
	defer l.Close()
	server := grpc.NewServer()
	sneslive.Register(server, svc)
	stop := context.AfterFunc(ctx, server.Stop)
	defer stop()
	if err := server.Serve(l); err != nil && err != grpc.ErrServerStopped {
		return err
	}
	return nil
}

func serveSocket(ctx context.Context, path string, svc *snesprobe.Service) error {
	l, err := localsocket.Listen(path)
	if err != nil {
		return err
	}
	defer l.Close()
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return err
			}
		}
		go func() {
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			_ = svc.Serve(ctx, conn, conn)
		}()
	}
}

func runSmoke(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	romPath := fs.String("rom", "", "ROM path")
	statePath := fs.String("state", "", "save-state path")
	frames := fs.Int("frames", 120, "frames per branch")
	rightInput := fs.Uint("right-input", uint(emulator.StandardButtonRight), "first branch input mask")
	upInput := fs.Uint("up-input", uint(emulator.StandardButtonUp), "second branch input mask")
	allowMismatch := fs.Bool("allow-state-rom-mismatch", true, "allow state ROM hash mismatch")
	watchPath := fs.String("watch-file", "", "watch spec path")
	outDir := fs.String("out-dir", "", "directory for derived branch checkpoint artifacts")
	var watchFlags repeatedFlag
	fs.Var(&watchFlags, "watch", "watch field name=space:addr[:width], for example pc=wram:0x10:1")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *romPath == "" {
		fmt.Fprintln(stderr, "smoke: missing --rom")
		return 2
	}
	if *statePath == "" {
		fmt.Fprintln(stderr, "smoke: missing --state")
		return 2
	}
	if *frames < 0 {
		fmt.Fprintln(stderr, "smoke: frames must be >= 0")
		return 2
	}
	watches, err := smokeWatches(*watchPath, watchFlags)
	if err != nil {
		fmt.Fprintf(stderr, "smoke: %v\n", err)
		return 2
	}

	svc := snesprobe.New()
	if _, err := svc.LoadROM(*romPath); err != nil {
		fmt.Fprintf(stderr, "load rom: %v\n", err)
		return 1
	}
	if _, err := svc.LoadState(*statePath, *allowMismatch); err != nil {
		fmt.Fprintf(stderr, "load state: %v\n", err)
		return 1
	}
	cp, err := svc.Snapshot("frame0")
	if err != nil {
		fmt.Fprintf(stderr, "snapshot: %v\n", err)
		return 1
	}
	var artifacts *smokeArtifactManifest
	if *outDir != "" {
		artifacts, err = newSmokeArtifactManifest(*outDir, *romPath, *statePath, cp)
		if err != nil {
			fmt.Fprintf(stderr, "artifact: %v\n", err)
			return 1
		}
		export, err := svc.ExportCheckpoint(snesprobe.ExportCheckpointRequest{
			Checkpoint: cp.Name,
			Path:       filepath.Join(*outDir, "frame0.state"),
		})
		if err != nil {
			fmt.Fprintf(stderr, "export checkpoint: %v\n", err)
			return 1
		}
		artifacts.SourceCheckpoint = checkpointArtifact(export)
	}
	rightSeq := repeatInput(uint16(*rightInput), *frames)
	upSeq := repeatInput(uint16(*upInput), *frames)
	right, err := svc.RunFromCheckpoint(context.Background(), snesprobe.RunFromCheckpointRequest{
		Checkpoint:    cp.Name,
		InputSequence: rightSeq,
		WatchSpec:     watches,
		EventFilter:   []string{"frame", "input", "watch"},
	})
	if err != nil {
		fmt.Fprintf(stderr, "run right: %v\n", err)
		return 1
	}
	if artifacts != nil {
		branch, err := snapshotBranchArtifact(svc, *outDir, "right", "right-final", uint16(*rightInput), right)
		if err != nil {
			fmt.Fprintf(stderr, "artifact right: %v\n", err)
			return 1
		}
		artifacts.Branches = append(artifacts.Branches, branch)
	}
	if _, err := svc.Restore(cp.Name); err != nil {
		fmt.Fprintf(stderr, "restore: %v\n", err)
		return 1
	}
	up, err := svc.RunFromCheckpoint(context.Background(), snesprobe.RunFromCheckpointRequest{
		Checkpoint:    cp.Name,
		InputSequence: upSeq,
		WatchSpec:     watches,
		EventFilter:   []string{"frame", "input", "watch"},
	})
	if err != nil {
		fmt.Fprintf(stderr, "run up: %v\n", err)
		return 1
	}
	if artifacts != nil {
		branch, err := snapshotBranchArtifact(svc, *outDir, "up", "up-final", uint16(*upInput), up)
		if err != nil {
			fmt.Fprintf(stderr, "artifact up: %v\n", err)
			return 1
		}
		artifacts.Branches = append(artifacts.Branches, branch)
		if err := writeSmokeArtifactManifest(*outDir, artifacts); err != nil {
			fmt.Fprintf(stderr, "artifact manifest: %v\n", err)
			return 1
		}
	}

	out := struct {
		ROMPath                 string               `json:"rom_path"`
		StatePath               string               `json:"state_path"`
		ArtifactDir             string               `json:"artifact_dir,omitempty"`
		Checkpoint              snesprobe.Checkpoint `json:"checkpoint"`
		Right                   branchSummary        `json:"right"`
		Up                      branchSummary        `json:"up"`
		DistinctWatchSummaries  bool                 `json:"distinct_watch_summaries"`
		DistinctStateHashes     bool                 `json:"distinct_state_hashes"`
		DistinctFramebufferHash bool                 `json:"distinct_framebuffer_hashes"`
	}{
		ROMPath:                 *romPath,
		StatePath:               *statePath,
		ArtifactDir:             *outDir,
		Checkpoint:              cp,
		Right:                   summarizeBranch(right),
		Up:                      summarizeBranch(up),
		DistinctWatchSummaries:  summarizeBranch(right).WatchHash != summarizeBranch(up).WatchHash,
		DistinctStateHashes:     right.FinalStateHash != up.FinalStateHash,
		DistinctFramebufferHash: right.FramebufferHash != up.FramebufferHash,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "encode smoke: %v\n", err)
		return 1
	}
	return 0
}

type smokeArtifactManifest struct {
	SchemaVersion     string                   `json:"schema_version"`
	GeneratedAt       string                   `json:"generated_at"`
	ROMPath           string                   `json:"rom_path"`
	ROMSHA256         string                   `json:"rom_sha256"`
	SourceStatePath   string                   `json:"source_state_path"`
	SourceStateSHA256 string                   `json:"source_state_sha256"`
	SourceCheckpoint  checkpointArtifactRecord `json:"source_checkpoint"`
	Branches          []branchArtifactRecord   `json:"branches"`
	Provenance        provenanceRecord         `json:"provenance"`
}

type checkpointArtifactRecord struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type branchArtifactRecord struct {
	Name                 string            `json:"name"`
	Frames               int               `json:"frames"`
	InputMask            uint16            `json:"input_mask"`
	InputHash            string            `json:"input_hash"`
	FinalStateHash       string            `json:"final_state_hash"`
	ExportedStatePath    string            `json:"exported_state_path"`
	ExportedStateSHA256  string            `json:"exported_state_sha256"`
	ExportedStateBytes   int               `json:"exported_state_bytes"`
	ComponentHashes      map[string]string `json:"component_hashes,omitempty"`
	FramebufferHash      string            `json:"framebuffer_hash"`
	Watches              map[string]uint64 `json:"watches"`
	Checkpoint           string            `json:"checkpoint"`
	CheckpointStateHash  string            `json:"checkpoint_state_hash"`
	CheckpointStateBytes int               `json:"checkpoint_state_bytes"`
}

type provenanceRecord struct {
	Kind  string `json:"kind"`
	Note  string `json:"note"`
	State string `json:"state"`
}

func newSmokeArtifactManifest(outDir, romPath, statePath string, cp snesprobe.Checkpoint) (*smokeArtifactManifest, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	romHash, err := hashFile(romPath)
	if err != nil {
		return nil, fmt.Errorf("hash rom: %w", err)
	}
	stateHash, err := hashFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("hash source state: %w", err)
	}
	return &smokeArtifactManifest{
		SchemaVersion:     "snesprobe-branch-checkpoint-v1",
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		ROMPath:           romPath,
		ROMSHA256:         romHash,
		SourceStatePath:   statePath,
		SourceStateSHA256: stateHash,
		SourceCheckpoint: checkpointArtifactRecord{
			Name:   cp.Name,
			SHA256: cp.Hash,
			Bytes:  cp.Bytes,
		},
		Provenance: provenanceRecord{
			Kind:  "derived-branch-checkpoints",
			State: "derived",
			Note:  "These checkpoints are deterministically derived from source_state_path and are not independent external save-states.",
		},
	}, nil
}

func snapshotBranchArtifact(svc *snesprobe.Service, outDir, name, checkpoint string, input uint16, run snesprobe.RunResult) (branchArtifactRecord, error) {
	cp, err := svc.Snapshot(checkpoint)
	if err != nil {
		return branchArtifactRecord{}, err
	}
	export, err := svc.ExportCheckpoint(snesprobe.ExportCheckpointRequest{
		Checkpoint: cp.Name,
		Path:       filepath.Join(outDir, checkpoint+".state"),
	})
	if err != nil {
		return branchArtifactRecord{}, err
	}
	if export.Hash != run.FinalStateHash {
		return branchArtifactRecord{}, fmt.Errorf("%s checkpoint hash %s != run final_state_hash %s", name, export.Hash, run.FinalStateHash)
	}
	return branchArtifactRecord{
		Name:                 name,
		Frames:               run.Frames,
		InputMask:            input,
		InputHash:            run.InputHash,
		FinalStateHash:       run.FinalStateHash,
		ExportedStatePath:    export.Path,
		ExportedStateSHA256:  export.Hash,
		ExportedStateBytes:   export.Bytes,
		ComponentHashes:      run.ComponentHashes,
		FramebufferHash:      run.FramebufferHash,
		Watches:              run.Watches,
		Checkpoint:           cp.Name,
		CheckpointStateHash:  cp.Hash,
		CheckpointStateBytes: cp.Bytes,
	}, nil
}

func writeSmokeArtifactManifest(outDir string, manifest *smokeArtifactManifest) error {
	if manifest.SourceCheckpoint.Path == "" {
		manifest.SourceCheckpoint.Path = filepath.Join(outDir, "frame0.state")
	}
	path := filepath.Join(outDir, "manifest.json")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func checkpointArtifact(export snesprobe.ExportCheckpointResult) checkpointArtifactRecord {
	return checkpointArtifactRecord{
		Name:   export.Checkpoint,
		Path:   export.Path,
		SHA256: export.Hash,
		Bytes:  export.Bytes,
	}
}

type branchSummary struct {
	Frames          int               `json:"frames"`
	InputHash       string            `json:"input_hash"`
	WatchHash       string            `json:"watch_hash"`
	Watches         map[string]uint64 `json:"watches"`
	FramebufferHash string            `json:"framebuffer_hash"`
	FinalStateHash  string            `json:"final_state_hash"`
}

func summarizeBranch(r snesprobe.RunResult) branchSummary {
	return branchSummary{
		Frames:          r.Frames,
		InputHash:       r.InputHash,
		WatchHash:       hashJSON(r.Watches),
		Watches:         r.Watches,
		FramebufferHash: r.FramebufferHash,
		FinalStateHash:  r.FinalStateHash,
	}
}

type repeatedFlag []string

func (f *repeatedFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedFlag) Set(s string) error {
	*f = append(*f, s)
	return nil
}

func smokeWatches(path string, specs []string) (snesprobe.WatchRequest, error) {
	req := snesprobe.WatchRequest{Path: path}
	for _, spec := range specs {
		watch, err := parseWatchSpec(spec)
		if err != nil {
			return snesprobe.WatchRequest{}, err
		}
		req.Watches = append(req.Watches, watch)
	}
	if req.Path == "" && len(req.Watches) == 0 {
		return snesprobe.WatchRequest{}, fmt.Errorf("missing --watch or --watch-file")
	}
	return req, nil
}

func parseWatchSpec(spec string) (snesprobe.WatchField, error) {
	name, rest, ok := strings.Cut(spec, "=")
	if !ok || name == "" || rest == "" {
		return snesprobe.WatchField{}, fmt.Errorf("watch %q: want name=space:addr[:width]", spec)
	}
	parts := strings.Split(rest, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return snesprobe.WatchField{}, fmt.Errorf("watch %q: want name=space:addr[:width]", spec)
	}
	addr, err := strconv.ParseUint(parts[1], 0, 32)
	if err != nil {
		return snesprobe.WatchField{}, fmt.Errorf("watch %q: parse addr: %w", spec, err)
	}
	width := 1
	if len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			return snesprobe.WatchField{}, fmt.Errorf("watch %q: parse width: %w", spec, err)
		}
		width = n
	}
	return snesprobe.WatchField{Name: name, Space: parts[0], Addr: uint32(addr), Width: width}, nil
}

func repeatInput(input uint16, n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = input
	}
	return out
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func hashJSON(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
