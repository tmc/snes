package framecap

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/snes"
	"github.com/tmc/snes/internal/trace"
)

// Options configure a Writer.
type Options struct {
	// Dir is the capture directory. It is created if needed.
	Dir string
	// Run and Trace identify the run; see Header.
	Run   *trace.RunInfo
	Trace string
	// TraceFrame, if non-nil, reports the trace's frame number, which
	// is recorded with each frame.
	TraceFrame func() int
	// Selection chooses which frames have stored pixels.
	// Every <= 0 means every frame.
	Selection Selection
	// Limits bound stored content.
	Limits Limits
	// PNG, if non-nil, selects stored frames to export as PNG.
	PNG func(number int) bool
	// Seq, if non-nil, maps frame boundaries to trace sequence numbers.
	Seq *SeqClock
}

// A Writer records frames into a capture directory. Its Keep and Frame
// methods have the signatures snes.System.CaptureFrames expects.
//
// After the first error, the Writer stores no further pixels but keeps
// recording the timeline while the manifest remains writable; callers
// check Err at safe points.
type Writer struct {
	opts Options
	f    *os.File
	bw   *bufio.Writer
	sum  hash.Hash
	enc  *json.Encoder

	pending *Record
	index   int
	stored  int
	bytes   int64
	blobs   map[string]int // content ID to first Index
	trunc   string
	err     error // first error
	merr    error // manifest write error
}

