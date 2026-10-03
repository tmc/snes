# Machine tools and game consumers

This repository implements SNES machine semantics, emulation, tracing,
disassembly, code generation, evidence admission and inspection. It contains
no built-in commercial-ROM digest catalogs, title overrides, game routines,
replay corpus authority or default behavioral profiles.

A consumer supplies:

- Original ROM bytes with an explicit digest.
- Routine, dispatch, editor and memory-binding profiles.
- Captures, history, snapshots and reviewed evidence roots.
- Game-specific labels, scenarios, generated C and integration results.

The public interface is the command line and versioned configuration schema.
Consumers do not import Go `internal` packages. `snesdasm workflow`, `snesextract`,
`snesdasm queue`, `snesbranch`, `snesedit` and the inspection tools accept
explicit operator inputs. Generic cartridge-header detection stays in the
engine; header exceptions and hardware-selection overrides are supplied by the
consumer through explicit options.

A default evidence verifier has no admission authority. Qualification requires
an explicitly pinned operator policy; producer cases cannot define their own
trust roots. Emission, compilation, observed execution and sampled qualification
are separate outcomes. Historical receipts keep the identity of the code and
artifacts that produced them; a file move does not qualify new source.

Core regressions use authored synthetic instruction/device fixtures. Real game
integration belongs to the consumer and must retain genuine refusal controls.
Source and license attribution for generic hardware tables is retained in
`NOTICE`.
