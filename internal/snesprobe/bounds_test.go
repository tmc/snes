package snesprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func loadedService(t *testing.T) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sfc")
	if err := os.WriteFile(path, testROM(), 0600); err != nil {
		t.Fatal(err)
	}
	s := New()
	if _, err := s.LoadROM(path); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReadMemoryBounds(t *testing.T) {
	s := loadedService(t)
	for _, tc := range []struct {
		name   string
		addr   uint32
		length int
		bad    bool
	}{
		{"empty", 0, 0, false}, {"empty end", 128 << 10, 0, false}, {"last", (128 << 10) - 1, 1, false}, {"whole", 0, 128 << 10, false},
		{"negative", 0, -1, true}, {"overrun", (128 << 10) - 1, 2, true}, {"past end", (128 << 10) + 1, 0, true}, {"huge", 0, int(^uint(0) >> 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := s.ReadMemory(MemoryRequest{Addr: tc.addr, Length: tc.length})
			if (err != nil) != tc.bad {
				t.Fatalf("read = %+v, %v", out, err)
			}
			if !tc.bad && out.Bytes != tc.length {
				t.Fatalf("bytes = %d", out.Bytes)
			}
		})
	}
}

func TestStepCancellation(t *testing.T) {
	s := loadedService(t)
	before, err := s.Snapshot("before")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.StepContext(ctx, StepRequest{Frames: MaxFrames}); !errors.Is(err, context.Canceled) {
		t.Fatalf("step = %v", err)
	}
	after, err := s.Snapshot("after")
	if err != nil {
		t.Fatal(err)
	}
	if before.Hash != after.Hash {
		t.Fatal("canceled step changed state")
	}
	if _, err := s.StepContext(context.Background(), StepRequest{Frames: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidQueryDoesNotAdvance(t *testing.T) {
	s := loadedService(t)
	before, _ := s.Snapshot("before")
	watch := WatchRequest{Watches: []WatchField{{Name: "bad", Width: 2, Addr: (128 << 10) - 1}}}
	for _, run := range []func() error{
		func() error { _, err := s.Step(StepRequest{Frames: 1, WatchSpec: watch}); return err },
		func() error {
			_, err := s.Run(context.Background(), RunRequest{Frames: 1, EventFilter: []string{"typo"}})
			return err
		},
		func() error {
			_, err := s.RunFromCheckpoint(context.Background(), RunFromCheckpointRequest{Checkpoint: "before", InputSequence: []uint16{0}, WatchSpec: watch})
			return err
		},
		func() error { _, err := s.Run(context.Background(), RunRequest{Frames: MaxFrames + 1}); return err },
	} {
		if err := run(); err == nil {
			t.Fatal("invalid request succeeded")
		}
	}
	after, _ := s.Snapshot("after")
	if before.Hash != after.Hash {
		t.Fatal("invalid request changed state")
	}
}

func TestRunStreamCancellationAndRetention(t *testing.T) {
	s := loadedService(t)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	_, err := s.RunStream(ctx, RunRequest{Frames: MaxFrames}, func(FrameSummary) error { n++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || n != 1 {
		t.Fatalf("stream emitted %d, error %v", n, err)
	}
	n = 0
	out, err := s.RunStream(context.Background(), RunRequest{Frames: 3}, func(FrameSummary) error { n++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(out.FrameSummaries) != 0 || out.Frames != 3 {
		t.Fatalf("stream = %+v, emitted %d", out, n)
	}
	inputs, err := s.runInputs(MaxFrames, nil, nil)
	if err != nil || len(inputs.sequence) != 0 || inputs.n != MaxFrames {
		t.Fatalf("scalar input allocated sequence: %+v, %v", inputs, err)
	}
}

func TestServiceConcurrentTransports(t *testing.T) {
	s := loadedService(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := s.RunStream(context.Background(), RunRequest{Frames: 1}, func(FrameSummary) error { close(entered); <-release; return nil })
		finished <- err
	}()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("snapshot-%d", i)
			// JSON dispatch and public entry points must use the same ownership rule.
			if i%2 == 0 {
				if _, err := s.Handle(context.Background(), "snapshot", []byte(fmt.Sprintf(`{"name":%q}`, name))); err != nil {
					t.Error(err)
				}
			} else {
				if _, err := s.Snapshot(name); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	var hash string
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("snapshot-%d", i)
		if _, err := s.Restore(name); err != nil {
			t.Fatal(err)
		}
		cp, err := s.Snapshot("verify")
		if err != nil {
			t.Fatal(err)
		}
		if hash != "" && hash != cp.Hash {
			t.Fatal("concurrent snapshots differ")
		}
		hash = cp.Hash
	}
}

func TestCheckpointLimits(t *testing.T) {
	s := New()
	for i := 0; i < maxCheckpointCount; i++ {
		if err := s.storeCheckpoint(fmt.Sprint(i), []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.storeCheckpoint("overflow", []byte{2}); err == nil {
		t.Fatal("checkpoint count unbounded")
	}
	if err := s.storeCheckpoint("0", []byte{3}); err != nil {
		t.Fatal(err)
	}
	if s.checkpoint["0"][0] != 3 {
		t.Fatal("replacement failed")
	}
}

func ExampleService_StepContext() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().StepContext(ctx, StepRequest{})
	fmt.Println(err != nil)
	// Output: true
}

func TestServiceConcurrentSockets(t *testing.T) {
	s := loadedService(t)
	path := s.romPath
	type client struct {
		conn net.Conn
		enc  *json.Encoder
		dec  *json.Decoder
	}
	var clients []client
	var served sync.WaitGroup
	for i := 0; i < 2; i++ {
		server, conn := net.Pipe()
		served.Add(1)
		go func() {
			defer served.Done()
			defer server.Close()
			if err := s.Serve(context.Background(), server, server); err != nil {
				t.Error(err)
			}
		}()
		clients = append(clients, client{conn, json.NewEncoder(conn), json.NewDecoder(conn)})
	}
	defer func() {
		for _, c := range clients {
			c.conn.Close()
		}
		served.Wait()
	}()
	phase := func(method string, params func(int) any) {
		var wg sync.WaitGroup
		for i, c := range clients {
			wg.Add(1)
			go func(i int, c client) {
				defer wg.Done()
				data, err := json.Marshal(params(i))
				if err != nil {
					t.Error(err)
					return
				}
				if err := c.enc.Encode(Request{Method: method, Params: data}); err != nil {
					t.Error(err)
					return
				}
				var response Response
				if err := c.dec.Decode(&response); err != nil {
					t.Error(err)
					return
				}
				if response.Error != "" {
					t.Error(response.Error)
				}
			}(i, c)
		}
		wg.Wait()
	}
	phase("load_rom", func(int) any { return map[string]any{"path": path} })
	phase("step", func(int) any { return StepRequest{Frames: 1} })
	want, err := s.Snapshot("want")
	if err != nil {
		t.Fatal(err)
	}
	phase("snapshot", func(i int) any { return map[string]any{"name": fmt.Sprintf("client-%d", i)} })
	phase("step", func(int) any { return StepRequest{Frames: 1} })
	phase("restore", func(i int) any { return map[string]any{"checkpoint": fmt.Sprintf("client-%d", i)} })
	got, err := s.Snapshot("got")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != want.Hash {
		t.Fatalf("restored state %s, want %s", got.Hash, want.Hash)
	}
}
