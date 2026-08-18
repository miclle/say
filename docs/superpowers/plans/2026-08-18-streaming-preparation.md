# Streaming TTS Preparation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Start document playback as soon as the first bounded TTS unit is ready while synthesizing later units in order in the background.

**Architecture:** Replace the fixed precomputed playlist with an ordered `player.TrackResult` stream. The player starts from the first prepared track, accumulates durations as later tracks arrive, waits only when playback or a forward seek reaches unprepared audio, and preserves the desired paused/playing state across buffering. The CLI owns one cancellable synthesis producer, waits for it before deleting temporary files, and keeps all terminal rendering on the player goroutine.

**Tech Stack:** Go 1.26 standard library, macOS `/usr/bin/say`, AVFoundation through the existing cgo bridge, ANSI terminal output, `go test`, `go vet`.

## Global Constraints

- `/usr/bin/say` remains sequential: only one bounded TTS unit is synthesized at a time.
- Playback starts after track `1/N` is ready; it must not wait for track `2/N`.
- Every unit sent to TTS remains non-empty and no longer than `--max-chars` Unicode code points.
- Background synthesis errors and cancellation propagate to the command, terminate active work, and preserve exit codes.
- Temporary audio is removed only after the synthesis goroutine exits.
- Natural playback waits at an unavailable next track and resumes automatically when it arrives.
- A forward five-second seek beyond prepared audio waits for enough ordered tracks; a seek made while paused remains paused.
- Backward seek across already prepared tracks remains immediate.
- Text remains visible before its matching audio starts.
- stdin raw mode is restored on success, failure, and cancellation.

---

### Task 1: Ordered streaming player

**Files:**
- Modify: `internal/player/player.go`
- Modify: `internal/player/player_test.go`

**Interfaces:**
- Produce: `player.TrackResult{Track player.Track, Err error}`
- Replace: `player.Play(ctx, tracks, transport, commands, view)` with `player.Play(ctx, total, results, transport, commands, view)`
- Extend: `player.View` with `Prepared(prepared, total int) error` and `Buffering(index, total int) error`
- Extend: `Seeked(..., duration time.Duration, complete bool)` so the TUI can distinguish known total duration from a prepared prefix

- [x] **Step 1: Write a failing first-track-start test**

  Send only track 1 on an open result channel and assert `play:one.aiff` occurs before track 2 is sent. This test must fail against the fixed-slice player because it cannot be called without all tracks.

- [x] **Step 2: Run the focused test and verify RED**

  Run: `go test ./internal/player -run TestPlayStartsWhenFirstStreamedTrackIsReady -count=1`

  Expected: FAIL because `Play` does not accept an ordered result stream.

- [x] **Step 3: Implement minimal ordered stream consumption**

  Validate `total`, channel, transport, and view; wait for the first `TrackResult`; call `Prepared(1, total)`, `Start(total)`, `Speaking`, and `Play`; append later tracks in arrival order without interrupting current playback.

- [x] **Step 4: Run the focused test and verify GREEN**

  Run: `go test ./internal/player -run TestPlayStartsWhenFirstStreamedTrackIsReady -count=1`

  Expected: PASS.

- [x] **Step 5: Write failing buffering and seek tests**

  Cover natural completion before track 2 arrives, a playing forward seek from global second 1 to second 6 when only a four-second first track is ready, the same seek while paused, backward seek after track 2 is prepared, producer error propagation, early channel close, and cancellation while waiting for the first track.

- [x] **Step 6: Run the expanded player tests and verify RED**

  Run: `go test ./internal/player -count=1`

  Expected: FAIL on missing buffering/pending-seek behavior.

- [x] **Step 7: Complete the streaming state machine**

  Maintain prepared tracks and prefix durations, desired playback state, current completion state, natural-next buffering, and one pending absolute seek target. Resolve the pending seek whenever a new ordered track makes the target addressable; on stream close, clamp only to the real document end and reject a prepared count different from `total`.

- [x] **Step 8: Run player tests and verify GREEN**

  Run: `go test ./internal/player -count=1`

  Expected: PASS.

### Task 2: Cancellable CLI synthesis producer

