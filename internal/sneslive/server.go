// Package sneslive adapts the SNES probe service to the snes.live.v1 gRPC API.
package sneslive

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"sync"

	"github.com/tmc/snes/internal/sneslive/v1"
	"github.com/tmc/snes/internal/snesprobe"
	"google.golang.org/grpc"
)

// Server implements snes.live.v1.SNESLiveServiceServer.
type Server struct {
	sneslivev1.UnimplementedSNESLiveServiceServer

	mu  sync.Mutex
	svc *snesprobe.Service
}

// NewServer returns a gRPC server wrapper for svc.
func NewServer(svc *snesprobe.Service) *Server {
	if svc == nil {
		svc = snesprobe.New()
	}
	return &Server{svc: svc}
}

// Register registers svc on grpcServer.
func Register(grpcServer *grpc.Server, svc *snesprobe.Service) {
	sneslivev1.RegisterSNESLiveServiceServer(grpcServer, NewServer(svc))
}

func (s *Server) LoadROM(_ context.Context, req *sneslivev1.LoadROMRequest) (*sneslivev1.LoadROMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.LoadROM(req.GetPath())
	if err != nil {
		return nil, err
	}
	return &sneslivev1.LoadROMResponse{Status: statusProto(out)}, nil
}

func (s *Server) LoadState(_ context.Context, req *sneslivev1.LoadStateRequest) (*sneslivev1.LoadStateResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.LoadState(req.GetPath(), req.GetAllowStateRomMismatch())
	if err != nil {
		return nil, err
	}
	return &sneslivev1.LoadStateResponse{Status: statusProto(out)}, nil
}

func (s *Server) Reset(context.Context, *sneslivev1.ResetRequest) (*sneslivev1.ResetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.Reset()
	if err != nil {
		return nil, err
	}
	return &sneslivev1.ResetResponse{Status: statusProto(out)}, nil
}

func (s *Server) SetInput(_ context.Context, req *sneslivev1.SetInputRequest) (*sneslivev1.SetInputResponse, error) {
	input, err := input16(req.GetInput())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.SetInput(input)
	if err != nil {
		return nil, err
	}
	return &sneslivev1.SetInputResponse{Status: statusProto(out)}, nil
}

func (s *Server) Step(_ context.Context, req *sneslivev1.StepRequest) (*sneslivev1.StepResponse, error) {
	input, err := input16(req.GetInput())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.Step(snesprobe.StepRequest{
		Frames:      int(req.GetFrames()),
		Input:       input,
		WatchSpec:   watchSpec(req.GetWatchSpec()),
		Framebuffer: req.GetFramebuffer(),
	})
	if err != nil {
		return nil, err
	}
	return stepProto(out)
}

func (s *Server) Run(ctx context.Context, req *sneslivev1.RunRequest) (*sneslivev1.RunResponse, error) {
	runReq, err := runRequest(req)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.Run(ctx, runReq)
	if err != nil {
		return nil, err
	}
	return runProto(out)
}

func (s *Server) RunStream(req *sneslivev1.RunStreamRequest, stream sneslivev1.SNESLiveService_RunStreamServer) error {
	runReq, err := runRequest(req.GetRun())
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.RunStream(stream.Context(), runReq, func(frame snesprobe.FrameSummary) error {
		return stream.Send(&sneslivev1.RunStreamResponse{
			Event: &sneslivev1.RunStreamResponse_Frame{Frame: frameProto(frame)},
		})
	})
	if err != nil {
		return err
	}
	final, err := runResultProto(out)
	if err != nil {
		return err
	}
	return stream.Send(&sneslivev1.RunStreamResponse{
		Event: &sneslivev1.RunStreamResponse_Final{Final: final},
	})
}

func (s *Server) ReadWatches(_ context.Context, req *sneslivev1.ReadWatchesRequest) (*sneslivev1.ReadWatchesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.ReadWatches(watchSpec(req.GetWatchSpec()))
	if err != nil {
		return nil, err
	}
	return &sneslivev1.ReadWatchesResponse{Watches: out.Watches}, nil
}

func (s *Server) ReadMemory(_ context.Context, req *sneslivev1.ReadMemoryRequest) (*sneslivev1.ReadMemoryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.ReadMemory(snesprobe.MemoryRequest{
		Space:  req.GetSpace(),
		Addr:   req.GetAddr(),
		Length: int(req.GetLength()),
	})
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return nil, fmt.Errorf("decode memory result: %w", err)
	}
	return &sneslivev1.ReadMemoryResponse{
		Space:  out.Space,
		Addr:   out.Addr,
		Bytes:  uint32(out.Bytes),
		Data:   data,
		Hash:   out.Hash,
		Format: out.Format,
	}, nil
}

func (s *Server) Snapshot(_ context.Context, req *sneslivev1.SnapshotRequest) (*sneslivev1.SnapshotResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.Snapshot(req.GetName())
	if err != nil {
		return nil, err
	}
	return &sneslivev1.SnapshotResponse{Checkpoint: checkpointProto(out)}, nil
}

func (s *Server) Restore(_ context.Context, req *sneslivev1.RestoreRequest) (*sneslivev1.RestoreResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.Restore(req.GetCheckpoint())
	if err != nil {
		return nil, err
	}
	return &sneslivev1.RestoreResponse{Status: statusProto(out)}, nil
}

func (s *Server) Fork(_ context.Context, req *sneslivev1.ForkRequest) (*sneslivev1.ForkResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.Fork(req.GetCheckpoint(), req.GetName())
	if err != nil {
		return nil, err
	}
	return &sneslivev1.ForkResponse{Checkpoint: checkpointProto(out)}, nil
}

