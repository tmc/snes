package framecap

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// A Capture is a capture directory opened for reading.
type Capture struct {
	Dir     string
	Header  Header
	Records []Record
	// Receipt is nil if the capture is incomplete.
	Receipt *Receipt
}

// Open reads the manifest and receipt in dir.
func Open(dir string) (*Capture, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("open frame capture: %w", err)
	}
	c := &Capture{Dir: dir}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		if n == 1 {
			if err := json.Unmarshal(sc.Bytes(), &c.Header); err != nil {
				return nil, fmt.Errorf("open frame capture: header: %w", err)
			}
			if c.Header.Kind != "frame_run" || c.Header.Schema != SchemaVersion {
				return nil, fmt.Errorf("open frame capture: unsupported header kind %q schema %d", c.Header.Kind, c.Header.Schema)
			}
			continue
		}
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("open frame capture: line %d: %w", n, err)
		}
		c.Records = append(c.Records, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("open frame capture: %w", err)
	}
	if c.Header.Kind == "" {
		return nil, fmt.Errorf("open frame capture: missing header")
	}
	rdata, err := os.ReadFile(filepath.Join(dir, ReceiptName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("open frame capture: %w", err)
	default:
		c.Receipt = new(Receipt)
		if err := json.Unmarshal(rdata, c.Receipt); err != nil {
			return nil, fmt.Errorf("open frame capture: receipt: %w", err)
		}
	}
	return c, nil
}

// At returns the record of the frame whose interval contains cycle, or
// nil. The last frame's interval is open-ended.
func (c *Capture) At(cycle uint64) *Record {
	for i := range c.Records {
		r := &c.Records[i]
		if cycle >= r.Start && (r.End == nil || cycle < *r.End) {
			return r
		}
	}
	return nil
}

// Pixels returns the native pixels of a stored frame after checking
// their content ID.
func (c *Capture) Pixels(r *Record) ([]uint16, error) {
	if !r.Stored {
		return nil, fmt.Errorf("frame %d: pixels not stored", r.Number)
	}
	f, err := os.Open(filepath.Join(c.Dir, filepath.FromSlash(r.Blob)))
	if err != nil {
		return nil, fmt.Errorf("frame %d: %w", r.Number, err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("frame %d: %w", r.Number, err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("frame %d: %w", r.Number, err)
	}
	if len(raw) != 2*r.Width*r.Height {
		return nil, fmt.Errorf("frame %d: %d bytes for %dx%d", r.Number, len(raw), r.Width, r.Height)
	}
	if id := ContentID(r.Format, r.Width, r.Height, raw); id != r.ContentID {
		return nil, fmt.Errorf("frame %d: content id %s, want %s", r.Number, id, r.ContentID)
	}
	px := make([]uint16, r.Width*r.Height)
	for i := range px {
		px[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return px, nil
}
