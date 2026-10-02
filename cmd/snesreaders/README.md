# snesreaders

Inspect reads of a selected physical WRAM byte version in a complete, pinned
runtime observation window:

```
go run ./cmd/snesreaders -window window.json \
  -window-sha256 FILE_SHA256 -writer 42
```

The writer is the raw event ID, including zero. Low-WRAM CPU mirrors and physical
WRAM-port events join to the same physical address. Reads are retained until the
next write, including a same-value write, or the end of the recorded window.
Unknown actors remain unknown; inconsistent byte values refuse the report.

This reports observed version consumption, not instruction arithmetic,
source-to-pixel causality, or captured recovery proof. The producer's declared
complete writer coverage is a prerequisite. Unrecorded future reads are unknown.
