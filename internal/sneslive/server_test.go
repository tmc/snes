package sneslive

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/snes/emulator"
	"github.com/tmc/snes/internal/sneslive/v1"
	"github.com/tmc/snes/internal/snesprobe"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestLiveServiceCheckpointForkAndStream(t *testing.T) {
	client, cleanup := newTestClient(t)
	defer cleanup()

	dir := t.TempDir()
	romPath := filepath.Join(dir, "test.sfc")
	if err := os.WriteFile(romPath, testROM(), 0o666); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := context.Background()
	load, err := client.LoadROM(ctx, &sneslivev1.LoadROMRequest{Path: romPath})
	if err != nil {
		t.Fatalf("LoadROM: %v", err)
	}
	status := load.GetStatus()
	if status.GetStatus() != "loaded" || status.GetFramebufferHash() == "" {
		t.Fatalf("status = %+v", status)
	}

	snapshot, err := client.Snapshot(ctx, &sneslivev1.SnapshotRequest{Name: "frame0"})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	cp := snapshot.GetCheckpoint()
	forkResp, err := client.Fork(ctx, &sneslivev1.ForkRequest{Checkpoint: "frame0", Name: "branch"})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	fork := forkResp.GetCheckpoint()
	if cp.GetHash() == "" || fork.GetHash() != cp.GetHash() {
		t.Fatalf("checkpoint = %+v fork = %+v", cp, fork)
	}

	watches := &sneslivev1.WatchSpec{Watches: []*sneslivev1.WatchField{{
		Name: "w0", Space: "wram", Addr: 0, Width: 1,
	}}}
	runResp, err := client.RunFromCheckpoint(ctx, &sneslivev1.RunFromCheckpointRequest{
		Checkpoint:    "frame0",
		InputSequence: []uint32{uint32(emulator.StandardButtonRight), uint32(emulator.StandardButtonRight)},
		WatchSpec:     watches,
		EventFilter:   []string{"frame", "input", "watch"},
	})
	if err != nil {
		t.Fatalf("RunFromCheckpoint: %v", err)
	}
	run := runResp.GetResult()
	if run.GetCheckpoint() != "frame0" || run.GetInputHash() == "" || run.GetFinalStateHash() == "" {
		t.Fatalf("run = %+v", run)
	}
	if _, ok := run.GetWatches()["w0"]; !ok {
		t.Fatalf("run missing watch: %+v", run.GetWatches())
	}

	stream, err := client.RunStream(ctx, &sneslivev1.RunStreamRequest{
		Run: &sneslivev1.RunRequest{
			Frames:      3,
			Input:       ptr(uint32(emulator.StandardButtonA)),
			WatchSpec:   watches,
			EventFilter: []string{"frame", "input", "watch", "component"},
			Every:       2,
		},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	var frames int
	var final *sneslivev1.RunResult
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err != io.EOF {
				t.Fatalf("stream Recv: %v", err)
			}
			break
		}
		if f := ev.GetFrame(); f != nil {
			frames++
			if f.GetFramebufferHash() == "" || f.GetComponentHashes() == nil {
				t.Fatalf("frame missing provenance: %+v", f)
			}
		}
		if r := ev.GetFinal(); r != nil {
			final = r
		}
	}
	if frames != 2 || final == nil || final.GetFrames() != 3 || final.GetFinalStateHash() == "" {
		t.Fatalf("stream frames=%d final=%+v", frames, final)
	}

	mem, err := client.ReadMemory(ctx, &sneslivev1.ReadMemoryRequest{Space: "wram", Length: 8})
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if mem.GetBytes() != 8 || len(mem.GetData()) != 8 || mem.GetHash() == "" {
		t.Fatalf("memory = %+v", mem)
	}
}

func newTestClient(t *testing.T) (sneslivev1.SNESLiveServiceClient, func()) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	Register(grpcServer, snesprobe.New())
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	return sneslivev1.NewSNESLiveServiceClient(conn), func() {
		conn.Close()
		grpcServer.Stop()
		lis.Close()
	}
}

func ptr[T any](v T) *T {
	return &v
}

func testROM() []byte {
	rom := make([]byte, 0x8000)
	for i := 0; i < 0x100; i++ {
		rom[i] = 0xea
	}
	rom[0x7fd5] = 0x20
	rom[0x7ffc] = 0x00
	rom[0x7ffd] = 0x80
	return rom
}

func TestStepContextAndNilRun(t *testing.T) {
	svc := snesprobe.New()
	path := filepath.Join(t.TempDir(), "test.sfc")
	if err := os.WriteFile(path, testROM(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.LoadROM(path); err != nil {
		t.Fatal(err)
	}
	server := NewServer(svc)
	before, err := svc.Snapshot("before")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := server.Step(ctx, &sneslivev1.StepRequest{Frames: 1}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled step: %v", err)
	}
	after, err := svc.Snapshot("after")
	if err != nil {
		t.Fatal(err)
	}
	if before.Hash != after.Hash {
		t.Fatal("canceled gRPC step changed state")
	}
	if _, err := server.Run(context.Background(), nil); err != nil {
		t.Fatalf("nil run: %v", err)
	}
}
