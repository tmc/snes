package main

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/snes/internal/snesagent"
)

type failedWriter struct {
	net.Conn
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *failedWriter) Write([]byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return 0, errors.New("controlled write failure")
}

func waitSocketTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("socket worker did not terminate")
	}
}

func TestAgentSocketDisconnectBroadcast(t *testing.T) {
	h, err := listenAgentSocket(filepath.Join(t.TempDir(), "agent.sock"), snesagent.FormatJSONL)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	a, b := net.Pipe()
	defer b.Close()
	conn := &failedWriter{Conn: a, entered: make(chan struct{}), release: make(chan struct{})}
	client := &agentSocketClient{conn: conn, send: make(chan snesagent.Observation, 1)}
	h.mu.Lock()
	h.clients[conn] = client
	h.mu.Unlock()
	written := make(chan struct{})
	read := make(chan struct{})
	go func() { h.write(client); close(written) }()
	go func() { h.read(conn); close(read) }()
	h.Broadcast(snesagent.Observation{})
	waitSocketTest(t, conn.entered)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				h.Broadcast(snesagent.Observation{})
			}
		}()
	}
	close(conn.release)
	waitSocketTest(t, written)
	waitSocketTest(t, read)
	h.Close()
	h.Close()
	wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) != 0 {
		t.Fatalf("retained %d clients", len(h.clients))
	}
}

type terminalReader struct {
	net.Conn
	reads atomic.Int32
}

func (c *terminalReader) Read([]byte) (int, error) {
	c.reads.Add(1)
	return 0, errors.New("terminal framing error")
}

func TestAgentSocketTerminalReadError(t *testing.T) {
	for _, format := range []snesagent.Format{snesagent.FormatJSONL, snesagent.FormatProto} {
		t.Run(string(format), func(t *testing.T) {
			h, err := listenAgentSocket(filepath.Join(t.TempDir(), "agent.sock"), format)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			a, b := net.Pipe()
			defer b.Close()
			conn := &terminalReader{Conn: a}
			client := &agentSocketClient{conn: conn, send: make(chan snesagent.Observation, 1)}
			h.mu.Lock()
			h.clients[conn] = client
			h.mu.Unlock()
			done := make(chan struct{})
			go func() { h.read(conn); close(done) }()
			waitSocketTest(t, done)
			if n := conn.reads.Load(); n != 1 {
				t.Fatalf("terminal reader retried %d times", n)
			}
			h.Broadcast(snesagent.Observation{})
		})
	}
}