// Create creates the capture directory and manifest and writes the
// header. It removes any receipt left by an earlier run.
func Create(opts Options) (*Writer, error) {
	if opts.Selection.Every <= 0 {
		opts.Selection.Every = 1
	}
	if err := os.MkdirAll(filepath.Join(opts.Dir, blobDir), 0o755); err != nil {
		return nil, fmt.Errorf("create frame capture: %w", err)
	}
	if err := os.Remove(filepath.Join(opts.Dir, ReceiptName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("create frame capture: %w", err)
	}
	f, err := os.Create(filepath.Join(opts.Dir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("create frame capture: %w", err)
	}
	w := &Writer{opts: opts, f: f, sum: sha256.New(), blobs: make(map[string]int)}
	w.bw = bufio.NewWriter(io.MultiWriter(f, w.sum))
	w.enc = json.NewEncoder(w.bw)
	w.emit(Header{
		Schema:    SchemaVersion,
		Kind:      "frame_run",
		Run:       opts.Run,
		Trace:     opts.Trace,
		Storage:   StorageGzipNative,
		Selection: opts.Selection,
		Limits:    opts.Limits,
	})
	return w, w.err
}

// Err returns the first error.
func (w *Writer) Err() error { return w.err }

// Truncated returns the reason content stopped being stored, or "".
func (w *Writer) Truncated() string { return w.trunc }

func (w *Writer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func (w *Writer) emit(v any) {
	if w.merr != nil {
		return
	}
	if err := w.enc.Encode(v); err != nil {
		w.merr = fmt.Errorf("write frame manifest: %w", err)
		w.fail(w.merr)
	}
}

// Keep records the boundary of a completed frame, completes the
// previous record, and reports whether the frame's pixels should be
// stored.
func (w *Writer) Keep(b snes.FrameBoundary) bool {
	if w.pending != nil {
		end := b.Start
		w.pending.End = &end
		w.flush(false)
	}
	r := &Record{Kind: "frame", Index: w.index, Number: b.Number, Start: b.Start, VBlank: b.VBlank}
	if w.opts.TraceFrame != nil {
		tf := w.opts.TraceFrame()
		r.TraceFrame = &tf
	}
	w.index++
	w.pending = r
	w.resolve(r, false)

	sel := w.opts.Selection
	switch {
	case b.Number < sel.From || (b.Number-sel.From)%sel.Every != 0:
		r.Skip = SkipSelection
	case w.err != nil:
		r.Skip = SkipError
	case w.trunc != "":
		r.Skip = SkipLimit
	case w.opts.Limits.Frames > 0 && w.stored >= w.opts.Limits.Frames:
		w.trunc = "frame_limit"
		r.Skip = SkipLimit
	}
	return r.Skip == ""
}

// resolve sets r's seq bounds that are known. If final, no further
// observations will be made, so a boundary no observation has reached
// is bounded by the next seq.
func (w *Writer) resolve(r *Record, final bool) {
	c := w.opts.Seq
	if c == nil {
		return
	}
	seq := func(p **uint64, cycle uint64) {
		if *p != nil {
			return
		}
		if s, ok := c.First(cycle); ok {
			*p = &s
		} else if final && !c.passed(cycle) {
			s := c.Next()
			*p = &s
		}
	}
	seq(&r.SeqStart, r.Start)
	seq(&r.SeqVBlank, r.VBlank)
	if r.End != nil {
		seq(&r.SeqEnd, *r.End)
	}
}

func (w *Writer) flush(final bool) {
	w.resolve(w.pending, final)
	w.emit(w.pending)
	w.pending = nil
}

// Frame stores the pixels of f, which must be the frame of the last
// boundary passed to Keep.
func (w *Writer) Frame(f *snes.Frame) {
	r := w.pending
	if r == nil || r.Number != f.Number || w.err != nil {
		w.fail(fmt.Errorf("frame capture: frame %d out of order", f.Number))
		return
	}
	r.Field, r.Interlace, r.Overscan = f.Field, f.Interlace, f.Overscan
	r.Width, r.Height, r.Format = f.Width, f.Height, f.Format
	r.HiresLines = spans(f.HiresLines)
	r.PseudoHires = f.PseudoHires
	r.FirstLine = f.FirstLine

	raw := nativeBytes(f.Pixels)
	id := ContentID(f.Format, f.Width, f.Height, raw)
	var add int64
	var blob []byte
	first, dup := w.blobs[id]
	if !dup {
		blob = compress(raw)
		add = int64(len(blob))
	}
	var png []byte
	if w.opts.PNG != nil && w.opts.PNG(f.Number) {
		var buf bytes.Buffer
		if err := EncodePNG(&buf, f.Width, f.Height, f.Pixels); err != nil {
			w.fail(err)
			return
		}
		png = buf.Bytes()
		add += int64(len(png))
	}
	if lim := w.opts.Limits.Bytes; lim > 0 && w.bytes+add > lim {
		w.trunc = "byte_limit"
		r.Skip = SkipLimit
		return
	}

	blobPath := filepath.ToSlash(filepath.Join(blobDir, id+".gz"))
	if !dup {
		if err := writeFile(filepath.Join(w.opts.Dir, blobPath), blob); err != nil {
			w.fail(err)
			r.Skip = SkipError
			return
		}
		w.blobs[id] = r.Index
	}
	if png != nil {
		r.PNG = filepath.ToSlash(filepath.Join(pngDir, fmt.Sprintf("%06d.png", f.Number)))
		if err := os.MkdirAll(filepath.Join(w.opts.Dir, pngDir), 0o755); err != nil {
			w.fail(fmt.Errorf("frame capture: %w", err))
		} else if err := writeFile(filepath.Join(w.opts.Dir, r.PNG), png); err != nil {
			w.fail(err)
		}
		if w.err != nil {
			r.PNG = ""
		} else {
			s := sha256.Sum256(png)
			r.PNGSHA256 = hex.EncodeToString(s[:])
		}
	}
	r.Stored = true
	r.ContentID = id
	r.Blob = blobPath
	if dup {
		r.DupOf = &first
	}
	w.stored++
	w.bytes += add
}

// Close writes the last record, closes the manifest and writes the
// receipt. The last record has no End: the run stopped before the next
// frame began. If outcome is complete and content was truncated, the
// receipt reports limit.
func (w *Writer) Close(outcome trace.Outcome) (Receipt, error) {
	if w.pending != nil {
		w.flush(true)
	}
	if err := w.bw.Flush(); err != nil {
		w.fail(fmt.Errorf("write frame manifest: %w", err))
	}
	if err := w.f.Close(); err != nil {
		w.fail(fmt.Errorf("write frame manifest: %w", err))
	}
	r := Receipt{
		Schema:           SchemaVersion,
		Outcome:          outcome,
		Frames:           w.index,
		Stored:           w.stored,
		Unique:           len(w.blobs),
		Bytes:            w.bytes,
		ManifestSHA256:   hex.EncodeToString(w.sum.Sum(nil)),
		TruncationReason: w.trunc,
	}
	if w.trunc != "" && outcome == trace.OutcomeComplete {
		r.Outcome = trace.OutcomeLimit
	}
	if w.err != nil {
		r.Outcome = trace.OutcomeSinkError
		r.Error = w.err.Error()
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return r, fmt.Errorf("encode frame receipt: %w", err)
	}
	if err := writeFile(filepath.Join(w.opts.Dir, ReceiptName), append(data, '\n')); err != nil {
		return r, err
	}
	return r, w.err
}

// ContentID returns the content identity of a frame: the hex SHA-256 of
// "<format> <width>x<height>\n" followed by the native pixel bytes.
func ContentID(format string, width, height int, raw []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s %dx%d\n", format, width, height)
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

func nativeBytes(px []uint16) []byte {
	b := make([]byte, 2*len(px))
	for i, v := range px {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	return b
}

func compress(raw []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	zw.Write(raw)
	zw.Close()
	return buf.Bytes()
}

// writeFile writes data to path so that path appears only when complete.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("frame capture: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("frame capture: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("frame capture: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("frame capture: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("frame capture: %w", err)
	}
	return nil
}
