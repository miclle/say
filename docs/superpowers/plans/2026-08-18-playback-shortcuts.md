# Playback Shortcuts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add responsive terminal controls so Space toggles play/pause, Left Arrow rewinds five seconds, and Right Arrow advances five seconds while document text remains synchronized with the spoken paragraph.

**Architecture:** Keep paragraph-first, rune-bounded text chunks as the unit sent to macOS TTS. Before playback, synthesize each bounded chunk to a temporary AIFF file with `/usr/bin/say`; then play the files through a small AVFoundation-backed transport with a global timeline across tracks. A platform-neutral controller owns play/pause/seek state and consumes decoded key events, while the Darwin terminal adapter temporarily enables raw input and restores the original terminal state on every exit path. Redirected/non-interactive execution still plays automatically without requiring keyboard input.

**Tech Stack:** Go 1.26 standard library, cgo Objective-C bridge to macOS AVFoundation, macOS `/usr/bin/say`, ANSI terminal output, `go test`, `go vet`.

## Global Constraints

- Every `/usr/bin/say` synthesis call receives exactly one non-empty chunk already bounded by `--max-chars` Unicode code points.
- Space toggles playing/paused without changing position; Left and Right seek exactly five seconds on the document-wide audio timeline and clamp at the beginning/end.
- Seeking across a track boundary updates the visible paragraph before audio resumes.
- A seek while paused remains paused; a seek while playing resumes from the target.
- Terminal raw mode is enabled only for a real character-device stdin and is always restored.
- Ctrl-C remains signal-driven cancellation, temporary audio is removed, and active native playback is released.
- Document and engine strings continue to be escaped before terminal rendering.
- macOS remains the only runtime-supported TTS/playback platform; non-Darwin builds return a precise unsupported-platform error.

---

### Task 1: File synthesis contract

**Files:**
- Modify: `internal/tts/speaker.go`
- Modify: `internal/tts/system_darwin.go`
- Modify: `internal/tts/system_darwin_test.go`
- Modify: `internal/tts/system_other.go`

**Interfaces:**
- Produce: `tts.Synthesizer` with `Name() string` and `Synthesize(context.Context, string, string) error`
- Produce: macOS AIFF files by calling `/usr/bin/say -o <path>` with voice/rate arguments and speech text on stdin

- [x] **Step 1: Write failing synthesizer tests**

  Assert that output path, voice, and rate are separate arguments; UTF-8 text is provided literally on stdin; empty text/output paths are rejected; command diagnostics and context cancellation are preserved.

- [x] **Step 2: Run the focused test and verify the new contract fails**

  Run: `go test ./internal/tts`

  Expected: FAIL because `Synthesizer` and `Synthesize` do not exist.

- [x] **Step 3: Implement bounded file synthesis**

  Replace the blocking audible `Speak` operation with AIFF synthesis to an explicit caller-owned path. Use `exec.CommandContext`, pass `-o` without a shell, retain `-v`/`-r`, and wrap stderr diagnostics.

- [x] **Step 4: Run the focused tests**

  Run: `go test ./internal/tts`

  Expected: PASS.

### Task 2: Native audio transport and timeline controller

**Files:**
- Create: `internal/audio/transport.go`
- Create: `internal/audio/transport_darwin.go`
- Create: `internal/audio/transport_other.go`
- Create: `internal/audio/transport_darwin_test.go`
- Replace: `internal/player/player.go`
- Replace: `internal/player/player_test.go`

**Interfaces:**
- Produce: `audio.Transport` operations to inspect duration, load, play, pause, seek, read position, and close one active file
- Produce: `player.Play(context.Context, []player.Track, audio.Transport, <-chan player.Command, player.View) error`
- Consume: toggle, rewind-five-seconds, and forward-five-seconds commands

- [x] **Step 1: Write failing controller tests with a fake transport**

  Cover automatic sequential playback, pause/resume, same-track ±5 second seeks, cross-track seeks, clamping at zero/end, paused seek state, render-before-play ordering, cancellation, and transport/render failures.

- [x] **Step 2: Run the player tests and verify they fail**

  Run: `go test ./internal/player`

  Expected: FAIL because the controllable transport contract does not exist.

