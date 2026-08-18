package terminal

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestViewRendersPlaybackLifecycleWithoutColor(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "lesson.txt", "macOS say (system voice)")

	view.Start(2)
	view.Speaking(0, 2, "第一句。")
	view.Paused(0, 2)
	view.Seeked(0, 2, -5*time.Second, 3*time.Second, 12*time.Second, true)
	view.Resumed(0, 2)
	view.Spoken(0, 2)
	view.Buffering(1, 2)
	view.Speaking(1, 2, "Second.")
	view.Failed(1, 2, errors.New("voice unavailable"))
	view.Finish(2)

	want := "say  lesson.txt\n" +
		"TTS  macOS say (system voice) · 2 speech units\n\n" +
		"Space 播放/暂停 · ← 回退 5s · → 快进 5s\n\n" +
		"[1/2] ▶ 第一句。\n" +
		"      ⏸ paused\n" +
		"      ↶ -5s · 00:03 / 00:12\n" +
		"      ▶ resumed\n" +
		"      ✓ played\n" +
		"      … buffering speech unit 2/2\n" +
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

	if got := output.String(); got != "say  notes.md\nTTS  test TTS · 1 speech unit\n\nSpace 播放/暂停 · ← 回退 5s · → 快进 5s\n\n[1/1] ▶ Visible before speech.\n" {
		t.Fatalf("output after Speaking() = %q", got)
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
		"Space 播放/暂停 · ← 回退 5s · → 快进 5s\n\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestViewMarksIncompletePreparedDuration(t *testing.T) {
	var output bytes.Buffer
	view := New(&output, false, "notes.md", "test TTS")

	if err := view.Seeked(0, 3, 5*time.Second, 8*time.Second, 12*time.Second, false); err != nil {
		t.Fatalf("Seeked() error = %v", err)
	}

	if got, want := output.String(), "      ↷ +5s · 00:08 / 00:12+\n"; got != want {
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
