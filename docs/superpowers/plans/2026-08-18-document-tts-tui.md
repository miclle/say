# Document TTS TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go command that opens a UTF-8 text document, keeps natural paragraphs together within a configurable TTS request limit, prints each paragraph-sized unit in a terminal UI, and plays it with the operating system TTS.

**Architecture:** Keep the speech engine behind a small `tts.Speaker` interface. The CLI reads and validates a document, `textchunk.Split` creates ordered rune-safe speech units, and `player.Play` emits each unit to a terminal renderer immediately before a blocking speech call, which guarantees text/audio ordering without buffering the entire audio stream.

**Tech Stack:** Go 1.26 standard library, macOS `/usr/bin/say`, ANSI terminal output, `go test`, `go vet`.

## Global Constraints

- The default TTS implementation on macOS is the voice selected in System Settings through `/usr/bin/say`.
- Every speech unit is non-empty and contains no more than `--max-chars` Unicode code points; the default limit is 500.
- Text becomes visible before its matching blocking TTS call begins.
- The initial document contract is a local, regular UTF-8 text file; `.txt` and Markdown-like text are accepted without format-specific parsing.
- Speech text is sent through standard input, never interpolated into a shell command.
- Untrusted document and TTS strings are escaped before terminal rendering, and output failures abort playback before speech starts.
- The implementation has no third-party runtime dependencies.

---

### Task 1: Go module, document reader, and paragraph-first bounded chunker

**Files:**
- Create: `go.mod`
- Create: `internal/document/read.go`
- Create: `internal/document/read_test.go`
- Create: `internal/textchunk/split.go`
- Create: `internal/textchunk/split_test.go`

**Interfaces:**
- Produces: `document.Read(path string) (name string, text string, err error)`
- Produces: `textchunk.Split(text string, maxRunes int) ([]string, error)`

- [x] **Step 1: Write failing document-reader tests**

  Cover BOM removal, CRLF normalization, empty documents, directories, and invalid UTF-8 using temporary real files. For example:

  ```go
  name, text, err := Read(path)
  if err != nil || name != "lesson.txt" || text != "第一句。\nSecond sentence." {
      t.Fatalf("Read() = %q, %q, %v", name, text, err)
  }
  ```

- [x] **Step 2: Run the reader tests and verify the package is missing**

  Run: `go test ./internal/document`

  Expected: FAIL because `Read` has not been defined.

- [x] **Step 3: Implement the minimal document reader**

  Use `os.Stat` and `os.ReadFile`; reject non-regular, empty-after-trimming, and invalid UTF-8 inputs; strip one UTF-8 BOM and normalize `\r\n`/`\r` to `\n`.

- [x] **Step 4: Run the reader tests and verify they pass**

  Run: `go test ./internal/document`

  Expected: PASS.

- [x] **Step 5: Write failing chunker tests**

  Use literal expectations for blank-line paragraph boundaries, greedy sentence packing inside oversized paragraphs, long sentences split at soft punctuation or whitespace, emoji/rune safety, whitespace normalization, invalid limits, and the invariant `utf8.RuneCountInString(chunk) <= maxRunes`. For example:

  ```go
  got, err := Split("你好世界。Go is fun!", 20)
  want := []string{"你好世界。Go is fun!"}
  ```

- [x] **Step 6: Run the chunker tests and verify the package is missing**

  Run: `go test ./internal/textchunk`

  Expected: FAIL because `Split` has not been defined.

- [x] **Step 7: Implement rune-safe paragraph-first splitting**

  Preserve blank-line-delimited natural paragraphs when they fit. For oversized paragraphs, detect hard sentence boundaries (`。！？!?` and an English full stop followed by whitespace/end), greedily pack adjacent sentences up to the limit, then split oversized sentences at the latest soft boundary (`，,；;：:、` or whitespace) and hard-split only when no soft boundary exists.

- [x] **Step 8: Run focused and full tests**

  Run: `go test ./internal/document ./internal/textchunk`

  Expected: PASS.

### Task 2: System TTS adapter and playback ordering

**Files:**
- Create: `internal/tts/speaker.go`
- Create: `internal/tts/system_darwin.go`
- Create: `internal/tts/system_darwin_test.go`
- Create: `internal/tts/system_other.go`
- Create: `internal/player/player.go`
- Create: `internal/player/player_test.go`

**Interfaces:**
- Produces: `tts.Speaker` with `Name() string` and `Speak(context.Context, string) error`
- Produces: `tts.NewSystem(voice string, rate int) (Speaker, error)`
- Consumes: ordered `[]string` from `textchunk.Split`
- Produces: `player.Play(ctx context.Context, chunks []string, speaker tts.Speaker, view player.View) error`
- Produces: error-returning `player.View` callbacks `Start`, `Speaking`, `Spoken`, `Failed`, and `Finish`

- [x] **Step 1: Write a failing Darwin system-speaker subprocess test**

  Replace the executable path inside the package with a temporary executable script that records arguments and copies stdin. Assert the exact voice/rate arguments and literal UTF-8 stdin, so shell interpolation or positional message arguments fail the test.

