// Package snesprobe provides a local control service for agent exploration.
package snesprobe

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	snes "github.com/tmc/snes"
	"github.com/tmc/snes/internal/trace"
)

// Request is a stdio JSON service request.
type Request struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is a stdio JSON service response.
type Response struct {
	ID     string `json:"id,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Service controls a single root SNES system plus named checkpoints.
// Its methods serialize access across transports. Use New to initialize a Service.
type Service struct {
	sys        *snes.System
	rom        []byte
	romPath    string
	checkpoint map[string][]byte
	nextID     int
	input      uint16
	mu         sync.Mutex
}

// New returns a new service.
func New() *Service {
	return &Service{checkpoint: map[string][]byte{}}
}

// Serve reads newline-delimited JSON requests and writes responses.
func (s *Service) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), maxRequestBytes)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			if err := enc.Encode(Response{Error: fmt.Sprintf("parse request: %v", err)}); err != nil {
				return err
			}
			continue
		}
		result, err := s.Handle(ctx, req.Method, req.Params)
		resp := Response{ID: req.ID, Result: result}
		if err != nil {
			resp.Error = err.Error()
			resp.Result = nil
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Handle runs method with params.
func (s *Service) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(params) > maxRequestBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", maxRequestBytes)
	}
	switch method {
	case "load_rom":
		var p struct {
			Path string `json:"path"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.loadROM(p.Path)
	case "load_state":
		var p struct {
			Path                  string `json:"path"`
			AllowStateROMMismatch bool   `json:"allow_state_rom_mismatch,omitempty"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.loadState(p.Path, p.AllowStateROMMismatch)
	case "reset":
		return s.reset()
	case "set_input":
		var p struct {
			Input uint16 `json:"input"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.setInput(p.Input)
	case "step":
		var p StepRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.step(ctx, p)
	case "run":
		var p RunRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.run(ctx, p, nil)
	case "read_watches":
		var p WatchRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.readWatches(p)
	case "read_memory":
		var p MemoryRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.readMemory(p)
	case "snapshot":
		var p struct {
			Name string `json:"name,omitempty"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.snapshot(p.Name)
	case "restore":
		var p struct {
			Checkpoint string `json:"checkpoint"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.restore(p.Checkpoint)
	case "fork":
		var p struct {
			Checkpoint string `json:"checkpoint"`
			Name       string `json:"name,omitempty"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.fork(p.Checkpoint, p.Name)
	case "export_checkpoint":
		var p ExportCheckpointRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.exportCheckpoint(p)
	case "run_from_checkpoint":
		var p RunFromCheckpointRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.runFromCheckpoint(ctx, p)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (s *Service) loadROM(path string) (Status, error) {
	if path == "" {
		return Status{}, fmt.Errorf("load_rom: missing path")
	}
	data, err := readBoundedFile(path, 64<<20)
	if err != nil {
		return Status{}, fmt.Errorf("read rom: %w", err)
	}
	sys := snes.NewSystem(nil)
	if err := sys.LoadROM(data); err != nil {
		return Status{}, err
	}
	sys.Power()
	s.sys = sys
	s.rom = append(s.rom[:0], data...)
	s.romPath = path
	s.checkpoint = map[string][]byte{}
	s.input = 0
	return s.status("loaded"), nil
}

func (s *Service) loadState(path string, allowMismatch bool) (Status, error) {
	if s.sys == nil {
		return Status{}, fmt.Errorf("load_state: no rom loaded")
	}
	data, err := readBoundedFile(path, 64<<20)
	if err != nil {
		return Status{}, fmt.Errorf("read state: %w", err)
	}
	if allowMismatch {
		err = s.sys.UnserializeWithOptions(data, snes.UnserializeOptions{IgnoreROMHash: true})
	} else {
		err = s.sys.Unserialize(data)
	}
	if err != nil {
		return Status{}, err
	}
	return s.status("state_loaded"), nil
}

func (s *Service) reset() (Status, error) {
	if s.sys == nil {
		return Status{}, fmt.Errorf("reset: no rom loaded")
	}
	s.sys.Reset()
	s.input = 0
	return s.status("reset"), nil
}

func (s *Service) setInput(input uint16) (Status, error) {
	if s.sys == nil {
		return Status{}, fmt.Errorf("set_input: no rom loaded")
	}
	s.input = input
	return s.status("input_set"), nil
}

// StepRequest describes a frame step.
type StepRequest struct {
	Frames      int          `json:"frames"`
	Input       uint16       `json:"input,omitempty"`
	WatchSpec   WatchRequest `json:"watch_spec,omitempty"`
	Framebuffer string       `json:"framebuffer,omitempty"`
}

// StepResult is returned from Step.
type StepResult struct {
	Frames          int               `json:"frames"`
	Input           uint16            `json:"input"`
	Watches         map[string]uint64 `json:"watches,omitempty"`
	FramebufferHash string            `json:"framebuffer_hash,omitempty"`
	FrameRGBA       string            `json:"frame_rgba_base64,omitempty"`
}

func (s *Service) step(ctx context.Context, p StepRequest) (StepResult, error) {
	if s.sys == nil {
		return StepResult{}, fmt.Errorf("step: no rom loaded")
	}
	if err := ctx.Err(); err != nil {
		return StepResult{}, err
	}
	if p.Frames < 0 || p.Frames > MaxFrames {
		return StepResult{}, fmt.Errorf("step: frames must be between 0 and %d", MaxFrames)
	}
	watch, err := validateQuery(p.WatchSpec, p.Framebuffer, nil)
	if err != nil {
		return StepResult{}, err
	}
	p.WatchSpec = watch
	for i := 0; i < p.Frames; i++ {
		if err := ctx.Err(); err != nil {
			return StepResult{}, err
		}
		if err := s.sys.SetInputState(0, p.Input); err != nil {
			return StepResult{}, err
		}
		if err := s.sys.RunFrame(); err != nil {
			return StepResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return StepResult{}, err
	}
	watches, err := s.readWatches(p.WatchSpec)
	if err != nil {
		return StepResult{}, err
	}
	out := StepResult{
		Frames:          p.Frames,
		Input:           p.Input,
		Watches:         watches.Watches,
		FramebufferHash: hashFrame(s.sys.FrameBuffer()),
	}
	if strings.EqualFold(p.Framebuffer, "rgba") {
		out.FrameRGBA = base64.StdEncoding.EncodeToString(frameRGBA(s.sys.FrameBuffer()))
	}
	return out, nil
}

// RunRequest describes a live run from the current state.
type RunRequest struct {
	Frames        int          `json:"frames,omitempty"`
	Input         *uint16      `json:"input,omitempty"`
	InputSequence []uint16     `json:"input_sequence,omitempty"`
	WatchSpec     WatchRequest `json:"watch_spec,omitempty"`
	EventFilter   []string     `json:"event_filter,omitempty"`
	Framebuffer   string       `json:"framebuffer,omitempty"`
	Every         int          `json:"every,omitempty"`
}

// FrameSummary describes one sampled frame from a run.
type FrameSummary struct {
	Frame           int               `json:"frame"`
	Input           uint16            `json:"input"`
	Watches         map[string]uint64 `json:"watches,omitempty"`
	FramebufferHash string            `json:"framebuffer_hash,omitempty"`
	ComponentHashes map[string]string `json:"component_hashes,omitempty"`
}

// RunLiveResult contains a live run summary plus sampled frame events.
type RunLiveResult struct {
	Frames          int               `json:"frames"`
	InputHash       string            `json:"input_hash"`
	EventFilter     []string          `json:"event_filter,omitempty"`
	Watches         map[string]uint64 `json:"watches,omitempty"`
	FramebufferHash string            `json:"framebuffer_hash,omitempty"`
	ComponentHashes map[string]string `json:"component_hashes,omitempty"`
	FinalStateHash  string            `json:"final_state_hash,omitempty"`
	FrameRGBA       string            `json:"frame_rgba_base64,omitempty"`
	FrameSummaries  []FrameSummary    `json:"frame_summaries,omitempty"`
}

func (s *Service) run(ctx context.Context, p RunRequest, emit func(FrameSummary) error) (RunLiveResult, error) {
	if s.sys == nil {
		return RunLiveResult{}, fmt.Errorf("run: no rom loaded")
	}
	if err := ctx.Err(); err != nil {
		return RunLiveResult{}, err
	}
	watch, err := validateQuery(p.WatchSpec, p.Framebuffer, p.EventFilter)
	if err != nil {
		return RunLiveResult{}, err
	}
	p.WatchSpec = watch
	inputs, err := s.runInputs(p.Frames, p.Input, p.InputSequence)
	if err != nil {
		return RunLiveResult{}, err
	}
	every := p.Every
	if every <= 0 {
		every = 1
	}
	filter := eventFilter(p.EventFilter)
	if emit == nil && inputs.n > 0 {
		count := (inputs.n-1)/every + 1
		if (inputs.n-1)%every != 0 {
			count++
		}
		// Allow for JSON escaping of watch names and the component hash map.
		summaryBytes := 8192
		for _, field := range p.WatchSpec.Watches {
			summaryBytes += 6*len(field.Name) + 64
		}
		if count > maxSummaries || count*summaryBytes > 3<<20 {
			return RunLiveResult{}, fmt.Errorf("run: response exceeds limit; increase every or use streaming")
		}
	}
	var frames []FrameSummary
	inputHash := sha256.New()
	for i := 0; i < inputs.n; i++ {
		input := inputs.at(i)
		_, _ = inputHash.Write([]byte{byte(input), byte(input >> 8)})
		select {
		case <-ctx.Done():
			return RunLiveResult{}, ctx.Err()
		default:
		}
		if err := s.sys.SetInputState(0, input); err != nil {
			return RunLiveResult{}, err
		}
		if err := s.sys.RunFrame(); err != nil {
			return RunLiveResult{}, err
		}
		if shouldSample(i, inputs.n, every) {
			summary, err := s.frameSummary(i+1, input, p.WatchSpec, filter)
			if err != nil {
				return RunLiveResult{}, err
			}
			if emit != nil {
				if err := emit(summary); err != nil {
					return RunLiveResult{}, err
				}
			}
			if emit == nil {
				frames = append(frames, summary)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return RunLiveResult{}, err
	}
	watches, err := s.readWatches(p.WatchSpec)
	if err != nil {
		return RunLiveResult{}, err
	}
	state, err := s.sys.Serialize()
	if err != nil {
		return RunLiveResult{}, err
	}
	hashes, _ := s.sys.StateHashes()
	out := RunLiveResult{
		Frames:          inputs.n,
		InputHash:       hex.EncodeToString(inputHash.Sum(nil)),
		EventFilter:     p.EventFilter,
		Watches:         watches.Watches,
		FramebufferHash: hashFrame(s.sys.FrameBuffer()),
		ComponentHashes: hashes,
		FinalStateHash:  hashBytes(state),
		FrameSummaries:  frames,
	}
	if strings.EqualFold(p.Framebuffer, "rgba") {
		out.FrameRGBA = base64.StdEncoding.EncodeToString(frameRGBA(s.sys.FrameBuffer()))
	}
	return out, nil
}

// WatchRequest describes watched WRAM fields.
type WatchRequest struct {
	Path    string       `json:"path,omitempty"`
	Watches []WatchField `json:"watches,omitempty"`
}

// WatchField is one watched memory field.
type WatchField struct {
	Name  string `json:"name"`
	Space string `json:"space,omitempty"`
	Addr  uint32 `json:"addr"`
	Width int    `json:"width"`
}

// WatchResult contains watch values.
type WatchResult struct {
	Watches map[string]uint64 `json:"watches"`
}

func (s *Service) readWatches(p WatchRequest) (WatchResult, error) {
	if s.sys == nil {
		return WatchResult{}, fmt.Errorf("read_watches: no rom loaded")
	}
	fields, err := watchFields(p)
	if err != nil {
		return WatchResult{}, err
	}
	out := make(map[string]uint64, len(fields))
	for _, f := range fields {
		if f.Space == "" {
			f.Space = "wram"
		}
		if f.Space != "wram" {
			return WatchResult{}, fmt.Errorf("watch %s: unsupported space %q", f.Name, f.Space)
		}
		if f.Width != 1 && f.Width != 2 {
			return WatchResult{}, fmt.Errorf("watch %s: width must be 1 or 2", f.Name)
		}
		buf := make([]byte, f.Width)
		if _, err := s.sys.ReadWRAMAt(buf, int64(f.Addr)); err != nil {
			return WatchResult{}, err
		}
		v := uint64(buf[0])
		if f.Width == 2 {
			v |= uint64(buf[1]) << 8
		}
		out[f.Name] = v
	}
	return WatchResult{Watches: out}, nil
}

// MemoryRequest describes a raw memory read.
type MemoryRequest struct {
	Space  string `json:"space,omitempty"`
	Addr   uint32 `json:"addr"`
	Length int    `json:"length"`
}

// MemoryResult contains raw memory bytes.
type MemoryResult struct {
	Space  string `json:"space"`
	Addr   uint32 `json:"addr"`
	Bytes  int    `json:"bytes"`
	Data   string `json:"data_base64"`
	Hash   string `json:"hash"`
	Format string `json:"format"`
}

func (s *Service) readMemory(p MemoryRequest) (MemoryResult, error) {
	if s.sys == nil {
		return MemoryResult{}, fmt.Errorf("read_memory: no rom loaded")
	}
	space := p.Space
	if space == "" {
		space = "wram"
	}
	if space != "wram" {
		return MemoryResult{}, fmt.Errorf("read_memory: unsupported space %q", space)
	}
	if p.Length < 0 || p.Addr > 128<<10 || p.Length > (128<<10)-int(p.Addr) {
		return MemoryResult{}, fmt.Errorf("read_memory: range exceeds 128 KiB WRAM")
	}
	data := make([]byte, p.Length)
	n := 0
	var err error
	if len(data) != 0 {
		n, err = s.sys.ReadWRAMAt(data, int64(p.Addr))
	}
	if err != nil {
		return MemoryResult{}, err
	}
	data = data[:n]
	return MemoryResult{
		Space:  space,
		Addr:   p.Addr,
		Bytes:  len(data),
		Data:   base64.StdEncoding.EncodeToString(data),
		Hash:   hashBytes(data),
		Format: "base64",
	}, nil
}

func (s *Service) snapshot(name string) (Checkpoint, error) {
	if s.sys == nil {
		return Checkpoint{}, fmt.Errorf("snapshot: no rom loaded")
	}
	data, err := s.sys.Serialize()
	if err != nil {
		return Checkpoint{}, err
	}
	nextID := s.nextID
	if name == "" {
		name, nextID = s.checkpointName("checkpoint")
	}
	if err := s.storeCheckpoint(name, data); err != nil {
		return Checkpoint{}, err
	}
	s.nextID = nextID
	return Checkpoint{Name: name, Hash: hashBytes(data), Bytes: len(data)}, nil
}

func (s *Service) restore(name string) (Status, error) {
	if s.sys == nil {
		return Status{}, fmt.Errorf("restore: no rom loaded")
	}
	data, ok := s.checkpoint[name]
	if !ok {
		return Status{}, fmt.Errorf("restore: unknown checkpoint %q", name)
	}
	if err := s.sys.Unserialize(data); err != nil {
		return Status{}, err
	}
	s.input = 0
	return s.status("restored"), nil
}

func (s *Service) fork(checkpoint, name string) (Checkpoint, error) {
	data, ok := s.checkpoint[checkpoint]
	if !ok {
		return Checkpoint{}, fmt.Errorf("fork: unknown checkpoint %q", checkpoint)
	}
	nextID := s.nextID
	if name == "" {
		name, nextID = s.checkpointName(checkpoint + "-fork")
	}
	if err := s.storeCheckpoint(name, data); err != nil {
		return Checkpoint{}, err
	}
	s.nextID = nextID
	return Checkpoint{Name: name, Hash: hashBytes(data), Bytes: len(data)}, nil
}

// ExportCheckpointRequest describes a checkpoint export to disk.
type ExportCheckpointRequest struct {
	Checkpoint string `json:"checkpoint"`
	Path       string `json:"path"`
}

// ExportCheckpointResult records a checkpoint export.
type ExportCheckpointResult struct {
	Checkpoint string `json:"checkpoint"`
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Bytes      int    `json:"bytes"`
}

func (s *Service) exportCheckpoint(p ExportCheckpointRequest) (ExportCheckpointResult, error) {
	if p.Checkpoint == "" {
		return ExportCheckpointResult{}, fmt.Errorf("export_checkpoint: missing checkpoint")
	}
	if p.Path == "" {
		return ExportCheckpointResult{}, fmt.Errorf("export_checkpoint: missing path")
	}
	data, ok := s.checkpoint[p.Checkpoint]
	if !ok {
		return ExportCheckpointResult{}, fmt.Errorf("export_checkpoint: unknown checkpoint %q", p.Checkpoint)
	}
	if err := os.WriteFile(p.Path, data, 0o600); err != nil {
		return ExportCheckpointResult{}, fmt.Errorf("write checkpoint: %w", err)
	}
	return ExportCheckpointResult{
		Checkpoint: p.Checkpoint,
		Path:       p.Path,
		Hash:       hashBytes(data),
		Bytes:      len(data),
	}, nil
}

// RunFromCheckpointRequest describes a forked run.
type RunFromCheckpointRequest struct {
	Checkpoint    string       `json:"checkpoint"`
	InputSequence []uint16     `json:"input_sequence"`
	WatchSpec     WatchRequest `json:"watch_spec,omitempty"`
	EventFilter   []string     `json:"event_filter,omitempty"`
	Framebuffer   string       `json:"framebuffer,omitempty"`
}

// RunResult contains a checkpointed run summary.
type RunResult struct {
	Checkpoint      string            `json:"checkpoint"`
	Frames          int               `json:"frames"`
	InputHash       string            `json:"input_hash"`
	EventFilter     []string          `json:"event_filter,omitempty"`
	Watches         map[string]uint64 `json:"watches,omitempty"`
	FramebufferHash string            `json:"framebuffer_hash,omitempty"`
	ComponentHashes map[string]string `json:"component_hashes,omitempty"`
	FinalStateHash  string            `json:"final_state_hash,omitempty"`
	FrameRGBA       string            `json:"frame_rgba_base64,omitempty"`
}

func (s *Service) runFromCheckpoint(ctx context.Context, p RunFromCheckpointRequest) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	if len(p.InputSequence) > MaxFrames {
		return RunResult{}, fmt.Errorf("run_from_checkpoint: frame count exceeds limit %d", MaxFrames)
	}
	watch, err := validateQuery(p.WatchSpec, p.Framebuffer, p.EventFilter)
	if err != nil {
		return RunResult{}, err
	}
	p.WatchSpec = watch

	if s.sys == nil {
		return RunResult{}, fmt.Errorf("run_from_checkpoint: no rom loaded")
	}
	data, ok := s.checkpoint[p.Checkpoint]
	if !ok {
		return RunResult{}, fmt.Errorf("run_from_checkpoint: unknown checkpoint %q", p.Checkpoint)
	}
	if err := s.sys.Unserialize(data); err != nil {
		return RunResult{}, err
	}
	for _, input := range p.InputSequence {
		select {
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		default:
		}
		if err := s.sys.SetInputState(0, input); err != nil {
			return RunResult{}, err
		}
		if err := s.sys.RunFrame(); err != nil {
			return RunResult{}, err
		}
	}
	watches, err := s.readWatches(p.WatchSpec)
	if err != nil {
		return RunResult{}, err
	}
	state, err := s.sys.Serialize()
	if err != nil {
		return RunResult{}, err
	}
	hashes, _ := s.sys.StateHashes()
	out := RunResult{
		Checkpoint:      p.Checkpoint,
		Frames:          len(p.InputSequence),
		InputHash:       hashInputSequence(p.InputSequence),
		EventFilter:     p.EventFilter,
		Watches:         watches.Watches,
		FramebufferHash: hashFrame(s.sys.FrameBuffer()),
		ComponentHashes: hashes,
		FinalStateHash:  hashBytes(state),
	}
	if strings.EqualFold(p.Framebuffer, "rgba") {
		out.FrameRGBA = base64.StdEncoding.EncodeToString(frameRGBA(s.sys.FrameBuffer()))
	}
	return out, nil
}

type runInput struct {
	n        int
	value    uint16
	sequence []uint16
}

func (in runInput) at(i int) uint16 {
	if len(in.sequence) != 0 {
		return in.sequence[i]
	}
	return in.value
}

func (s *Service) runInputs(frames int, input *uint16, sequence []uint16) (runInput, error) {
	if frames < 0 || frames > MaxFrames || len(sequence) > MaxFrames {
		return runInput{}, fmt.Errorf("run: frame count exceeds limit %d", MaxFrames)
	}
	if len(sequence) > 0 {
		if frames > 0 && frames != len(sequence) {
			return runInput{}, fmt.Errorf("run: frames does not match input_sequence length")
		}
		return runInput{n: len(sequence), sequence: sequence}, nil
	}
	value := s.input
	if input != nil {
		value = *input
	}
	return runInput{n: frames, value: value}, nil
}

func (s *Service) frameSummary(frame int, input uint16, watch WatchRequest, filter map[string]bool) (FrameSummary, error) {
	out := FrameSummary{Frame: frame}
	if filter["input"] {
		out.Input = input
	}
	if filter["watch"] {
		watches, err := s.readWatches(watch)
		if err != nil {
			return FrameSummary{}, err
		}
		out.Watches = watches.Watches
	}
	if filter["frame"] || filter["framebuffer"] {
		out.FramebufferHash = hashFrame(s.sys.FrameBuffer())
	}
	if filter["component"] || filter["provenance"] {
		hashes, _ := s.sys.StateHashes()
		out.ComponentHashes = hashes
	}
	return out, nil
}

func eventFilter(events []string) map[string]bool {
	filter := map[string]bool{}
	if len(events) == 0 {
		filter["frame"] = true
		filter["input"] = true
		filter["watch"] = true
		return filter
	}
	for _, event := range events {
		event = strings.ToLower(strings.TrimSpace(event))
		if event != "" {
			filter[event] = true
		}
	}
	return filter
}

func shouldSample(i, total, every int) bool {
	return i%every == 0 || i == total-1
}

// Checkpoint identifies a stored state.
type Checkpoint struct {
	Name  string `json:"name"`
	Hash  string `json:"hash"`
	Bytes int    `json:"bytes"`
}

// Status is a generic status result.
type Status struct {
	Status          string `json:"status"`
	ROMPath         string `json:"rom_path,omitempty"`
	FramebufferHash string `json:"framebuffer_hash,omitempty"`
}

func (s *Service) status(status string) Status {
	out := Status{Status: status, ROMPath: s.romPath}
	if s.sys != nil {
		out.FramebufferHash = hashFrame(s.sys.FrameBuffer())
	}
	return out
}

func decodeParams(data json.RawMessage, v any) error {
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	return nil
}

func watchFields(p WatchRequest) ([]WatchField, error) {
	if len(p.Watches) > maxWatches {
		return nil, fmt.Errorf("watch count exceeds %d", maxWatches)
	}
	var fields []WatchField
	fields = append(fields, p.Watches...)
	if p.Path != "" {
		f, err := os.Open(p.Path)
		if err != nil {
			return nil, fmt.Errorf("open watch spec: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxRequestBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxRequestBytes {
			return nil, fmt.Errorf("watch spec exceeds %d bytes", maxRequestBytes)
		}
		watches, err := trace.ParseWatches(strings.NewReader(string(data)))
		if err != nil {
			return nil, err
		}
		for _, w := range watches {
			fields = append(fields, WatchField{Name: w.Name, Space: w.Range.Space, Addr: w.Range.Start, Width: w.Width})
		}
	}
	if len(fields) > maxWatches {
		return nil, fmt.Errorf("watch count exceeds %d", maxWatches)
	}
	for _, f := range fields {
		if len(f.Name) > 256 || (f.Space != "" && f.Space != "wram") || (f.Width != 1 && f.Width != 2) || f.Addr > (128<<10)-uint32(f.Width) {
			return nil, fmt.Errorf("invalid watch %q", f.Name)
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields, nil
}

func hashFrame(fb []uint16) string {
	buf := make([]byte, 0, len(fb)*2)
	for _, px := range fb {
		buf = append(buf, byte(px), byte(px>>8))
	}
	return hashBytes(buf)
}

func hashInputSequence(inputs []uint16) string {
	buf := make([]byte, 0, len(inputs)*2)
	for _, input := range inputs {
		buf = append(buf, byte(input), byte(input>>8))
	}
	return hashBytes(buf)
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func frameRGBA(fb []uint16) []byte {
	rgba := image.NewRGBA(image.Rect(0, 0, 256, len(fb)/256))
	for i, px := range fb {
		r5 := px & 0x1f
		g5 := (px >> 5) & 0x1f
		b5 := (px >> 10) & 0x1f
		j := i * 4
		rgba.Pix[j] = uint8((r5 * 255) / 31)
		rgba.Pix[j+1] = uint8((g5 * 255) / 31)
		rgba.Pix[j+2] = uint8((b5 * 255) / 31)
		rgba.Pix[j+3] = 0xff
	}
	return rgba.Pix
}

// LoadROM loads and powers a ROM.
func (s *Service) LoadROM(path string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadROM(path)
}

// LoadState restores a state from disk.
func (s *Service) LoadState(path string, allowMismatch bool) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadState(path, allowMismatch)
}

// Reset resets the running system.
func (s *Service) Reset() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reset()
}

// SetInput sets the default input mask used by Run when no input sequence is
// supplied.
func (s *Service) SetInput(input uint16) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setInput(input)
}

// Step runs frames with an input mask.
func (s *Service) Step(p StepRequest) (StepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.step(context.Background(), p)
}

// Run advances from the current state. It can emit sampled frame summaries for
// frame, input, watch, framebuffer, and component-hash provenance.
func (s *Service) Run(ctx context.Context, p RunRequest) (RunLiveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run(ctx, p, nil)
}

// RunStream advances from the current state and calls emit for every sampled
// frame summary before returning the final run result. Streamed summaries are not
// retained in the result. The callback must honor cancellation and must not call
// methods on this Service; the run owns the machine until the callback returns.
func (s *Service) RunStream(ctx context.Context, p RunRequest, emit func(FrameSummary) error) (RunLiveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run(ctx, p, emit)
}

// ReadWatches reads watched fields.
func (s *Service) ReadWatches(p WatchRequest) (WatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readWatches(p)
}

// ReadMemory reads raw memory. The first slice supports WRAM, which is the
// stable writable memory surface used by replay/watch consumers.
func (s *Service) ReadMemory(p MemoryRequest) (MemoryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readMemory(p)
}

// Snapshot serializes the current state into a named checkpoint.
func (s *Service) Snapshot(name string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot(name)
}

// Restore restores a named checkpoint.
func (s *Service) Restore(name string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restore(name)
}

// Fork creates a copy of a checkpoint under a new name.
func (s *Service) Fork(checkpoint, name string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fork(checkpoint, name)
}

// ExportCheckpoint writes exact serialized checkpoint bytes to disk.
func (s *Service) ExportCheckpoint(p ExportCheckpointRequest) (ExportCheckpointResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exportCheckpoint(p)
}

// RunFromCheckpoint restores checkpoint, runs the input sequence, and reports
// the requested watch and hash surfaces.
func (s *Service) RunFromCheckpoint(ctx context.Context, p RunFromCheckpointRequest) (RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runFromCheckpoint(ctx, p)
}

// StepContext runs frames, checking ctx before setup and between frames.
func (s *Service) StepContext(ctx context.Context, p StepRequest) (StepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.step(ctx, p)
}

// MaxFrames bounds work accepted by one local control request.
const MaxFrames = 36000

const (
	maxRequestBytes    = 1 << 20
	maxSummaries       = 1024
	maxWatches         = 128
	maxCheckpointCount = 64
	maxCheckpointBytes = 256 << 20
)

// checkpointName selects an unused name without committing the counter. At
// most len(checkpoint)+1 candidates are needed because stored names are unique.
func (s *Service) checkpointName(prefix string) (string, int) {
	for id := s.nextID + 1; ; id++ {
		name := fmt.Sprintf("%s-%d", prefix, id)
		if _, exists := s.checkpoint[name]; !exists {
			return name, id
		}
	}
}

func (s *Service) storeCheckpoint(name string, data []byte) error {
	if len(name) > 256 {
		return fmt.Errorf("checkpoint name exceeds 256 bytes")
	}
	if _, ok := s.checkpoint[name]; !ok && len(s.checkpoint) >= maxCheckpointCount {
		return fmt.Errorf("checkpoint count exceeds %d", maxCheckpointCount)
	}
	total := len(data)
	for key, value := range s.checkpoint {
		if key != name {
			total += len(value)
		}
	}
	if total > maxCheckpointBytes {
		return fmt.Errorf("checkpoint storage exceeds %d bytes", maxCheckpointBytes)
	}
	s.checkpoint[name] = append([]byte(nil), data...)
	return nil
}

func validateQuery(watch WatchRequest, framebuffer string, events []string) (WatchRequest, error) {
	if len(events) > 16 {
		return WatchRequest{}, fmt.Errorf("event filter exceeds 16 entries")
	}
	for _, event := range events {
		if len(event) > 64 {
			return WatchRequest{}, fmt.Errorf("event name exceeds 64 bytes")
		}
	}
	if framebuffer != "" && !strings.EqualFold(framebuffer, "rgba") && !strings.EqualFold(framebuffer, "hash") {
		return WatchRequest{}, fmt.Errorf("unsupported framebuffer %q", framebuffer)
	}
	for event := range eventFilter(events) {
		switch event {
		case "frame", "input", "watch", "framebuffer", "component", "provenance":
		default:
			return WatchRequest{}, fmt.Errorf("unsupported event %q", event)
		}
	}
	fields, err := watchFields(watch)
	if err != nil {
		return WatchRequest{}, err
	}
	return WatchRequest{Watches: fields}, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}
