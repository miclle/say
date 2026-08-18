# Microsoft Edge TTS Integration Implementation Plan

> **For Codex:** Execute this plan task by task with test-driven development. Keep `system` as the default provider and describe Edge TTS as experimental.

**Goal:** Add an optional Microsoft Edge Read Aloud TTS provider to `say`, with MP3 playback, provider-specific CLI options, bounded network calls, and unchanged default system-TTS behavior.

**Architecture:** Extend the existing synthesizer boundary so each provider declares its output extension. Add a provider factory that validates provider-specific options. Implement Edge Read Aloud as a context-aware WebSocket client that builds the same signed requests and SSML used by Slideo, returns a complete MP3 track, and fits the existing background preparation/player pipeline.

**Tech Stack:** Go, `github.com/coder/websocket`, macOS AVFoundation, standard `flag`, `httptest`, Go tests.

---

### Task 1: Make the synthesizer provider-neutral

**Files:**
- Modify: `internal/tts/speaker.go`
- Modify: `internal/tts/system_darwin.go`
- Modify: `internal/tts/system_other.go`
- Modify: `internal/tts/system_darwin_test.go`
- Create: `internal/tts/factory.go`
- Create: `internal/tts/factory_test.go`

**Steps:**
1. Add failing tests for the system provider's `.aiff` extension and default factory selection.
2. Run `go test ./internal/tts` and confirm the new assertions fail.
3. Add `Extension() string` to `Synthesizer`, implement it for the system provider, and introduce `Provider`, `Options`, and `New(Options)`.
4. Run `go test ./internal/tts` and confirm it passes.

### Task 2: Implement Microsoft Edge Read Aloud synthesis

**Files:**
- Create: `internal/tts/edge.go`
- Create: `internal/tts/edge_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Steps:**
1. Add focused failing tests for Edge defaults, `.mp3` output, speed validation, XML escaping, signed URL generation, binary audio parsing, cancellation, timeout, and file-write errors.
2. Run each focused test and confirm the expected red state before implementation.
3. Add `github.com/coder/websocket` and implement the Edge protocol: GEC token, request IDs, `speech.config`, SSML, audio-frame collection, and `turn.end` completion.
4. Wrap every request in a 45-second timeout derived from the caller context. Never write a partial output file on protocol failure.
5. Run `go test ./internal/tts` and confirm all provider tests pass.

### Task 3: Expose provider-specific CLI controls

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Steps:**
1. Add failing tests for `--provider edge`, conditional voice defaults, `--speed`, MP3 track paths, unknown providers, and incompatible explicitly supplied flags.
2. Run focused CLI tests and confirm they fail for the intended missing behavior.
3. Replace the system-specific factory arguments with `tts.Options`, add `--provider` and `--speed`, and derive track suffixes from `Synthesizer.Extension()`.
4. Reject explicit `--rate` with Edge and explicit `--speed` with system so options are never silently ignored.
5. Run `go test ./internal/cli` and confirm it passes.

### Task 4: Document and verify the shipped behavior

**Files:**
- Modify: `README.md`

**Steps:**
1. Document the unchanged system default and an Edge example using `--provider edge`.
2. Clearly label Edge Read Aloud as experimental and network-dependent; document provider-specific voice/rate/speed behavior.
3. Run `gofmt` on changed Go files.
4. Run `go test ./...`, `go vet ./...`, and `go build ./...`.
5. Run a live Edge synthesis smoke test with neutral text, then run the TUI in a PTY and verify preparation, MP3 playback, text advancement, pause/resume, and seek behavior.
6. Review staged, unstaged, and untracked changes for correctness and scope; leave the completed work uncommitted unless the user asks to publish it.
