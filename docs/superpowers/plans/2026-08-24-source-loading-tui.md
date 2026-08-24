# Source Loading TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show an animated, stage-aware TUI loading line while `say` reads and parses a local document or web article, then clear it before the existing provider and playback UI begins.

**Architecture:** Keep filesystem, HTTP, Markdown, and Readability work in `internal/document`, but expose neutral stage events through a progress callback. Add a focused `terminal.Loader` renderer for spinner frames and same-line updates; `internal/cli` runs source loading while driving that renderer. The existing TTS selection, text chunking, audio preparation, player, and stable chapter view remain unchanged.

**Tech Stack:** Go 1.26, existing `context`, `time`, and terminal ANSI rendering; current `document`, `cli`, and `terminal` packages; Go tests with deterministic renderer steps.

## Global Constraints

- Keep all current uncommitted web-playback changes and local-file behavior.
- Do not move filesystem, HTTP, Markdown, or Readability implementation into `internal/terminal`.
- Show animation only when stdout is a terminal; redirected output remains byte-for-byte free of spinner/control sequences.
- Display the stages `Reading file`, `Parsing document`, `Reading webpage`, and `Extracting webpage content` as applicable.
- Use a single terminal row, rotate Braille spinner frames every 80 milliseconds, and clear the row before provider selection, playback preparation, or an error message.
- Preserve context cancellation and stop source loading before returning exit code 130.
- Do not change player/view chapter contracts, commit, or push.

---

### Task 1: Source progress events

**Files:**
- Modify: `internal/document/read.go`
- Modify: `internal/document/web.go`
- Modify: `internal/document/read_test.go`

**Interfaces:**
- Produces: `type Stage uint8` with `StageReadingDocument`, `StageParsingDocument`, `StageReadingWebPage`, and `StageExtractingWebPage`.
- Produces: `type ProgressFunc func(Stage)`.
- Produces: `ReadSourceWithProgress(ctx context.Context, source string, progress ProgressFunc) (name, text string, err error)`.
- Preserves: `ReadSource(ctx, source)` as a wrapper with no callback.

- [x] **Step 1: Write failing stage-sequence tests**

Add literal sequence assertions proving `.txt` reports only `StageReadingDocument`, Markdown reports reading then parsing, and an HTTP article reports reading-web then extracting-web. Reuse the real Markdown converter, `httptest.Server`, and Readability extraction; do not assert on a progress mock instead of the resulting text.

- [x] **Step 2: Run tests and verify RED**

Run: `go test ./internal/document -run 'TestReadSourceReports' -count=1`

Expected: compilation fails because `Stage`, `ProgressFunc`, and `ReadSourceWithProgress` do not exist.

- [x] **Step 3: Implement minimal stage reporting**

Add a nil-safe `report(progress, stage)` helper. Split local reading into an internal progress-aware function called by both `Read` and `ReadSourceWithProgress`; emit parsing only before Markdown conversion. Pass the callback into web reading, emitting web-read before `client.Do` and web-extract immediately before `readability.FromReader`.

- [x] **Step 4: Run document tests and verify GREEN**

Run: `go test ./internal/document -count=1`

Expected: PASS, including existing extraction and error contracts.

### Task 2: Spinner renderer and CLI lifecycle

**Files:**
- Create: `internal/terminal/loading.go`
- Create: `internal/terminal/loading_test.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Produces: `terminal.NewLoader(writer io.Writer, color, enabled bool) *Loader`.
- Produces: `(*Loader).Start(message string) error`, `Update(message string) error`, `Advance() error`, and `Finish() error`.
- Changes: CLI `documentReader` accepts `document.ProgressFunc` and defaults to `document.ReadSourceWithProgress`.
- Produces: CLI `readDocumentWithLoading(...)` which returns the document result after the loading row is cleared.

- [x] **Step 1: Write failing renderer tests**

Assert that `Start` draws `⠋`, `Advance` draws the next frame `⠙`, `Update` replaces the stage label on the same row, and `Finish` emits a final `\r\x1b[2K`. Assert a disabled loader writes nothing. Use `errorWriter` to prove renderer write errors are returned.

- [x] **Step 2: Run renderer tests and verify RED**

Run: `go test ./internal/terminal -run 'TestLoader' -count=1`

Expected: compilation fails because `NewLoader` does not exist.

- [x] **Step 3: Implement the minimal loader**

Use the frame sequence `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`. Render through one row with carriage-return plus erase-line, escape untrusted messages through the existing `safe` helper, and keep disabled methods as no-ops.

- [x] **Step 4: Write failing CLI lifecycle tests**

Inject a reader that emits web-read and web-extract stages. With stdout marked terminal and stdin non-terminal, assert the spinner/stage lines precede the normal `say  <title>` header and are cleared; with redirected stdout, assert none of the spinner frames or stage labels appear. Add a cancellation case proving the row is cleared, TTS is not initialized, and the command returns 130.

- [x] **Step 5: Run CLI tests and verify RED**

Run: `go test ./internal/cli -run 'TestRunShowsSourceLoading|TestRunHidesSourceLoading|TestRunCancelsSourceLoading' -count=1`

Expected: compilation fails because the progress-aware reader and loading lifecycle do not exist.

- [x] **Step 6: Implement CLI loading orchestration**

Start the loader before launching the progress-aware reader, drive `Advance` from an 80-millisecond ticker, map document stages to the four required English labels, drain queued stage events before clearing on completion, and cancel the read context on renderer failure or parent cancellation. Keep the non-terminal path synchronous and unchanged apart from the progress-capable signature.

- [x] **Step 7: Run terminal and CLI suites**

Run: `go test ./internal/terminal ./internal/cli -count=1`

Expected: PASS without races, leaked goroutines, or spinner bytes in redirected output.

### Task 3: Documentation and verification

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/plans/2026-08-24-source-loading-tui.md`

**Interfaces:**
- Documents: stage-aware loading behavior and its terminal-only boundary.

- [x] **Step 1: Update README and mark plan steps complete**

Add one feature bullet stating that terminal output shows reading/parsing progress for local documents and webpages, while redirected output stays stable.

- [x] **Step 2: Format and inspect**

Run: `gofmt -w internal/document/read.go internal/document/web.go internal/document/read_test.go internal/terminal/loading.go internal/terminal/loading_test.go internal/cli/run.go internal/cli/run_test.go`

Run: `git diff --check && git status --short --branch && git diff --stat`

Expected: no whitespace errors and no unrelated files.

- [x] **Step 3: Run full verification**

Run: `go test -race ./...`

Run: `go vet ./...`

Run: `go build ./...`

Expected: every command exits 0.

- [x] **Step 4: Review the final contract**

Confirm that slow file/page work is visibly represented in terminal output, source logic remains in `document`, redirected output is unchanged, the loading row clears on success/error/cancellation, and stable chapter playback behavior is untouched.
