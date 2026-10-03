// Package entity implements memory object and observed state machine recovery
// for SNES reverse engineering.
//
// Traditional disassemblers and decompilers often attempt to model entity
// memory as structures or objects referenced by pointers. In 65816 SNES assembly,
// entity systems are almost universally organized as Structure-of-Arrays (SoA)
// parallel tables (for example, tables in WRAM indexed by X or Y register:
// LDA $0D80,X; STA $0E20,X).
//
// This package models entity schemas, update dispatch tables, observed finite
// state machines with witness receipts, frame phase boundaries (logic update,
// OAM shadow buffer commit, and V-Blank DMA), and replayable lifecycle
// verification.
//
// # Evidence Boundaries
//
// In-memory simulation, heuristic position updates, and test schemas provide
// architectural scaffolding; they do NOT constitute verified game evidence.
// True dynamic observation requires recorded CPU execution witnesses, verified
// WRAM/OAM DMA transfers, and authentic replay traces from original machine
// captures.
package entity