- [x] **Step 2: Run the TTS test and verify it fails**

  Run: `go test ./internal/tts`

  Expected: FAIL because the system speaker does not exist.

- [x] **Step 3: Implement the system speaker**

  On Darwin invoke `/usr/bin/say` through `exec.CommandContext`, add `-v` and `-r` only when configured, and set `cmd.Stdin = strings.NewReader(text)`. Return a wrapped error containing stderr. On other operating systems return a precise unsupported-platform error.

- [x] **Step 4: Run the TTS test and verify it passes**

  Run: `go test ./internal/tts`

  Expected: PASS.

- [x] **Step 5: Write failing playback-order tests**

  Record real callback and fake-speaker events into one ordered slice and assert the sequence below, plus stop-on-error and canceled-context behavior:

  ```go
  want := []string{
      "start:2", "speaking:0:第一句。", "speak:第一句。", "spoken:0",
      "speaking:1:Second.", "speak:Second.", "spoken:1", "finish",
  }
  ```

- [x] **Step 6: Run the player test and verify it fails**

  Run: `go test ./internal/player`

  Expected: FAIL because `Play` and `View` do not exist.

- [x] **Step 7: Implement synchronous playback orchestration**

  Validate dependencies and chunks, call `view.Speaking` before `speaker.Speak`, abort before speech on output failure, stop immediately on context cancellation or speech failure, and emit `Finish` only after every unit succeeds.

- [x] **Step 8: Run focused and full tests**

  Run: `go test ./internal/tts ./internal/player ./...`

  Expected: PASS.

### Task 3: Terminal view and CLI integration

**Files:**
- Create: `internal/terminal/view.go`
- Create: `internal/terminal/view_test.go`
- Create: `internal/cli/run.go`
- Create: `internal/cli/run_test.go`
- Create: `cmd/say/main.go`

**Interfaces:**
- Consumes: `player.View` and writes to an injected `io.Writer`
- Produces: `terminal.New(writer io.Writer, color bool, title string, engine string) *View`
- Consumes: `document.Read`, `textchunk.Split`, `tts.NewSystem`, and `player.Play`
- Produces: `cli.Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int`

- [x] **Step 1: Write failing terminal-view output tests**

  Render without color into a buffer and assert a literal header, `[1/2]` speaking line, completion marker, failure message, and final summary. The first paragraph-sized unit must appear as soon as `Speaking` is called.

- [x] **Step 2: Run the terminal tests and verify they fail**

  Run: `go test ./internal/terminal`

  Expected: FAIL because `View` does not exist.

- [x] **Step 3: Implement the terminal view**

  Print a compact header and append one paragraph-sized unit at a time. Escape C0/C1 controls from untrusted strings, propagate output errors, use ANSI bold/color only when enabled, keep no full-screen state, and show deterministic indexes and totals so redirected output remains useful.

- [x] **Step 4: Run the terminal tests and verify they pass**

  Run: `go test ./internal/terminal`

  Expected: PASS.

- [x] **Step 5: Write failing CLI integration tests**

  Inject a package-local system-speaker factory in tests, run against a temporary UTF-8 document, and assert default limit propagation, printed-before-spoken order, `--max-chars` validation, missing file errors, usage exit code 2, speech failures, and canceled exit code 130.

- [x] **Step 6: Run the CLI tests and verify they fail**

  Run: `go test ./internal/cli`

  Expected: FAIL because `Run` does not exist.

- [x] **Step 7: Implement CLI and signal-aware entry point**

  Parse `--voice`, `--rate`, `--max-chars`, and `--no-color`; require exactly one document path; enable color only for a character-device stdout; construct the system speaker; and use `signal.NotifyContext` for Ctrl-C cancellation.

- [x] **Step 8: Run CLI and full tests**

  Run: `go test ./internal/cli ./...`

  Expected: PASS.

### Task 4: User documentation and release-quality verification

**Files:**
- Modify: `README.md`

**Interfaces:**
- Documents: build command, supported input, default system TTS behavior, paragraph/limit behavior, flags, examples, controls, platform boundary, and test commands.

- [x] **Step 1: Replace the placeholder README with shipped behavior**

  Include `go install ./cmd/say`, `go run ./cmd/say -- ./notes.md`, `--max-chars 300`, `--voice Tingting`, `--rate 210`, Ctrl-C behavior, UTF-8 input limitations, and macOS system-voice selection.

- [x] **Step 2: Format and run the complete verification suite**

  Run: `gofmt -w cmd internal && go test -race ./... && go vet ./... && go build ./...`

  Expected: every command exits 0 with no test failures or vet diagnostics.

- [x] **Step 3: Run a no-audio CLI error-path smoke test**

  Run: `go build -o /tmp/say-tts-tui ./cmd/say && /tmp/say-tts-tui --max-chars 0 README.md`

  Expected: exit 2 and a clear `max-chars must be greater than zero` diagnostic before any TTS invocation.

- [x] **Step 4: Review the final diff against all four product requirements**

  Confirm one code/test/doc path for opening a document, default system TTS, per-call length enforcement, and paragraph-level print-before-play ordering.
