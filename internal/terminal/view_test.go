package terminal

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestViewRendersPlaybackLifecycleWithoutColor(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "lesson.txt", "macOS say (system voice)")

	view.Start(2)
	view.Speaking(0, 2, "第一句。")
	view.Spoken(0, 2)
	view.Speaking(1, 2, "Second.")
	view.Failed(1, 2, errors.New("voice unavailable"))
	view.Finish(2)

	want := "say  lesson.txt\n" +
		"TTS  macOS say (system voice) · 2 speech units\n\n" +
		"[1/2] ▶ 第一句。\n" +
		"      ✓ played\n" +
		"[2/2] ▶ Second.\n" +
		"      ✗ voice unavailable\n\n" +
		"✓ Finished 2 speech units.\n"
	if got := output.String(); got != want {
		t.Fatalf("output =\n%q\nwant =\n%q", got, want)
	}
}

func TestViewWritesSentenceWhenSpeakingStarts(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")

	view.Start(1)
	view.Speaking(0, 1, "Visible before speech.")

	if got := output.String(); got != "say  notes.md\nTTS  test TTS · 1 speech unit\n\n[1/1] ▶ Visible before speech.\n" {
		t.Fatalf("output after Speaking() = %q", got)
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
