package terminal

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestViewPreviewsUnpreparedSentenceInExistingChapterList(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"One.", "Two. Three."})
	view.Start(2)
	view.Speaking(0, 2, "One.")
	if err := view.Selected(1, 2, "Two. Three.", 1); err != nil {
		t.Fatal(err)
	}
	if view.activeChapter != 1 || view.activeSentence != 1 || view.playing {
		t.Fatalf("selection did not move independently of audio: %+v", view)
	}
	if len(view.chapters) != 2 || !strings.Contains(view.chapterStatus, "selecting") || strings.Contains(output.String(), "buffering") {
		t.Fatalf("preview changed chapter structure or reported audio buffering: %q", output.String())
	}
	view.Buffering(1, 2)
	view.Seeked(1, 2, "Two. Three.", 1, true, 0, 0, 0, false)
	if view.chapterStatus != "" || view.activeSentence != 1 || !view.playing {
		t.Fatal("activation did not clear selection/buffering")
	}
}

func TestViewBufferingInitializesNewChapterWithoutResettingSelectedSentence(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"One.", "Two. Three."})
	view.Speaking(0, 2, "One.")
	view.Spoken(0, 2)
	view.Buffering(1, 2)
	if view.activeChapter != 1 || view.activeSentence != 0 || view.activeComplete {
		t.Fatalf("buffering retained previous chapter state: chapter=%d sentence=%d complete=%t", view.activeChapter, view.activeSentence, view.activeComplete)
	}
	view.Selected(1, 2, "Two. Three.", 1)
	view.Buffering(1, 2)
	if view.activeChapter != 1 || view.activeSentence != 1 || view.activeComplete || view.playing {
		t.Fatal("same-chapter buffering reset the selected sentence or pause state")
	}
}

func TestViewRendersPlaybackLifecycleWithoutColor(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "lesson.txt", "macOS say (system voice)")

	view.Start(2)
	view.Speaking(0, 2, "第一句。")
	view.Paused(0, 2)
	view.Resumed(0, 2)
	view.Spoken(0, 2)
	view.Buffering(1, 2)
	view.Speaking(1, 2, "Second.")
	view.Failed(1, 2, errors.New("voice unavailable"))
	view.Finish(2)

	want := "say  lesson.txt\n" +
		"TTS  macOS say (system voice) · 2 speech units\n\n" +
		"Space Play/Pause · ←/→ Sentence · ↑/↓ Chapter\n\n" +
		"[1/2] ▶ 第一句。\n" +
		"\x1b[1A\r\x1b[2K[1/2] ⏸ 第一句。\n" +
		"\x1b[1A\r\x1b[2K[1/2] ▶ 第一句。\n" +
		"\x1b[1A\r\x1b[2K[1/2] ✓ 第一句。\n" +
		"      … buffering speech unit 2/2\n" +
		"[2/2] ▶ Second.\n" +
		"      ✗ voice unavailable\n\n" +
		"✓ Finished 2 speech units.\n"
	if got := output.String(); got != want {
		t.Fatalf("output =\n%q\nwant =\n%q", got, want)
	}
}

func TestViewSeekReplacesCurrentSentenceWithoutAppending(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")
	view.Speaking(0, 2, "First.")

	if err := view.Seeked(1, 2, "Second.", 0, true, 5*time.Second, 6*time.Second, 12*time.Second, true); err != nil {
		t.Fatalf("Seeked() error = %v", err)
	}

	want := "[1/2] ▶ First.\n" +
		"\x1b[1A\r\x1b[2K[2/2] ▶ Second.\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestViewSeekKeepsPausedIcon(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")
	view.Speaking(0, 2, "First.")

	if err := view.Seeked(1, 2, "Second.", 0, false, 5*time.Second, 6*time.Second, 12*time.Second, true); err != nil {
		t.Fatalf("Seeked() error = %v", err)
	}

	want := "[1/2] ▶ First.\n" +
		"\x1b[1A\r\x1b[2K[2/2] ⏸ Second.\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestViewMovesPlaybackProgressWithinStableChapterList(t *testing.T) {
	screen := newTestTerminalScreen()
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{"One.", "Two.", "Three."})

	view.Start(3)
	view.Speaking(0, 3, "One.")
	view.Spoken(0, 3)
	view.Speaking(1, 3, "Two.")
	view.Seeked(2, 3, "Three.", 0, true, 5*time.Second, 7*time.Second, 12*time.Second, true)
	view.Seeked(0, 3, "One.", 0, true, -5*time.Second, 2*time.Second, 12*time.Second, true)
	view.Seeked(2, 3, "Three.", 0, true, 5*time.Second, 7*time.Second, 12*time.Second, true)

	want := []string{
		"[1/3] ✓ One.",
		"[2/3] · Two.",
		"[3/3] ▶ Three.",
	}
	if got := screen.chapterLines(); !equalStrings(got, want) {
		t.Fatalf("visible chapter lines = %#v, want %#v", got, want)
	}
}

