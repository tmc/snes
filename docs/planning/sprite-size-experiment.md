# Sprite size data experiment

The supported action selects an observed OAM entry and edits its size bit. It is separate from compiled C rotation execution.

Config mode is `sprite_data`. `sprite_edit` supplies an operator-pinned capture path/SHA, sprite index, through-frame and optional Large value. No caller-supplied writer PC, source address or cycle is accepted. Large=null is the unchanged control; setting the original captured size is reset. Addend must be zero in this mode.

The consumer owns and hashes the compressed capture, bounds decoded size, verifies schema/completeness/writer coverage/ROM identity and reconstructs the latest high-OAM byte version with provenance.ExplainSprite. This relies on the explicit operator-reviewed capture pin; a digest alone does not make arbitrary producer data authoritative. No recovered routine range or qualification is inferred.

The narrow supported writer is bank0 STA absolute,Y into low WRAM, with captured raw source matching the canonical offset and DMA source. Runtime hooks verify the actual writer PC/opcode, write cycle/value/PPU frame and observed entry context (M8,DB0,Y0,A low byte). At instruction completion, the original memory value and WRAM mirror identity are checked. The isolated edited branch then substitutes only the selected size bit through the backing WRAM device, preserving other seven bits, CPU clocks, bus MDR and source files. This intervention is not a CPU bus write, C edit or ROM patch.

Any source write through a canonical alias or WRAM port before consumption refuses. The pinned DMA read, register write and physical high-OAM write must match their cycles/frame/address/channel/value in the current run. The unchanged branch independently follows the same checks. Missing/stale writer or consumption fails the experiment and publishes no partial result. Partial execution is discarded, not rolled back.

Result records captured explanation/link separately from current runtime context and the applied intervention. CapturedProofEligible and ReplacementExecuted are false. OriginalMatch means equality to the unchanged branch, not repeatability. Repeated edited outputs prove repeatability separately; reset compares original frames/state/bus clocks again.

This is a one-version, one-shot intervention. Later game writes can restore the packed byte. A frame difference establishes the rendered consequence of this controlled experiment, not exclusive ownership of individual pixels by an OAM object. Low-byte origins, rendered priority/occlusion and full sprite semantics remain qualified.