**Files:**
- Modify: `internal/audio/transport_darwin.go`
- Modify: `internal/audio/transport_other.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Produce: package-level `audio.Duration(path string) (time.Duration, error)` for probing a completed file without borrowing the playback transport
- Add dependency: `durationReader func(path string) (time.Duration, error)`
- Produce: `prepareTracks(ctx, chunks, tempDir, synthesizer, durationReader) (<-chan player.TrackResult, <-chan struct{})`

- [x] **Step 1: Write a failing CLI startup regression test**

  Use a synthesizer whose second call blocks. Run the CLI in a goroutine, allow track 1 to finish, keep track 2 blocked, and assert the fake transport receives `Play` before releasing track 2. The observable behavior catches any return to eager full-document preparation.

- [x] **Step 2: Run the regression test and verify RED**

  Run: `go test ./internal/cli -run TestRunStartsPlaybackBeforeSecondTrackFinishesSynthesis -count=1`

  Expected: FAIL because the current loop waits for every synthesis call.

- [x] **Step 3: Add independent duration probing**

  Extract the existing create/read/destroy logic into `audio.Duration`; keep `(*Transport).Duration` as a compatibility wrapper if tests still consume it; add the non-Darwin/cgo-disabled error implementation.

- [x] **Step 4: Implement the ordered producer**

  Start one goroutine that synthesizes each bounded chunk to its deterministic temporary path, probes its duration, sends one ordered `TrackResult`, and closes both result/done channels. Send exactly one contextual error result before stopping.

- [x] **Step 5: Integrate producer lifecycle**

  Create raw input and playback transport before calling the streaming player, cancel the producer when playback returns, wait for `done`, then restore terminal mode and allow deferred transport/temp cleanup. Do not render from the producer goroutine.

- [x] **Step 6: Run CLI tests and verify GREEN**

  Run: `go test ./internal/audio ./internal/cli -count=1`

  Expected: PASS.

- [x] **Step 7: Add failure and cancellation coverage**

  Assert a second-track synthesis failure reaches stderr after track 1 can start, active cancellation stops the blocked synthesizer, the producer exits before temp paths disappear, and raw terminal restoration still runs.

- [x] **Step 8: Run focused packages**

  Run: `go test -race ./internal/audio ./internal/player ./internal/cli -count=1`

  Expected: PASS with no race report.

### Task 3: Preparation/buffering TUI and shipped documentation

**Files:**
- Modify: `internal/terminal/view.go`
- Modify: `internal/terminal/view_test.go`
- Modify: `README.md`

**Interfaces:**
- Render initial state: `… preparing audio · 0/N ready`
- Render first-ready state before controls: `… ready to play · 1/N prepared`
- Render underrun state: `… buffering speech unit K/N`
- Render incomplete seek totals with a `+` suffix

- [x] **Step 1: Write failing terminal output tests**

  Assert literal preparation, first-ready, buffering, known-total seek, and incomplete-total seek output. Retain terminal-control escaping assertions.

- [x] **Step 2: Run terminal tests and verify RED**

  Run: `go test ./internal/terminal -count=1`

  Expected: FAIL because the new view callbacks and incomplete-duration rendering do not exist.

- [x] **Step 3: Implement minimal TUI callbacks**

  Keep header output idempotent, avoid background goroutine writes, and emit preparation updates only before playback starts so long documents do not print one line per background track.

- [x] **Step 4: Run terminal tests and verify GREEN**

  Run: `go test ./internal/terminal -count=1`

  Expected: PASS.

- [x] **Step 5: Update README**

  Replace the all-tracks-up-front description with first-track startup, sequential background prefetch, buffering semantics, incomplete seek-duration notation, cancellation cleanup, and unchanged TTS request limits.

- [x] **Step 6: Run complete automated verification**

  Run: `gofmt -w cmd internal && go test -race -count=1 ./... && go vet ./... && go build ./... && CGO_ENABLED=0 go test -count=1 ./... && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./... && git diff --check`

  Expected: all commands exit 0.

- [x] **Step 7: Run the original long-document regression**

  Build a temporary binary and run it in a real PTY against `/Users/miclle/github/miclle/aihub/docs/PRICE_MANAGEMENT.md`. Confirm `[1/79]` and audible playback appear after only the first AIFF is generated, later AIFF files continue growing during playback, Space/Left/Right still work, Ctrl-C returns 130, and the same PTY reports terminal mode restored.

- [x] **Step 8: Review the final diff**

  Verify the root-cause path no longer contains a full-document synthesis loop before `player.Play`, all background writes are ordered through channels, no generated audio/binaries are tracked, and the worktree contains only the intended fix.