func (s *Server) RunFromCheckpoint(ctx context.Context, req *sneslivev1.RunFromCheckpointRequest) (*sneslivev1.RunFromCheckpointResponse, error) {
	inputs, err := inputSequence(req.GetInputSequence())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := s.svc.RunFromCheckpoint(ctx, snesprobe.RunFromCheckpointRequest{
		Checkpoint:    req.GetCheckpoint(),
		InputSequence: inputs,
		WatchSpec:     watchSpec(req.GetWatchSpec()),
		EventFilter:   append([]string(nil), req.GetEventFilter()...),
		Framebuffer:   req.GetFramebuffer(),
	})
	if err != nil {
		return nil, err
	}
	result, err := runFromCheckpointResultProto(out)
	if err != nil {
		return nil, err
	}
	return &sneslivev1.RunFromCheckpointResponse{Result: result}, nil
}

func runRequest(req *sneslivev1.RunRequest) (snesprobe.RunRequest, error) {
	inputs, err := inputSequence(req.GetInputSequence())
	if err != nil {
		return snesprobe.RunRequest{}, err
	}
	var input *uint16
	if req.Input != nil {
		v, err := input16(req.GetInput())
		if err != nil {
			return snesprobe.RunRequest{}, err
		}
		input = &v
	}
	return snesprobe.RunRequest{
		Frames:        int(req.GetFrames()),
		Input:         input,
		InputSequence: inputs,
		WatchSpec:     watchSpec(req.GetWatchSpec()),
		EventFilter:   append([]string(nil), req.GetEventFilter()...),
		Framebuffer:   req.GetFramebuffer(),
		Every:         int(req.GetEvery()),
	}, nil
}

func watchSpec(spec *sneslivev1.WatchSpec) snesprobe.WatchRequest {
	if spec == nil {
		return snesprobe.WatchRequest{}
	}
	out := snesprobe.WatchRequest{Path: spec.GetPath()}
	for _, w := range spec.GetWatches() {
		out.Watches = append(out.Watches, snesprobe.WatchField{
			Name:  w.GetName(),
			Space: w.GetSpace(),
			Addr:  w.GetAddr(),
			Width: int(w.GetWidth()),
		})
	}
	return out
}

func statusProto(s snesprobe.Status) *sneslivev1.Status {
	return &sneslivev1.Status{
		Status:          s.Status,
		RomPath:         s.ROMPath,
		FramebufferHash: s.FramebufferHash,
	}
}

func checkpointProto(c snesprobe.Checkpoint) *sneslivev1.Checkpoint {
	return &sneslivev1.Checkpoint{
		Name:  c.Name,
		Hash:  c.Hash,
		Bytes: uint64(c.Bytes),
	}
}

func stepProto(r snesprobe.StepResult) (*sneslivev1.StepResponse, error) {
	frame, err := optionalBase64(r.FrameRGBA)
	if err != nil {
		return nil, err
	}
	return &sneslivev1.StepResponse{
		Frames:          uint32(r.Frames),
		Input:           uint32(r.Input),
		Watches:         r.Watches,
		FramebufferHash: r.FramebufferHash,
		FrameRgba:       frame,
	}, nil
}

func runProto(r snesprobe.RunLiveResult) (*sneslivev1.RunResponse, error) {
	result, err := runResultProto(r)
	if err != nil {
		return nil, err
	}
	return &sneslivev1.RunResponse{Result: result}, nil
}

func runResultProto(r snesprobe.RunLiveResult) (*sneslivev1.RunResult, error) {
	frame, err := optionalBase64(r.FrameRGBA)
	if err != nil {
		return nil, err
	}
	out := &sneslivev1.RunResult{
		Frames:          uint32(r.Frames),
		InputHash:       r.InputHash,
		EventFilter:     append([]string(nil), r.EventFilter...),
		Watches:         r.Watches,
		FramebufferHash: r.FramebufferHash,
		ComponentHashes: r.ComponentHashes,
		FinalStateHash:  r.FinalStateHash,
		FrameRgba:       frame,
	}
	for _, f := range r.FrameSummaries {
		out.FrameSummaries = append(out.FrameSummaries, frameProto(f))
	}
	return out, nil
}

func runFromCheckpointResultProto(r snesprobe.RunResult) (*sneslivev1.RunResult, error) {
	frame, err := optionalBase64(r.FrameRGBA)
	if err != nil {
		return nil, err
	}
	return &sneslivev1.RunResult{
		Frames:          uint32(r.Frames),
		InputHash:       r.InputHash,
		EventFilter:     append([]string(nil), r.EventFilter...),
		Watches:         r.Watches,
		FramebufferHash: r.FramebufferHash,
		ComponentHashes: r.ComponentHashes,
		FinalStateHash:  r.FinalStateHash,
		FrameRgba:       frame,
		Checkpoint:      r.Checkpoint,
	}, nil
}

func frameProto(f snesprobe.FrameSummary) *sneslivev1.FrameSummary {
	return &sneslivev1.FrameSummary{
		Frame:           uint32(f.Frame),
		Input:           uint32(f.Input),
		Watches:         f.Watches,
		FramebufferHash: f.FramebufferHash,
		ComponentHashes: f.ComponentHashes,
	}
}

func inputSequence(in []uint32) ([]uint16, error) {
	out := make([]uint16, 0, len(in))
	for _, v := range in {
		u, err := input16(v)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func input16(v uint32) (uint16, error) {
	if v > math.MaxUint16 {
		return 0, fmt.Errorf("input mask %d overflows uint16", v)
	}
	return uint16(v), nil
}

func optionalBase64(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode frame rgba: %w", err)
	}
	return out, nil
}
