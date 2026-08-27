# Word Document Playback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add local `.docx` and Word 97-2003 `.doc` narration using only Go code, without `textutil`, LibreOffice, Word, CGO, or other external processes.

**Architecture:** Keep `ReadSourceWithProgress` as the source dispatcher and extend local-file loading before the existing UTF-8 check. A bounded OOXML reader extracts narratable content from DOCX packages; a bounded MS-DOC reader uses `mscfb` for the OLE container and project-owned FIB/CLX/piece-table parsing for the main Word story. Both return normalized UTF-8 narration to the unchanged `textchunk -> tts -> player -> terminal` pipeline.

**Tech Stack:** Go 1.26 standard library (`archive/zip`, `encoding/xml`, `encoding/binary`, `unicode/utf16`), `golang.org/x/text/encoding/charmap`, and `github.com/richardlehane/mscfb v1.0.7`.

## Global Constraints

- Runtime parsing must not execute or require system applications or external processes.
- Support local `.docx` and Word 97-2003 `.doc`; remote Word URLs, `.docm`, OCR, images, macros, and password-protected documents remain unsupported.
- Extract the main document story only. DOCX omits comments, headers, footers, footnotes, endnotes, deleted/moved revisions, text boxes, and embedded objects. DOC omits field instructions but deliberately does not interpret CHPX/PAPX formatting properties, so property-marked deleted revisions and exact table-row boundaries are outside the initial `.doc` contract.
- Preserve DOCX paragraphs, list items, manual line breaks, hyperlink display text, table rows, and table cell boundaries as useful narration. Preserve DOC main-story characters and paragraph controls, with `0x07` represented as a conservative cell pause.
- Reject malformed, encrypted, empty, oversized, or canceled inputs before TTS initialization.
- Preserve existing `.txt`, `.md`, `.markdown`, and HTTP(S) behavior.
- Do not commit or push unless the user explicitly requests it.

---

### Task 1: Bounded DOCX narration extraction

**Files:**
- Create: `internal/document/docx.go`
- Create: `internal/document/docx_test.go`

**Interfaces:**
- Consumes: `context.Context`, a local path, and `normalizeNarration(string)`.
- Produces: `func readDOCX(ctx context.Context, path string) (string, error)`.

- [x] **Step 1: Write the failing DOCX behavior tests**

Create a ZIP fixture in the test using `archive/zip`. The main XML fixture must contain two paragraphs, a numbered-list paragraph, hyperlink display text, a manual break, inserted/deleted revisions, and a two-cell table. Add regression fixtures for moved revisions, deleted rows/cells, drawing/text-box content, and multi-Choice markup compatibility with a single Fallback. Assert the literal narration:

```go
want := "Guide\n\nRead the visible link.\nNext line.\n\nFirst item\n\nAdded text\n\nName，Status\nready，yes"
```

Add independent tests asserting clear errors for a non-ZIP file, missing `word/document.xml`, malformed XML, an oversized main document part, an empty document, and a context canceled before parsing.

- [x] **Step 2: Run DOCX tests and verify RED**

Run: `go test ./internal/document -run 'TestReadDOCX' -count=1`

Expected: compilation fails because `readDOCX` is undefined.

- [x] **Step 3: Implement the minimum bounded DOCX reader**

Implement these exact limits and namespaces:

```go
const (
    maxWordInputBytes = 64 << 20
    maxDOCXEntries = 4096
    maxDOCXUncompressedBytes = 256 << 20
    maxDOCXMainXMLBytes = 16 << 20
    maxWordNarrationBytes = 16 << 20
)

const transitionalWordprocessingML = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
const strictWordprocessingML = "http://purl.oclc.org/ooxml/wordprocessingml/main"
```

Validate the regular file and compressed size, inspect ZIP entry counts and declared uncompressed sizes without extracting resources, and open only `word/document.xml`. Stream XML tokens with context checks and accept content only under the WordprocessingML body. Capture only `t`, `tab`, `br`, and `cr`; suppress `del`, `moveFrom`, drawing/object/picture/text-box subtrees and deleted table rows/cells. Treat the parser as a minimal markup-compatibility consumer by ignoring Choice branches and reading a single Fallback. Use single newlines for list paragraphs, blank lines for ordinary paragraphs, `，` for table cells, and newlines for table rows. Normalize and reject empty or oversized narration.

- [x] **Step 4: Run DOCX tests and verify GREEN**

Run: `go test ./internal/document -run 'TestReadDOCX' -count=1`

Expected: all DOCX tests pass.

- [x] **Step 5: Refactor without changing behavior**

Keep ZIP validation, XML token extraction, and narration boundary handling in focused helpers in `docx.go`. Re-run the same test command after refactoring.

