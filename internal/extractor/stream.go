package extractor

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// StreamFinisher drains any unread bytes to EOF, closes the stream resources,
// and returns the raw and decompressed SHA-256 digests.
type StreamFinisher func() (rawSHA, decSHA string, err error)

// OpenHashedStream opens a text or gzip stream for line-by-line scanning while computing
// both raw and decompressed SHA-256 digests over the exact bytes read.
// The caller must call the returned StreamFinisher after scanning completes.
// The StreamFinisher consumes any remaining bytes to EOF before closing the stream,
// guaranteeing that the returned digests describe the entire stream that was parsed.
// Streaming buffer memory is strictly bounded (O(1)).
func OpenHashedStream(path string) (*bufio.Scanner, StreamFinisher, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open stream: %w", err)
	}

	rawHasher := sha256.New()

	if !strings.HasSuffix(path, ".gz") {
		tee := io.TeeReader(f, rawHasher)
		scanner := bufio.NewScanner(tee)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 10*1024*1024)

		finisher := func() (string, string, error) {
			if _, err := io.Copy(io.Discard, tee); err != nil {
				f.Close()
				return "", "", fmt.Errorf("drain stream %q: %w", path, err)
			}
			if err := f.Close(); err != nil {
				return "", "", fmt.Errorf("close stream %q: %w", path, err)
			}
			h := hex.EncodeToString(rawHasher.Sum(nil))
			return h, h, nil
		}
		return scanner, finisher, nil
	}

	// For .gz files, tee raw compressed stream into rawHasher while decompressing and teeing into decHasher.
	rawTee := io.TeeReader(f, rawHasher)
	gz, err := gzip.NewReader(rawTee)
	if err != nil {
		f.Close()
		return nil, nil, fmt.Errorf("create gzip reader for %q: %w", path, err)
	}

	decHasher := sha256.New()
	decTee := io.TeeReader(gz, decHasher)
	scanner := bufio.NewScanner(decTee)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	finisher := func() (string, string, error) {
		var drainErr error
		if _, err := io.Copy(io.Discard, decTee); err != nil && drainErr == nil {
			drainErr = fmt.Errorf("drain decompressed stream %q: %w", path, err)
		}
		if err := gz.Close(); err != nil && drainErr == nil {
			drainErr = fmt.Errorf("close gzip reader %q: %w", path, err)
		}
		if _, err := io.Copy(io.Discard, rawTee); err != nil && drainErr == nil {
			drainErr = fmt.Errorf("drain raw stream %q: %w", path, err)
		}
		if err := f.Close(); err != nil && drainErr == nil {
			drainErr = fmt.Errorf("close raw stream %q: %w", path, err)
		}
		if drainErr != nil {
			return "", "", drainErr
		}
		rawHex := hex.EncodeToString(rawHasher.Sum(nil))
		decHex := hex.EncodeToString(decHasher.Sum(nil))
		return rawHex, decHex, nil
	}

	return scanner, finisher, nil
}

// FileDigests computes both raw and decompressed SHA-256 hashes of a file in a single streaming pass.
// For non-.gz files, rawHex and decHex are identical.
// Memory usage is strictly bounded (O(1)).
func FileDigests(path string) (rawHex string, decHex string, err error) {
	_, finisher, err := OpenHashedStream(path)
	if err != nil {
		return "", "", err
	}
	return finisher()
}

// ReceiptData captures the receipt schema fields used for verification.
type ReceiptData struct {
	Schema       int    `json:"schema"`
	Outcome      string `json:"outcome"`
	StreamSHA256 string `json:"stream_sha256"`
	EventCount   int    `json:"event_count,omitempty"`
}

// VerifyReceiptDigest reads a receipt file and asserts its stream_sha256 matches
// the expected digest according to explicit receipt schema semantics.
//
// In receipt schema 2:
//   - For uncompressed streams (.jsonl): stream_sha256 must match the raw file hash.
//   - For compressed streams (.jsonl.gz): stream_sha256 defines the uncompressed stream hash
//     (decompressed), or the raw container hash when emitted by raw file sinks.
func VerifyReceiptDigest(receiptPath, streamPath, rawHex, decHex string) (*ReceiptData, error) {
	if receiptPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return nil, fmt.Errorf("read receipt file: %w", err)
	}
	var r ReceiptData
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal receipt file: %w", err)
	}
	if r.StreamSHA256 == "" {
		return &r, nil
	}

	isCompressed := strings.HasSuffix(streamPath, ".gz")
	if !isCompressed {
		if r.StreamSHA256 != rawHex {
			return nil, fmt.Errorf("receipt %q stream_sha256 mismatch for uncompressed stream: got %s, want raw %s",
				receiptPath, r.StreamSHA256, rawHex)
		}
		return &r, nil
	}

	if r.StreamSHA256 != decHex && r.StreamSHA256 != rawHex {
		return nil, fmt.Errorf("receipt %q stream_sha256 mismatch for compressed stream: got %s, expected uncompressed %s or container %s",
			receiptPath, r.StreamSHA256, decHex, rawHex)
	}
	return &r, nil
}

// OpenStream opens a text or gzip stream for line-by-line reading with a bounded buffer.
func OpenStream(path string) (*bufio.Scanner, io.Closer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open stream: %w", err)
	}

	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("read gzip stream: %w", err)
		}
		scanner := bufio.NewScanner(gz)
		buf := make([]byte, 1024*1024)
		scanner.Buffer(buf, 10*1024*1024)
		return scanner, &multiCloser{readers: []io.Closer{gz, f}}, nil
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	return scanner, f, nil
}

type multiCloser struct {
	readers []io.Closer
}

func (m *multiCloser) Close() error {
	var firstErr error
	for _, r := range m.readers {
		if err := r.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