func TestViewHighlightsOnlyActiveChapterText(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"One.", "Two."})

	view.Speaking(0, 2, "One.")
	if got := output.String(); !strings.Contains(got, "\x1b[7mOne.\x1b[0m") {
		t.Fatalf("speaking output = %q, want active chapter text in reverse video", got)
	}
	if got := output.String(); strings.Contains(got, "\x1b[7mTwo.\x1b[0m") {
		t.Fatalf("speaking output = %q, pending chapter text must not be highlighted", got)
	}

	output.Reset()
	view.Paused(0, 2)
	if got := output.String(); !strings.Contains(got, "\x1b[7mOne.\x1b[0m") {
		t.Fatalf("paused output = %q, want active chapter highlight to preserve position", got)
	}

	output.Reset()
	view.Spoken(0, 2)
	if got := output.String(); strings.Contains(got, "\x1b[7m") {
		t.Fatalf("completed output = %q, completed chapter must not stay highlighted", got)
	}

	output.Reset()
	plainView := New(&output, false, "notes.md", "test TTS")
	plainView.SetChapters([]string{"Plain."})
	plainView.Speaking(0, 1, "Plain.")
	if got := output.String(); strings.Contains(got, "\x1b[7m") {
		t.Fatalf("plain output = %q, no-color mode must not emit reverse video", got)
	}
}

func TestViewHighlightsEveryWrappedLineOfActiveChapter(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.width = 14
	view.SetChapters([]string{"abcdefgh", "Two."})

	view.Speaking(0, 2, "abcdefgh")

	wantHighlights := []string{
		"\x1b[7mabcdef\x1b[0m",
		"\x1b[7mgh\x1b[0m",
	}
	for _, want := range wantHighlights {
		if got := output.String(); !strings.Contains(got, want) {
			t.Fatalf("wrapped output = %q, want highlighted chunk %q", got, want)
		}
	}
}

func TestViewHighlightsCurrentSentenceWithinActiveChapter(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First sentence. Second sentence.", "Pending."})

	view.Speaking(0, 2, "First sentence. Second sentence.")
	if got := output.String(); !strings.Contains(got, "\x1b[7mFirst sentence.\x1b[0m") {
		t.Fatalf("initial output = %q, want first sentence highlighted", got)
	}
	if got := output.String(); strings.Contains(got, "\x1b[7mSecond sentence.\x1b[0m") {
		t.Fatalf("initial output = %q, second sentence must remain plain", got)
	}

	output.Reset()
	view.Progress(0, 2, 1)
	if got := output.String(); strings.Contains(got, "\x1b[7mFirst sentence.\x1b[0m") || !strings.Contains(got, "\x1b[7mSecond sentence.\x1b[0m") {
		t.Fatalf("progress output = %q, want only second sentence highlighted", got)
	}

	output.Reset()
	view.Paused(0, 2)
	if got := output.String(); !strings.Contains(got, "\x1b[7mSecond sentence.\x1b[0m") {
		t.Fatalf("paused output = %q, want second sentence highlight preserved", got)
	}

	output.Reset()
	view.Spoken(0, 2)
	if got := output.String(); strings.Contains(got, "\x1b[7m") {
		t.Fatalf("completed output = %q, completed chapter must not stay highlighted", got)
	}
}

func TestViewSentenceHighlightKeepsChapterLayoutStable(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First. Second."})
	view.Speaking(0, 1, "First. Second.")

	output.Reset()
	view.Progress(0, 1, 1)

	if got := output.String(); !strings.Contains(got, "First. \x1b[7mSecond.\x1b[0m") {
		t.Fatalf("progress output = %q, want sentence highlight without changing paragraph layout", got)
	}
}

func TestViewSentenceProgressRedrawsOnlyAfterIndexChanges(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First. Second."})
	view.Speaking(0, 1, "First. Second.")

	output.Reset()
	view.Progress(0, 1, 0)
	if got := output.String(); got != "" {
		t.Fatalf("unchanged progress output = %q, want no redraw", got)
	}

	view.Progress(0, 1, 1)
	if got := output.String(); !strings.Contains(got, "\x1b[7mSecond.\x1b[0m") {
		t.Fatalf("changed progress output = %q, want second sentence highlighted", got)
	}
}

