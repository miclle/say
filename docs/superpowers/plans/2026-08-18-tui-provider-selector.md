# TUI Provider Selector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let interactive users choose the TTS provider in the terminal while preserving explicit `--provider` behavior and the non-interactive system default.

**Architecture:** Add a small generic single-line terminal selector that decodes arrow keys, Enter, and cancellation without importing TTS concerns. The CLI decides the provider through three explicit paths: a supplied flag wins, an interactive terminal invokes the selector, and redirected/non-interactive execution retains `system`.

**Tech Stack:** Go standard library, existing raw-terminal support, existing CLI dependency injection, Go tests.

## Global Constraints

- Explicit `--provider system|edge` must skip the selector.
- Non-interactive stdin/stdout must continue to default to `system`.
- Interactive selection uses `↑`/`↓`, Enter, and Ctrl-C.
- Provider-specific `--rate` and `--speed` validation runs against the resolved provider.
- No new third-party dependency.

---

### Task 1: Terminal selector

**Files:**
- Create: `internal/terminal/select.go`
- Create: `internal/terminal/select_test.go`

**Interfaces:**
- Consumes: an already-raw `io.Reader`, an `io.Writer`, a prompt, and non-empty labels.
- Produces: `func Select(ctx context.Context, reader io.Reader, writer io.Writer, prompt string, labels []string) (int, error)`.

- [x] **Step 1: Write failing selection behavior tests**

Add tests proving `\x1b[B\r` chooses the second label, `\x1b[A\r` wraps to the final label, Enter chooses the initial label, and cancellation returns `context.Canceled`.

- [x] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/terminal -run TestSelect -count=1`

Expected: compilation fails because `Select` does not exist.

- [x] **Step 3: Implement the selector**

Decode CSI and SS3 arrow sequences one byte at a time, redraw one line with `\r\x1b[2K`, print a newline on confirmation, and select between input/error and `ctx.Done()` so signal cancellation is not blocked by a terminal read.

- [x] **Step 4: Run the focused tests and verify GREEN**

Run: `go test ./internal/terminal -run TestSelect -count=1`

Expected: all selector tests pass.

### Task 2: CLI provider resolution

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `terminal.Select`, `dependencies.beginRaw`, explicit flag presence from `FlagSet.Visit`, and terminal detection.
- Produces: `type providerSelector func(context.Context, io.Reader, io.Writer) (tts.Provider, error)` in CLI dependencies.

- [x] **Step 1: Write failing CLI behavior tests**

Add tests proving an omitted provider invokes the selector only in an interactive terminal, the returned Edge provider receives Edge options and `.mp3` paths, an explicit provider skips selection, non-interactive execution remains system, provider-specific flags are validated after selection, and cancellation restores raw mode with exit code 130.

- [x] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/cli -run 'TestRun.*Provider|TestRun.*Selection' -count=1`

Expected: the interactive omitted-provider cases fail because CLI still resolves the flag default directly.

- [x] **Step 3: Implement provider resolution**

Determine interactivity before synthesizer construction. When `--provider` was not visited and the terminal is interactive, enter raw mode, call the injected selector, restore raw mode on every outcome, then apply `--rate`/`--speed` compatibility checks to the selected provider. Keep the existing flag and non-interactive paths unchanged.

- [x] **Step 4: Run CLI and full tests**

Run: `go test ./internal/cli -count=1` and `go test ./...`

Expected: all tests pass.

### Task 3: Documentation and terminal acceptance

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: the shipped selector behavior from Tasks 1 and 2.
- Produces: user-facing interactive/default/override documentation.

- [x] **Step 1: Document provider selection**

Explain that an interactive terminal prompts when `--provider` is omitted, arrow keys choose, Enter confirms, explicit `--provider` skips the prompt, and redirected execution remains `system`.

- [x] **Step 2: Run real PTY acceptance**

Run `go run ./cmd/say --no-color <neutral-document>` in a PTY, choose Edge with `↓` and Enter, and verify the Edge engine appears before MP3 playback. Repeat with explicit `--provider system` and verify no selector is shown.

- [x] **Step 3: Run final verification**

Run `gofmt`, `go test -race ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`, then review staged, unstaged, and untracked scope.
