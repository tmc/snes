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
	switch method {
	case "load_rom":
		var p struct {
			Path string `json:"path"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.LoadROM(p.Path)
	case "load_state":
		var p struct {
			Path                  string `json:"path"`
			AllowStateROMMismatch bool   `json:"allow_state_rom_mismatch,omitempty"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.LoadState(p.Path, p.AllowStateROMMismatch)
	case "reset":
		return s.Reset()
	case "set_input":
		var p struct {
			Input uint16 `json:"input"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.SetInput(p.Input)
	case "step":
		var p StepRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.Step(p)
	case "run":
		var p RunRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.Run(ctx, p)
	case "read_watches":
		var p WatchRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.ReadWatches(p)
	case "read_memory":
		var p MemoryRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.ReadMemory(p)
	case "snapshot":
		var p struct {
			Name string `json:"name,omitempty"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.Snapshot(p.Name)
	case "restore":
		var p struct {
			Checkpoint string `json:"checkpoint"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.Restore(p.Checkpoint)
	case "fork":
		var p struct {
			Checkpoint string `json:"checkpoint"`
			Name       string `json:"name,omitempty"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.Fork(p.Checkpoint, p.Name)
	case "run_from_checkpoint":
		var p RunFromCheckpointRequest
		if err := decodeParams(params, &p); err != nil {
			return nil, err
		}
		return s.RunFromCheckpoint(ctx, p)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

// LoadROM loads and powers a ROM.
func (s *Service) LoadROM(path string) (Status, error) {
	if path == "" {
		return Status{}, fmt.Errorf("load_rom: missing path")
	}
	data, err := os.ReadFile(path)
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

// LoadState restores a state from disk.
func (s *Service) LoadState(path string, allowMismatch bool) (Status, error) {
	if s.sys == nil {
		return Status{}, fmt.Errorf("load_state: no rom loaded")
	}
	data, err := os.ReadFile(path)
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

// Reset resets the running system.
func (s *Service) Reset() (Status, error) {
	if s.sys == nil {
		return Status{}, fmt.Errorf("reset: no rom loaded")
	}
	s.sys.Reset()
	s.input = 0
	return s.status("reset"), nil
}

// SetInput sets the default input mask used by Run when no input sequence is
// supplied.
func (s *Service) SetInput(input uint16) (Status, error) {
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

// Step runs frames with an input mask.
func (s *Service) Step(p StepRequest) (StepResult, error) {
	if s.sys == nil {
		return StepResult{}, fmt.Errorf("step: no rom loaded")
	}
	if p.Frames < 0 {
		return StepResult{}, fmt.Errorf("step: frames must be >= 0")
	}
	for i := 0; i < p.Frames; i++ {
		if err := s.sys.SetInputState(0, p.Input); err != nil {
			return StepResult{}, err
		}
		if err := s.sys.RunFrame(); err != nil {
			return StepResult{}, err
		}
	}
	watches, err := s.ReadWatches(p.WatchSpec)
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

// Run advances from the current state. It can emit sampled frame summaries for
// frame, input, watch, framebuffer, and component-hash provenance.
func (s *Service) Run(ctx context.Context, p RunRequest) (RunLiveResult, error) {
	return s.run(ctx, p, nil)
}

// RunStream advances from the current state and calls emit for every sampled
// frame summary before returning the final run result.
func (s *Service) RunStream(ctx context.Context, p RunRequest, emit func(FrameSummary) error) (RunLiveResult, error) {
	return s.run(ctx, p, emit)
}

func (s *Service) run(ctx context.Context, p RunRequest, emit func(FrameSummary) error) (RunLiveResult, error) {
	if s.sys == nil {
		return RunLiveResult{}, fmt.Errorf("run: no rom loaded")
	}
	inputs, err := s.runInputs(p.Frames, p.Input, p.InputSequence)
	if err != nil {
		return RunLiveResult{}, err
	}
	every := p.Every
	if every <= 0 {
		every = 1
	}
	filter := eventFilter(p.EventFilter)
	var frames []FrameSummary
	for i, input := range inputs {
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
		if shouldSample(i, len(inputs), every) {
			summary, err := s.frameSummary(i+1, input, p.WatchSpec, filter)
			if err != nil {
				return RunLiveResult{}, err
			}
			if emit != nil {
				if err := emit(summary); err != nil {
					return RunLiveResult{}, err
				}
			}
			frames = append(frames, summary)
		}
	}
	watches, err := s.ReadWatches(p.WatchSpec)
	if err != nil {
		return RunLiveResult{}, err
	}
	state, err := s.sys.Serialize()
	if err != nil {
		return RunLiveResult{}, err
	}
	hashes, _ := s.sys.StateHashes()
	out := RunLiveResult{
		Frames:          len(inputs),
		InputHash:       hashInputSequence(inputs),
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

// ReadWatches reads watched fields.
func (s *Service) ReadWatches(p WatchRequest) (WatchResult, error) {
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

// ReadMemory reads raw memory. The first slice supports WRAM, which is the
// stable writable memory surface used by replay/watch consumers.
func (s *Service) ReadMemory(p MemoryRequest) (MemoryResult, error) {
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
	if p.Length < 0 {
		return MemoryResult{}, fmt.Errorf("read_memory: length must be >= 0")
	}
	data := make([]byte, p.Length)
	n, err := s.sys.ReadWRAMAt(data, int64(p.Addr))
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

// Snapshot serializes the current state into a named checkpoint.
func (s *Service) Snapshot(name string) (Checkpoint, error) {
	if s.sys == nil {
		return Checkpoint{}, fmt.Errorf("snapshot: no rom loaded")
	}
	data, err := s.sys.Serialize()
	if err != nil {
		return Checkpoint{}, err
	}
	if name == "" {
		s.nextID++
		name = fmt.Sprintf("checkpoint-%d", s.nextID)
	}
	s.checkpoint[name] = append([]byte(nil), data...)
	return Checkpoint{Name: name, Hash: hashBytes(data), Bytes: len(data)}, nil
}

// Restore restores a named checkpoint.
func (s *Service) Restore(name string) (Status, error) {
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

// Fork creates a copy of a checkpoint under a new name.
func (s *Service) Fork(checkpoint, name string) (Checkpoint, error) {
	data, ok := s.checkpoint[checkpoint]
	if !ok {
		return Checkpoint{}, fmt.Errorf("fork: unknown checkpoint %q", checkpoint)
	}
	if name == "" {
		s.nextID++
		name = fmt.Sprintf("%s-fork-%d", checkpoint, s.nextID)
	}
	s.checkpoint[name] = append([]byte(nil), data...)
	return Checkpoint{Name: name, Hash: hashBytes(data), Bytes: len(data)}, nil
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

// RunFromCheckpoint restores checkpoint, runs the input sequence, and reports
// the requested watch and hash surfaces.
func (s *Service) RunFromCheckpoint(ctx context.Context, p RunFromCheckpointRequest) (RunResult, error) {
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
	watches, err := s.ReadWatches(p.WatchSpec)
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

func (s *Service) runInputs(frames int, input *uint16, sequence []uint16) ([]uint16, error) {
	if len(sequence) > 0 {
		if frames > 0 && frames != len(sequence) {
			return nil, fmt.Errorf("run: frames=%d does not match input_sequence length %d", frames, len(sequence))
		}
		return append([]uint16(nil), sequence...), nil
	}
	if frames < 0 {
		return nil, fmt.Errorf("run: frames must be >= 0")
	}
	state := s.input
	if input != nil {
		state = *input
	}
	out := make([]uint16, frames)
	for i := range out {
		out[i] = state
	}
	return out, nil
}

func (s *Service) frameSummary(frame int, input uint16, watch WatchRequest, filter map[string]bool) (FrameSummary, error) {
	out := FrameSummary{Frame: frame}
	if filter["input"] {
		out.Input = input
	}
	if filter["watch"] {
		watches, err := s.ReadWatches(watch)
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
	var fields []WatchField
	fields = append(fields, p.Watches...)
	if p.Path != "" {
		f, err := os.Open(p.Path)
		if err != nil {
			return nil, fmt.Errorf("open watch spec: %w", err)
		}
		defer f.Close()
		watches, err := trace.ParseWatches(f)
		if err != nil {
			return nil, err
		}
		for _, w := range watches {
			fields = append(fields, WatchField{Name: w.Name, Space: w.Range.Space, Addr: w.Range.Start, Width: w.Width})
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