func TestViewSeekResetsSentenceHighlightToChapterStart(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First. Second."})
	view.Speaking(0, 1, "First. Second.")
	view.Progress(0, 1, 1)

	output.Reset()
	view.Seeked(0, 1, "First. Second.", 0, false, -5*time.Second, time.Second, 10*time.Second, true)
	if got := output.String(); !strings.Contains(got, "\x1b[7mFirst.\x1b[0m") || strings.Contains(got, "\x1b[7mSecond.\x1b[0m") {
		t.Fatalf("seek output = %q, want first sentence highlighted", got)
	}
}

func TestViewSeekWithinCurrentSentenceKeepsHighlight(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First. Second."})
	view.Speaking(0, 1, "First. Second.")
	view.Progress(0, 1, 1)

	output.Reset()
	view.Seeked(0, 1, "First. Second.", 1, false, 5*time.Second, 8*time.Second, 10*time.Second, true)
	if got := output.String(); !strings.Contains(got, "\x1b[7mSecond.\x1b[0m") {
		t.Fatalf("same-sentence seek output = %q, want second sentence highlight preserved", got)
	}
}

func TestViewSeekWithinSameSentenceDoesNotRedrawUnchangedFrame(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First. Second."})
	if err := view.Speaking(0, 1, "First. Second."); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := view.Seeked(0, 1, "First. Second.", 0, true, 5*time.Second, 6*time.Second, 20*time.Second, true); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("same-sentence seek cleared and repainted unchanged chapter text")
	}
}

func TestViewChapterUpdateWritesOneFrame(t *testing.T) {
	output := &countingWriter{}
	view := New(output, true, "notes.md", "test TTS")
	view.SetChapters([]string{"First.", "Second."})
	if err := view.Speaking(0, 2, "First."); err != nil {
		t.Fatal(err)
	}
	output.writes = 0
	if err := view.Seeked(1, 2, "Second.", 0, true, 0, 10*time.Second, 20*time.Second, true); err != nil {
		t.Fatal(err)
	}
	if output.writes != 1 {
		t.Fatalf("chapter update writes = %d, want one complete frame to avoid visible clearing", output.writes)
	}
}

type countingWriter struct{ writes int }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return len(p), nil
}

func BenchmarkViewSeek(b *testing.B) {
	view := New(io.Discard, true, "notes.md", "test TTS")
	chapters := make([]string, 200)
	for i := range chapters {
		chapters[i] = strings.Repeat("章节内容用于验证方向键跳转的显示开销。", 20)
	}
	view.SetChapters(chapters)
	if err := view.Speaking(0, len(chapters), chapters[0]); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := view.Seeked(i%2, len(chapters), chapters[i%2], 0, true, 5*time.Second, time.Second, time.Hour, true); err != nil {
			b.Fatal(err)
		}
	}
}

func TestViewPadsChapterNumbersToTotalWidth(t *testing.T) {
	screen := newTestTerminalScreen()
	screen.setSize(80, 30)
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{
		"One.", "Two.", "Three.", "Four.", "Five.", "Six.",
		"Seven.", "Eight.", "Nine.", "Ten.", "Eleven.", "Twelve.",
	})

	view.Speaking(0, 12, "One.")

	want := []string{
		"[01/12] ▶ One.",
		"[02/12] · Two.",
		"[03/12] · Three.",
		"[04/12] · Four.",
		"[05/12] · Five.",
		"[06/12] · Six.",
		"[07/12] · Seven.",
		"[08/12] · Eight.",
		"[09/12] · Nine.",
		"[10/12] · Ten.",
		"[11/12] · Eleven.",
		"[12/12] · Twelve.",
	}
	if got := screen.chapterLines(); !equalStrings(got, want) {
		t.Fatalf("visible chapter lines = %#v, want %#v", got, want)
	}
}

func TestViewWrapsChapterTextAfterPlaybackIcon(t *testing.T) {
	screen := newTestTerminalScreen()
	screen.setSize(18, 8)
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{
		"abcdefghijk", "Two.", "Three.", "Four.", "Five.", "Six.",
		"Seven.", "Eight.", "Nine.", "Ten.", "Eleven.", "Twelve.",
	})

	view.Speaking(0, 12, "abcdefghijk")

	want := []string{
		"[01/12] ▶ abcdefgh",
		"          ijk",
	}
	if got := screen.visibleLines(); !equalStrings(got, want) {
		t.Fatalf("visible lines = %#v, want %#v", got, want)
	}
}

