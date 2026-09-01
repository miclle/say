# macOS Menu Bar Playback Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show native macOS menu-bar playback controls while `say` is running, publish Now Playing metadata and media-key commands, and preserve terminal behavior and a `--no-menu-bar` fallback.

**Architecture:** Keep `player.Play` as the single playback state machine. A new `internal/desktop` controller implements `player.View`, mirrors terminal events into a small immutable desktop snapshot, renders four standard `NSStatusItem` buttons, and publishes metadata through `MPNowPlayingInfoCenter`. Terminal input and desktop callbacks are merged into the existing buffered `player.Command` stream. On macOS, `desktop.Run` owns the AppKit run loop on the main OS thread while the CLI runs in a worker goroutine.

**Tech Stack:** Go 1.26, cgo, Objective-C ARC, AppKit, MediaPlayer, AVFoundation, standard Go tests.

## Global Constraints

- Preserve the existing `document -> textchunk -> tts -> player -> terminal` pipeline.
- Previous/next menu-bar and media controls navigate whole sentences, matching left/right terminal keys.
- Do not use deprecated `NSStatusItem.view`; use standard `NSStatusBarButton` instances.
- Do not add a Swift helper, IPC service, or third-party menu-bar dependency.
- `--no-menu-bar` must retain the current CLI lifecycle exactly.
- Playback must still compile on non-Darwin platforms through build-tagged no-op desktop code.
- Do not commit or push without a separate user request.

---

### Task 1: Idempotent remote commands and composite views

**Files:**
- Create: `internal/player/view.go`
- Create: `internal/player/view_test.go`
- Modify: `internal/player/player.go`
- Modify: `internal/player/navigation.go`
- Modify: `internal/player/navigation_test.go`

**Interfaces:**
- Produces: `ResumePlayback`, `PausePlayback`, and `CombineViews(views ...View) View`.
- `ResumePlayback` and `PausePlayback` are idempotent; `Toggle` retains existing behavior.

- [x] Write failing player tests proving repeated remote play/pause commands do not invert intent and proving a composite view forwards every callback in order.
- [x] Run `go test ./internal/player -run 'TestNavigationRemote|TestCombinedView' -count=1` and confirm failure is caused by missing APIs.
- [x] Add the two command values and centralize play-intent transitions in `handleCommand`.
- [x] Implement `CombineViews` with zero-view no-op behavior and first-error propagation.
- [x] Re-run the targeted tests, then `go test ./internal/player -count=1`.

### Task 2: Desktop state reducer and command integration

**Files:**
- Create: `internal/desktop/controller.go`
- Create: `internal/desktop/controller_test.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Produces: `desktop.Controls`, `desktop.NewControls(backend)`, `Controls.Configure(title string, chapters []string)`, and `Controls.Commands() <-chan player.Command`.
- Produces: `cli.RunWithControls(ctx, args, stdout, stderr, controls)` and `cli.WantsMenuBar(args []string) bool`.
- Consumes: `player.CombineViews` and the existing `player.Command` values.

- [x] Write failing reducer tests for initial hidden state, sentence text/queue indexing, selecting, buffering, paused/resumed, finish/failure cleanup, and bounded command delivery.
- [x] Write failing CLI tests showing terminal and desktop commands reach the same player and `--no-menu-bar`/help skip the desktop lifecycle.
- [x] Run `go test ./internal/desktop ./internal/cli -count=1` and confirm expected failures.
- [x] Implement the platform-neutral snapshot reducer and backend interface without AppKit calls.
- [x] Implement command-channel merging with cancellation and no goroutine leak.
- [x] Re-run targeted tests and the full desktop/CLI package tests.

### Task 3: Native AppKit and MediaPlayer bridge

**Files:**
- Create: `internal/desktop/native_darwin.h`
- Create: `internal/desktop/native_darwin.m`
- Create: `internal/desktop/runtime_darwin.go`
- Create: `internal/desktop/runtime_other.go`
- Create: `internal/desktop/runtime_darwin_test.go`

**Interfaces:**
- Produces: `desktop.Run(ctx, func(Controls) int) int`.
- Native callbacks emit explicit Go commands: toggle, backward, forward, resume, and pause.
- Native rendering consumes a desktop snapshot containing document title, current sentence, play/busy state, and flattened queue position.

- [x] Write failing native contract tests for command-number mapping, UTF-8/title truncation, and runtime cleanup.
- [x] Run `go test ./internal/desktop -count=1` and confirm the native bridge is absent.
- [x] Implement an accessory `NSApplication` event loop and four adjacent standard status items: sentence, previous, play/pause, next.
- [x] Register `MPRemoteCommandCenter` handlers and publish title, document name, playback state, playback rate, and queue index/count through `MPNowPlayingInfoCenter`.
- [x] Dispatch every AppKit mutation to the main queue, remove handlers/metadata on completion, and release the cgo handle only after the run loop exits.
- [x] Add the non-Darwin direct-run implementation and re-run desktop tests.

### Task 4: CLI lifecycle, documentation, and complete verification

**Files:**
- Modify: `cmd/say/main.go`
- Modify: `README.md`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `desktop.Run`, `cli.WantsMenuBar`, and `cli.RunWithControls`.
- Adds CLI flag: `-no-menu-bar`.

- [x] Write the failing CLI flag/help assertions before changing production parsing.
- [x] Run the focused CLI tests and confirm the missing-flag failure.
- [x] Make `main` enter `desktop.Run` only for playback invocations that want the menu bar; keep help, invalid invocation, and `--no-menu-bar` on the direct CLI path.
- [x] Document the menu-bar buttons, Control Center/media-key behavior, `--no-menu-bar`, multiple-process behavior, and macOS-only boundary.
- [x] Run `gofmt -w` on changed Go files and `clang-format` only if it is already configured; otherwise preserve the Objective-C style manually.
- [x] Run `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./...`, `go mod tidy -diff`, and `git diff --check`.
- [x] Perform real macOS normal, opt-out, and interrupt runs. A startup-thread lifecycle subprocess additionally verifies visible status items, registered remote handlers, truncated menu text, full Now Playing text, and cleanup; native callback mapping verifies sentence command semantics.

## Verification Record

- Normal macOS playback completed three speech units and exited 0.
- `--no-menu-bar` completed the same fixture and exited 0 without entering `desktop.Run`.
- A paused run interrupted with `Ctrl-C` cleared the native lifecycle and exited 130 without hanging.
- `TestNativeLifecycleOnStartupThread` executes create, render, pause/resume, clear, stop, and destroy on a real AppKit subprocess; it asserts status-item visibility, remote-command registration, full-vs-truncated metadata, and post-clear Now Playing state.
- System UI automation cannot enumerate this windowless accessory process, so the native status buttons were verified through AppKit state and callback contract tests rather than synthetic on-screen clicks.

## Self-Review

- Spec coverage: direct menu-bar controls, Now Playing, media keys, terminal coexistence, lifecycle cleanup, opt-out, non-Darwin compilation, docs, and runtime verification are each assigned to a task.
- Placeholder scan: no deferred implementation placeholders remain.
- Type consistency: desktop controls implement `player.View`; both terminal and desktop views are composed once, and all input sources converge on `player.Command`.
- Scope: one new internal package plus narrow changes to player, CLI, main, and README; no second binary or external runtime dependency.
