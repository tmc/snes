package snesagent

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestProtoReaderLimits(t *testing.T) {
	for _, size := range []uint64{1<<20 + 1, ^uint64(0)} {
		header := binary.AppendUvarint(nil, size)
		_, err := NewProtoReader(bytes.NewReader(header)).Read()
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("size %d: %v", size, err)
		}
	}
	msg := MarshalClientMessageProto(ClientMessage{})
	data := binary.AppendUvarint(nil, uint64(len(msg)))
	data = append(data, msg...)
	if _, err := NewProtoReader(bytes.NewReader(data)).Read(); err != nil {
		t.Fatal(err)
	}
}
