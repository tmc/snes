# snesedit

Serve an explicitly selected editor target observation manifest on loopback.

```
go run ./cmd/snesedit -manifest /path/to/manifest.json
```

Optional inspection pages:

- `/progress`: supply `-progress-config` and `-progress-sha256`. The configuration pins recovery, coverage and optional recorded batch evidence. Counts retain separate units and scopes.
- `/statewrites`: supply `-state-writes /path/to/window.json` and `-state-writes-sha256 <file SHA-256>`. The input is a complete `snes-observation-window-v1` JSON window, at most 64 MiB. Its ROM identity must match the target.

The state-write page selects one physical WRAM byte or low-RAM mirror and a half-open host-relative frame interval. It preserves repeated byte writes and raw event order. A word access appears as separate byte records. Before values require consistent prior observed accesses. Initial values and dispatch ancestry remain unknown. DMA records do not inherit the incidental CPU PC as writer attribution.

The file SHA verifies the selected raw input bytes. The displayed window SHA identifies canonical JSON and can differ from the file SHA. Neither hash establishes producer honesty, game-variable meaning, pixel causality or recovered-C qualification. The page shows writer PCs as text; code-view navigation is unavailable in this editor.

Missing optional evidence remains unavailable. Loading an invalid inspection input refuses startup before constructing experiment output directories. The inspection routes are read-only; existing explicitly configured experiment routes retain their separate behavior.