func TestViewKeepsEmojiGraphemeTogetherWhenWrapping(t *testing.T) {
	screen := newTestTerminalScreen()
	screen.setSize(12, 8)
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{
		"👨‍👨‍👧A", "Two.", "Three.", "Four.", "Five.", "Six.",
		"Seven.", "Eight.", "Nine.", "Ten.", "Eleven.", "Twelve.",
	})

	view.Speaking(0, 12, "👨‍👨‍👧A")

	want := []string{
		"[01/12] ▶ 👨‍👨‍👧",
		"          A",
	}
	if got := screen.visibleLines(); !equalStrings(got, want) {
		t.Fatalf("visible lines = %#v, want %#v", got, want)
	}
}

func TestViewKeepsPreviousChapterCompletedWhenResumingBufferedTarget(t *testing.T) {
	screen := newTestTerminalScreen()
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{"One.", "Two."})

	view.Start(2)
	view.Speaking(0, 2, "One.")
	view.Spoken(0, 2)
	view.Buffering(1, 2)
	view.Paused(1, 2)
	view.Resumed(1, 2)

	want := []string{
		"[1/2] ✓ One.",
		"[2/2] ▶ Two.",
	}
	if got := screen.chapterLines(); !equalStrings(got, want) {
		t.Fatalf("visible chapter lines = %#v, want %#v", got, want)
	}
}

func TestViewClearsBufferingWhenNextSentenceStarts(t *testing.T) {
	screen := newTestTerminalScreen()
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{"First sentence. Second sentence."})

	view.Start(1)
	view.Speaking(0, 1, "First sentence. Second sentence.")
	view.Buffering(0, 1)
	view.Progress(0, 1, 1)

	if got := strings.Join(screen.visibleLines(), "\n"); strings.Contains(got, "buffering") {
		t.Fatalf("visible output = %q, want buffering status cleared after sentence playback resumes", got)
	}
}

func TestViewRepaintsChapterListAfterTerminalResize(t *testing.T) {
	screen := newTestTerminalScreen()
	screen.setSize(80, 24)
	view := New(screen, false, "notes.md", "test TTS")
	view.SetChapters([]string{"One.", "Two.", "Three."})

	view.Start(3)
	view.Speaking(0, 3, "One.")
	screen.setSize(40, 12)
	view.Paused(0, 3)

	if screen.clearScreenCount != 1 {
		t.Fatalf("full-screen repaint count = %d, want 1 after terminal resize", screen.clearScreenCount)
	}
	want := []string{
		"[1/3] ⏸ One.",
		"[2/3] · Two.",
		"[3/3] · Three.",
	}
	if got := screen.chapterLines(); !equalStrings(got, want) {
		t.Fatalf("visible chapter lines = %#v, want %#v", got, want)
	}
}

func TestViewWritesSentenceWhenSpeakingStarts(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")

	view.Start(1)
	view.Speaking(0, 1, "Visible before speech.")

	if got := output.String(); got != "say  notes.md\nTTS  test TTS · 1 speech unit\n\nSpace Play/Pause · ←/→ Sentence · ↑/↓ Chapter\n\n[1/1] ▶ Visible before speech.\n" {
		t.Fatalf("output after Speaking() = %q", got)
	}
}

func TestViewUpdatesWrappedSentenceIconInPlace(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")
	view.width = 10
	text := "中文"

	view.Speaking(0, 1, text)
	view.Spoken(0, 1)

	want := "[1/1] ▶ " + text + "\n" +
		"\x1b[2A\r\x1b[2K[1/1] ✓ " + text + "\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestViewDoesNotEmitCursorUpdatesWithoutInteractiveControls(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")
	view.SetControls(false)

	view.Speaking(0, 1, "Plain transcript.")
	view.Paused(0, 1)
	view.Resumed(0, 1)
	view.Spoken(0, 1)

	if got, want := output.String(), "[1/1] ▶ Plain transcript.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestViewRendersPreparationOnce(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")

	if err := view.Preparing(2); err != nil {
		t.Fatalf("Preparing() error = %v", err)
	}
	if err := view.Prepared(1, 2); err != nil {
		t.Fatalf("Prepared() error = %v", err)
	}
	if err := view.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := view.Prepared(2, 2); err != nil {
		t.Fatalf("Prepared() after Start() error = %v", err)
	}

	want := "say  notes.md\nTTS  test TTS · 2 speech units\n\n" +
		"… preparing audio · 0/2 ready\n" +
		"… ready to play · 1/2 prepared\n" +
		"Space Play/Pause · ←/→ Sentence · ↑/↓ Chapter\n\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestViewAddsANSIStylesWhenColorEnabled(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, true, "notes.md", "test TTS")

	view.Start(1)

	if got := output.String(); !bytes.Contains([]byte(got), []byte("\x1b[1m")) {
		t.Fatalf("colored output = %q, want ANSI bold sequence", got)
	}
}

