# TUI Chapter Progress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render each speech chapter exactly once in an interactive terminal and move the active playback state up or down inside that stable chapter list during playback and seeking.

**Architecture:** The CLI gives the terminal view the complete ordered chapter text before playback starts. The view owns a chapter-state model and redraws one contiguous viewport in place; player events update active, completed, paused, and buffering state without appending duplicate chapter rows. Non-interactive output keeps its append-only transcript behavior.

**Tech Stack:** Go 1.26, ANSI terminal control sequences, `golang.org/x/term`, `github.com/mattn/go-runewidth`, Go tests.

## Global Constraints

- Space remains play/pause; Left and Right remain document-wide five-second seeks.
- An interactive seek must not append chapter or seek-status rows.
- Visible chapter indices must be unique and contiguous; the active icon moves within that list.
- Target text must be visible before resumed audio playback.
- Redirected output must contain no ANSI cursor-control sequences.

---

### Task 1: Stable chapter viewport

**Files:**
- Modify: `internal/terminal/view.go`
- Test: `internal/terminal/view_test.go`

**Interfaces:**
- Consumes: `SetChapters([]string)` before `Preparing` and `Start`.
- Produces: one in-place chapter viewport driven by `Speaking`, `Spoken`, `Paused`, `Resumed`, `Buffering`, and `Seeked`.

- [x] **Step 1: Write the failing terminal-screen regression test**

Use a small ANSI-aware test writer, configure three chapters, seek backward and replay forward, then assert the visible screen contains exactly `[1/3]`, `[2/3]`, `[3/3]` once each with the active icon on the correct row.

- [x] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/terminal -run TestViewMovesPlaybackProgressWithinStableChapterList -count=1`

Expected: FAIL because the current view tracks only one replaceable line and has no complete chapter list.

- [x] **Step 3: Implement the minimal chapter-state renderer**

Add ordered chapter text, completion state, active index, playback state, terminal height, viewport selection, and a single clear-and-redraw path. Keep the existing non-interactive append-only path.

- [x] **Step 4: Run terminal tests and verify GREEN**

Run: `go test ./internal/terminal -count=1`

Expected: PASS with unique, contiguous chapter rows and correct `▶`, `⏸`, `✓`, and pending icons.

### Task 2: CLI and playback integration

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/player/player.go`
- Test: `internal/cli/run_test.go`
- Test: `internal/player/player_test.go`

**Interfaces:**
- Consumes: document chunks returned by `textchunk.Split`.
- Produces: terminal `SetChapters(chunks)` configuration before playback and seek events that never invoke duplicate chapter appends.

- [x] **Step 1: Write the failing interactive CLI regression test**

Run an interactive fake playback and assert its first playback frame already contains one contiguous chapter list without missing indices.

- [x] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/cli -run TestRunRendersContiguousInteractiveChapterListBeforePlayback -count=1`

Expected: FAIL until all chunks are supplied to the view.

- [x] **Step 3: Wire chapters into the view and retain render-before-play ordering**

Call `view.SetChapters(chunks)` before `Preparing`; keep seek activation separate from ordinary `Speaking` events and render the target state before `Transport.Play`.

- [x] **Step 4: Run player and CLI tests and verify GREEN**

Run: `go test ./internal/player ./internal/cli -count=1`

Expected: PASS without duplicate `Speaking` events during seeks.

### Task 3: Documentation and full verification

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: the verified chapter viewport behavior.
- Produces: accurate shortcut and TUI documentation.

- [x] **Step 1: Update README behavior examples**

Describe the stable chapter list, pending/active/completed icons, viewport movement, silent seeks, and unchanged non-interactive transcript behavior.

- [x] **Step 2: Run complete verification**

Run: `go test -race ./...`

Run: `go vet ./...`

Run: `go build ./...`

Run: `git diff --check`

Expected: all commands exit 0.

- [x] **Step 3: Review final scope**

Confirm the diff contains only the existing uncommitted TUI work plus the stable chapter-progress implementation, tests, plan, dependency metadata, and README updates. Leave the changes uncommitted unless the user explicitly requests delivery.