### Task 2: Bounded Word 97-2003 DOC narration extraction

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/document/doc.go`
- Create: `internal/document/cfb.go`
- Create: `internal/document/doc_test.go`

**Interfaces:**
- Consumes: `context.Context`, a local path, `maxWordInputBytes`, `maxWordNarrationBytes`, and `normalizeNarration(string)`.
- Produces: `func readDOC(ctx context.Context, path string) (string, error)`.

- [x] **Step 1: Add only the OLE container dependency**

Run: `go get github.com/richardlehane/mscfb@v1.0.7`

Then run: `go mod tidy`

Expected: `mscfb v1.0.7` and its required modules appear; no DOC/DOCX conversion SDK is added.

- [x] **Step 2: Write the failing DOC behavior tests**

Build a deterministic CFB v3 fixture entirely in Go test code. It must contain `WordDocument` and `1Table` streams of at least 4096 bytes, a Word 97 FIB selecting `1Table`, one CLX/Pcdt piece, and UTF-16LE body text:

```go
source := "标题\r第一段。\rName\x07Status\rready\x07yes\r"
want := "标题\n\n第一段。\n\nName，Status\n\nready，yes"
```

Add independent tests for a compressed Windows-1252 piece containing `Café — ready`, a field instruction whose cached result remains visible, a non-CFB file, missing Word/Table streams, encrypted FIB flags, malformed CLX, out-of-range piece offsets, empty body text, oversized input, and pre-canceled context.

- [x] **Step 3: Run DOC tests and verify RED**

Run: `go test ./internal/document -run 'TestReadDOC' -count=1`

Expected: compilation fails because `readDOC` is undefined.

- [x] **Step 4: Implement FIB and CLX validation**

Preflight the CFB header, FAT-backed directory chain, entry count, and traversal depth before calling `mscfb.New`; keep container metadata reads context-aware and bounded. Use `mscfb.New` only to obtain bounded root-level `WordDocument`, `0Table`, and `1Table` streams, rejecting duplicate target streams. Parse dynamic FIB offsets from `csw` and `cslw`; reject `fEncrypted` and `fObfuscated`; read `ccpText`, `fcClx`, and `lcbClx`; select the table stream using `fWhichTblStm`.

Parse CLX safely by skipping bounded `Prc` records, requiring `Pcdt` marker `0x02`, validating `(lcb-4)%12 == 0`, monotonic CP values, sufficient PCD entries, and a final CP covering `ccpText`.

- [x] **Step 5: Implement piece decoding and narration controls**

For each piece intersecting `[0, ccpText)`, decode compressed pieces with `charmap.Windows1252` and uncompressed pieces with UTF-16LE. Validate every byte range before slicing. Preserve field results but suppress field instructions using control characters `0x13`, `0x14`, and `0x15`; map `0x07` to a table-cell separator, `0x09` to a space, `0x0B` to a line break, and `0x0C`/`0x0D` to paragraph boundaries; drop other control/object markers. Normalize and enforce the output limit.

- [x] **Step 6: Run DOC tests and verify GREEN**

Run: `go test ./internal/document -run 'TestReadDOC' -count=1`

Expected: all DOC tests pass.

### Task 3: Local source dispatch and progress integration

**Files:**
- Modify: `internal/document/read.go`
- Modify: `internal/document/web.go`
- Modify: `internal/document/read_test.go`

**Interfaces:**
- Consumes: `readDOCX(ctx, path)` and `readDOC(ctx, path)`.
- Produces: context-aware local dispatch while preserving `Read(path)` compatibility.

- [x] **Step 1: Write failing local-dispatch tests**

Add tests proving case-insensitive `.DOCX`/`.DOC` dispatch and progress stages:

```go
wantStages := []Stage{StageReadingDocument, StageParsingDocument}
```

Add a cancellation test proving `ReadSource` returns `context.Canceled` for a pre-canceled Word read. Retain the existing literal expectations for text and Markdown behavior.

- [x] **Step 2: Run dispatch tests and verify RED**

Run: `go test ./internal/document -run 'TestRead(Source.*Word|Word)' -count=1`

Expected: Word fixtures fail under the current UTF-8 local-file path.

- [x] **Step 3: Implement context-aware local dispatch**

Change the private signature to:

```go
func readLocal(ctx context.Context, path string, progress ProgressFunc) (name string, text string, err error)
```

Keep `Read(path)` by passing `context.Background()`. In `ReadSourceWithProgress`, pass the caller context. After regular-file validation and `StageReadingDocument`, dispatch `.docx` and `.doc` before reading bytes as UTF-8; report `StageParsingDocument` exactly once for Markdown and Word formats.

- [x] **Step 4: Run the document package and verify GREEN**

Run: `go test -race ./internal/document -count=1`

Expected: all existing and new document tests pass.

### Task 4: User-facing contract and CLI regression coverage

**Files:**
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: the unchanged `document.ReadSourceWithProgress` function type.
- Produces: accurate CLI help and README boundaries for `.doc`/`.docx`.

- [x] **Step 1: Write the failing CLI help assertion**

Change the help test to require wording equivalent to:

```text
local text, Markdown, or Word document, or HTTP(S) web article
```

Run: `go test ./internal/cli -run TestRunHelp -count=1`

Expected: failure because help still says only local UTF-8 documents.

- [x] **Step 2: Update CLI help and README**

Document `.docx` OOXML extraction, `.doc` Word 97-2003 main-story extraction, unsupported encrypted/legacy/pre-97 documents, omitted non-text objects and secondary stories, bounded parsing, and the absence of external runtime converters. Remove the statement that Word parsing is not included; keep PDF explicitly unsupported.

- [x] **Step 3: Verify CLI and docs-adjacent behavior**

Run: `go test ./internal/cli -count=1`

Expected: all CLI tests pass.

### Task 5: Full verification and diff review

**Files:**
- Review every modified file; make only regression-backed corrections.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: a verified, uncommitted feature diff.

- [x] **Step 1: Format and module-graph checks**

Run:

```bash
gofmt -w internal/document/*.go internal/cli/*.go
go mod tidy
go mod tidy -diff
gofmt -d internal/document internal/cli
git diff --check
```

Expected: the final three checks produce no diff/errors.

- [x] **Step 2: Full verification**

Run:

```bash
go test -race ./...
go vet ./...
go build ./...
```

Expected: every command exits zero.

- [x] **Step 3: Review requirements and mutations**

Inspect `git diff --stat`, `git diff`, and `git status --short`. Confirm tests would fail for wrong extension dispatch, DOCX deleted/moved-revision inclusion, missing ZIP/CFB bounds, wrong DOC table selection, wrong compressed decoding, visible field instructions, ignored context cancellation, and initialization of TTS after a parse error. Report the CHPX/PAPX limitation explicitly.
