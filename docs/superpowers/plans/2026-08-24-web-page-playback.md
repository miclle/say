# Web Page Playback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `say` accept an HTTP(S) article URL, extract its readable title and paragraph-preserving narration with `github.com/miclle/readability.go`, and play it through the existing TTS/TUI pipeline without changing local-file behavior.

**Architecture:** Add source dispatch to the existing `internal/document` package: local paths continue through `Read`, while HTTP(S) URLs use a context-aware, size-limited HTTP fetch followed by Readability extraction. Convert Readability's cleaned article HTML to narration while retaining block boundaries, then pass the result into the unchanged `textchunk`, TTS, streaming player, and terminal view layers. Inject the document reader at the CLI boundary so URL behavior is testable without live network access.

**Tech Stack:** Go 1.26, standard `net/http`, `net/url`, and `mime`; `github.com/miclle/readability.go v0.1.0`; existing `golang.org/x/net/html`, Go tests, and `httptest`.

## Global Constraints

- Preserve local `.txt`, `.md`, and `.markdown` behavior and all existing playback controls.
- Recognize only absolute `http://` and `https://` URLs as remote input.
- Use a 15-second HTTP client timeout, a 10 MiB decompressed response limit, and a `say` User-Agent.
- Accept `text/html` and `application/xhtml+xml`; allow a missing Content-Type for compatibility.
- Preserve article paragraph boundaries before the existing `--max-chars` splitter runs.
- HTTP errors, unsupported URL schemes/content types, oversized pages, parse failures, and empty extraction must fail before TTS initialization.
- Do not commit or push unless the user requests it.

---

### Task 1: Web source reader

**Files:**
- Create: `internal/document/web.go`
- Modify: `internal/document/read_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: existing `Read(path string) (name, text string, err error)` for local inputs.
- Produces: `ReadSource(ctx context.Context, source string) (name, text string, err error)` for CLI source dispatch.
- Produces: internal `readWeb(ctx context.Context, source string, client httpDoer) (name, text string, err error)` for deterministic HTTP tests.

- [x] **Step 1: Write failing source-dispatch, extraction, and failure-contract tests**

Add tests with `httptest.Server` proving that `ReadSource` keeps local reads unchanged and that `readWeb` sends a `say` User-Agent, accepts HTML, extracts the article title, removes navigation/script text, and returns paragraph-separated narration. The extraction fixture must contain enough real prose for Readability scoring. In the same RED phase, cover non-2xx status, non-HTML Content-Type, more than 10 MiB of body data, no readable content, unsupported URL schemes, and a canceled request context; assert error categories and observable messages rather than internal constants.

- [x] **Step 2: Run the document tests and verify RED**

Run: `go test ./internal/document -run 'TestReadSource|TestReadWeb' -count=1`

Expected: compilation fails because `ReadSource` and `readWeb` do not exist.

- [x] **Step 3: Add the pinned Readability dependency and minimal reader implementation**

Run: `go get github.com/miclle/readability.go@v0.1.0`

Implement URL dispatch, `http.NewRequestWithContext`, status/content-type/body-limit checks, `readability.FromReader`, title fallback to the final response host, and empty-content validation. Convert `Article.Content`, not flattened `Article.TextContent`, to narration so `<p>` and heading boundaries remain visible to `textchunk.Split`.

- [x] **Step 4: Run focused tests and verify GREEN**

Run: `go test ./internal/document -run 'TestReadSource|TestReadWeb' -count=1`

Expected: PASS.

- [x] **Step 5: Run all document tests**

Run: `go test ./internal/document -count=1`

Expected: PASS with local and web behavior green.

### Task 2: CLI integration

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `document.ReadSource(ctx, source)` from Task 1.
- Produces: `documentReader func(context.Context, string) (string, string, error)` in CLI dependencies.

- [x] **Step 1: Write the failing URL playback test**

Inject a document reader that returns `("Article title", "first paragraph\n\nsecond paragraph", nil)` for a literal HTTPS URL. Assert that the real CLI pipeline synthesizes both paragraphs, renders the article title, and initializes no TTS when source reading fails.

- [x] **Step 2: Run the focused CLI test and verify RED**

Run: `go test ./internal/cli -run 'TestRunReadsWebSourceBeforePlayback' -count=1`

Expected: compilation fails because the dependency and URL-aware call do not exist.

- [x] **Step 3: Wire the document reader into the CLI**

Add `readDocument` to `dependencies`, default it to `document.ReadSource`, use it with the command context, and keep the rest of the playback path unchanged. Update usage from `<document>` to `<document-or-url>` and describe local document or web article input.

- [x] **Step 4: Run focused and full CLI tests**

Run: `go test ./internal/cli -run 'TestRunReadsWebSourceBeforePlayback|TestRunHelp' -count=1`

Then run: `go test ./internal/cli -count=1`

Expected: PASS.

### Task 3: Documentation and release-quality verification

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: the shipped command contract from Tasks 1 and 2.
- Produces: installation, feature, usage, privacy/network, and input-boundary documentation matching actual behavior.

- [x] **Step 1: Update README**

Document direct `say https://example.com/article` usage, Readability extraction, HTTP limits/errors, the fact that page HTML is downloaded locally, and that only the extracted narration is sent onward when the Edge provider is selected. Remove the statement that web parsing is unsupported.

- [x] **Step 2: Format and inspect the diff**

Run: `gofmt -w internal/document/web.go internal/document/read_test.go internal/cli/run.go internal/cli/run_test.go`

Run: `git diff --check && git diff --stat && git diff`

Expected: no whitespace errors and no unrelated changes.

- [x] **Step 3: Run full verification**

Run: `go test -race ./...`

Run: `go vet ./...`

Run: `go build ./...`

Expected: every command exits 0.

- [x] **Step 4: Review goal coverage**

Confirm that `say URL` reaches the unchanged playback path, local file tests remain green, failure paths stop before TTS, paragraph boundaries survive extraction, README matches shipped behavior, and no commit or push occurred.