func TestViewEscapesTerminalControlCharactersFromUntrustedText(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "bad\x1b]52;c;title\a.txt", "voice\x1b[31m")

	view.Start(1)
	view.Speaking(0, 1, "hello\x1b[2Jworld")
	view.Failed(0, 1, errors.New("failure\nforged status"))

	got := output.String()
	if strings.ContainsAny(got, "\x1b\a") {
		t.Fatalf("output contains raw terminal control character: %q", got)
	}
	for _, escaped := range []string{"\\u001B]52;c;title\\u0007.txt", "voice\\u001B[31m", "hello\\u001B[2Jworld", "failure\\u000Aforged status"} {
		if !strings.Contains(got, escaped) {
			t.Fatalf("output = %q, want escaped text %q", got, escaped)
		}
	}
}

func TestViewReturnsOutputFailure(t *testing.T) {
	wantErr := errors.New("output closed")
	view := New(errorWriter{err: wantErr}, false, "notes.md", "test TTS")

	err := view.Speaking(0, 1, "must not be spoken")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Speaking() error = %v, want %v", err, wantErr)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type testTerminalScreen struct {
	lines            [][]rune
	row              int
	col              int
	width            int
	height           int
	clearScreenCount int
}

func newTestTerminalScreen() *testTerminalScreen {
	return &testTerminalScreen{lines: make([][]rune, 1)}
}

func (s *testTerminalScreen) TerminalSize() (int, int, error) {
	return s.width, s.height, nil
}

func (s *testTerminalScreen) setSize(width, height int) {
	s.width = width
	s.height = height
}

func (s *testTerminalScreen) Write(data []byte) (int, error) {
	for offset := 0; offset < len(data); {
		if data[offset] == '\x1b' && offset+1 < len(data) && data[offset+1] == '[' {
			consumed := s.applyCSI(data[offset+2:])
			if consumed > 0 {
				offset += consumed + 2
				continue
			}
		}
		switch data[offset] {
		case '\n':
			s.row++
			s.col = 0
			s.ensureRow()
			offset++
		case '\r':
			s.col = 0
			offset++
		default:
			r, size := utf8.DecodeRune(data[offset:])
			s.writeRune(r)
			offset += size
		}
	}
	return len(data), nil
}

func (s *testTerminalScreen) applyCSI(data []byte) int {
	command := -1
	for i, b := range data {
		if b >= '@' && b <= '~' {
			command = i
			break
		}
	}
	if command < 0 {
		return 0
	}
	parameter := string(data[:command])
	value := 1
	if parameter != "" {
		parsed, err := strconv.Atoi(parameter)
		if err != nil {
			return 0
		}
		value = parsed
	}
	switch data[command] {
	case 'A':
		s.row -= value
		if s.row < 0 {
			s.row = 0
		}
	case 'B':
		s.row += value
		s.ensureRow()
	case 'K':
		if value == 2 {
			s.lines[s.row] = nil
		}
	case 'H':
		s.row = 0
		s.col = 0
	case 'J':
		if value == 2 {
			s.lines = make([][]rune, 1)
			s.clearScreenCount++
		}
	}
	return command + 1
}

func (s *testTerminalScreen) writeRune(r rune) {
	s.ensureRow()
	for len(s.lines[s.row]) <= s.col {
		s.lines[s.row] = append(s.lines[s.row], ' ')
	}
	s.lines[s.row][s.col] = r
	s.col++
}

func (s *testTerminalScreen) ensureRow() {
	for len(s.lines) <= s.row {
		s.lines = append(s.lines, nil)
	}
}

func (s *testTerminalScreen) chapterLines() []string {
	var chapters []string
	for _, line := range s.lines {
		text := strings.TrimRight(string(line), " ")
		if strings.HasPrefix(text, "[") {
			chapters = append(chapters, text)
		}
	}
	return chapters
}

func (s *testTerminalScreen) visibleLines() []string {
	var visible []string
	for _, line := range s.lines {
		text := strings.TrimRight(string(line), " ")
		if text != "" {
			visible = append(visible, text)
		}
	}
	return visible
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
