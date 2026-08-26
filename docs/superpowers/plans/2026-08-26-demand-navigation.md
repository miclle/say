# Demand-driven speech navigation implementation plan

> Implemented inline on `main`, as explicitly requested by the user. The follow-up review requested one buffering-view fix, then authorized commit and push.

**Goal:** Move text selection immediately, settle navigation after 200 ms without another arrow, and prepare only the final target plus three following sentences.

**Architecture:** Replace the append-only chapter audio stream with indexed sentence results. A cancellable single-worker preparation scheduler caches completed audio and prioritizes the newest requested target. The player owns selection, debounce, playback intent, and presentation; source text defines navigation independently of audio availability.

**Tech stack:** Existing Go, context/channels/timers, macOS AVFoundation and existing TTS providers. No new dependencies.

## Constraints

- Work directly on main and preserve unrelated changes.
- Keep the fixed chapter list and real-audio sentence highlighting.
- Navigation preserves pause intent; Space remains play/pause.
- During selection, suspend preparation and native audio loading; render the selection first.
- Cache successful audio for this run; discard canceled partial files and ignore obsolete completion/errors.
- Normal playback starts at the first ready sentence and advances without navigation debounce.
- Prefetch failure must not abort the currently playable sentence; report it only when demanded.
- Cancel and join the worker before removing temporary audio files.

## Task 1: Indexed preparation

Files: `internal/player/preparation.go`, `internal/player/preparation_test.go`, `internal/cli/run.go`.

Interface: `Target{Chapter, Sentence int}`, `AudioResult{Target Target, Audio SentenceTrack, Err error}`, `AudioSource` with `Request(Target)`, `Suspend()`, `Results() <-chan AudioResult`. `NewPreparation(context.Context, []string, PrepareSentence)` creates a source; `Close()` cancels and joins it. `PrepareSentence` takes context, target and sentence text and returns audio metadata.

- [x] Add a blocking synthetic provider test. Request chapter 0, suspend its blocked prefetch, then request chapter 3; assert chapter 3 is prepared before skipped chapters and canceled work does not emit errors.
- [x] Run `go test ./internal/player -run Preparation -count=1` and establish the failing baseline.
- [x] Implement the single-worker scheduler with a replaceable request mailbox, per-job cancellation, successful-result cache, and three-sentence lookahead.
- [x] Verify cached targets are not synthesized again, prefetch is bounded, cancellation joins the worker, and prefetch errors are deferred until demand.

## Task 2: Selection and playback

Files: `internal/player/player.go`, `internal/player/navigation.go`, player tests, `internal/terminal/view.go`, terminal tests.

- [x] Add regression tests asserting that an arrow immediately renders a selected unprepared sentence without loading audio or reporting buffering.
- [x] Verify those tests fail on the old immediate-seek behavior.
- [x] Store text and sentence boundaries before audio. Use sparse indexed audio results instead of append-only chapter snapshots.
- [x] Reset a 200 ms timer on each arrow, pause old audio once, and suspend the source. On expiry request only the latest target; only then show buffering if necessary.
- [x] Ignore old results for activation. Keep normal playback advancement immediate. Finish at the final source sentence even when earlier chapters were skipped.
- [x] Cover mixed/reversed arrows, boundaries, initial preparation navigation, Space during debounce/buffering, cached return, and cancellation.

## Task 3: Integration and verification

Files: `internal/cli/run.go`, `internal/cli/run_test.go`, `README.md`.

- [x] Connect the scheduler to real TTS with deterministic per-target paths; remove partial output on cancellation/error and validate durations.
- [x] Retain provider, source loading, noninteractive output, cleanup, and first-sentence-start regression coverage.
- [x] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`.
- [x] Exercise actual system TTS through a PTY with rapid arrows, pause/resume, and interrupt; distinguish programmatic verification from subjective audible latency.
- [x] Update README to describe selection debounce, priority generation, three-sentence prefetch, session cache, and remaining synthesis latency.
- [x] Review the final diff and cover the reported cross-chapter buffering state issue with real-View integration tests.

## Verification result

Implemented directly on `main`. Full tests, race tests, vet, build, and diff checks passed. Navigation/preparation regressions also passed 20 repeated runs. Virtual-clock tests cover the 200 ms deadline, timer reset, mixed/reversed keys, cached return, and pause intent; the CLI integration covers canceled partial-file cleanup and skipping directly to chapter 12.

Actual macOS system TTS was exercised in a PTY with a 56-chapter fixture: rapid 1 -> 12 -> 40 selection, paused return to cached chapter 12, sentence navigation, Space, and Ctrl-C (exit 130). Generated files showed only visited targets and their lookahead, and the run's audio directory was removed on exit. This verifies native execution and state transitions, not a subjective listening-latency benchmark. No live Edge-service test was performed.

The review regression uses the real terminal View with controlled audio availability: after the first chapter finishes, pause/resume while the second is buffering must not display the second as completed. Buffering now initializes the newly active chapter and first sentence while preserving a same-chapter sentence selection and playback intent.