- [x] **Step 3: Implement the platform-neutral playback state machine**

  Maintain prefix durations for a document-wide timeline, poll native playback completion at a bounded interval, switch tracks at boundaries, preserve the paused/playing state across seeks, and close the active transport on every return path.

- [x] **Step 4: Implement and probe the Darwin AVFoundation bridge**

  Wrap `AVAudioPlayer` behind cgo with one retained player at a time. Validate file creation, duration, current position, `play`, `pause`, `setCurrentTime`, and release behavior using a short AIFF fixture synthesized during a Darwin-only test. Provide an unsupported-platform implementation for other builds.

- [x] **Step 5: Run focused tests**

  Run: `go test ./internal/audio ./internal/player`

  Expected: PASS.

### Task 3: Raw keyboard input and terminal status

**Files:**
- Create: `internal/terminal/keys.go`
- Create: `internal/terminal/keys_test.go`
- Create: `internal/terminal/raw_darwin.go`
- Create: `internal/terminal/raw_other.go`
- Modify: `internal/terminal/view.go`
- Modify: `internal/terminal/view_test.go`

**Interfaces:**
- Produce: decoding of ` `, `ESC [ D`, and `ESC [ C` into player commands
- Produce: a restorable raw-input session for character-device stdin
- Extend: playback view with preparation, paused/resumed, seek, and active-track states

- [x] **Step 1: Write failing key-decoder and rendering tests**

  Feed fragmented escape sequences and unrelated bytes through the decoder; assert exact command order and clean EOF behavior. Assert the controls help line and safe status output.

- [x] **Step 2: Run terminal tests and verify they fail**

  Run: `go test ./internal/terminal`

  Expected: FAIL because keyboard decoding and controllable status rendering do not exist.

- [x] **Step 3: Implement key decoding and terminal mode lifecycle**

  Decode keys independently from terminal mode. On Darwin, save termios, disable canonical input and echo while retaining signal generation, set single-byte reads, and return an idempotent restore function.

- [x] **Step 4: Extend the append-only TUI**

  Show `Space 播放/暂停 · ← 回退 5s · → 快进 5s`, print each newly active paragraph before playback, and append concise pause/seek status without emitting untrusted control characters.

- [x] **Step 5: Run focused tests**

  Run: `go test ./internal/terminal`

  Expected: PASS.

### Task 4: CLI integration, compatibility, and documentation

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`
- Modify: `README.md`

**Interfaces:**
- Consume: `tts.Synthesizer`, `audio.Transport`, terminal key stream, and the controllable player
- Preserve: existing flags, exit codes, paragraph-first chunking, and non-interactive automatic playback

- [x] **Step 1: Write failing CLI integration tests**

  Inject fake synthesis and transport factories. Assert one synthesis call/file per bounded chunk, keyboard controls only for interactive input, temp cleanup on success/failure/cancellation, and previous usage/document error behavior.

- [x] **Step 2: Run CLI tests and verify they fail**

  Run: `go test ./internal/cli`

  Expected: FAIL because CLI still invokes the blocking speaker directly.

- [x] **Step 3: Integrate preparation and playback**

  Create a private temporary directory, synthesize ordered AIFF tracks, collect durations through the transport, enter raw mode only when stdin/stdout are character devices, start the key decoder, and defer restoration/cleanup before controller playback.

- [x] **Step 4: Update README with shipped controls and behavior**

  Document preparation, the three shortcuts, five-second cross-paragraph seeking, raw-mode/redirect behavior, TTS call limits, Ctrl-C, and macOS/CGO requirements.

- [x] **Step 5: Format and run complete verification**

  Run: `gofmt -w cmd internal && go test -race ./... && go vet ./... && go build ./...`

  Expected: every command exits 0 with no test failures, race reports, vet diagnostics, or build errors.

- [x] **Step 6: Run a real-system smoke test**

  Build a temporary binary, synthesize a short temporary document, verify Space pauses/resumes and arrows seek in a real PTY, then confirm terminal state restoration with `stty -g` before/after. Avoid committing generated audio or binaries.

- [x] **Step 7: Review, commit, push, and verify remote parity**

  Review the complete diff for the three requested shortcuts and global constraints, stage only intended files, create an intentional commit on `main`, push `origin/main`, and verify local HEAD equals the remote SHA.
