package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/miclle/say/internal/tts"
)

func TestRunUsesDefaultsAndPrintsEveryChunkBeforeSpeech(t *testing.T) {
	path := writeDocument(t, "lesson.txt", strings.Repeat("界", 500)+"末")
	var stdout, stderr bytes.Buffer
	speaker := &observingSpeaker{output: &stdout}
	var gotVoice string
	var gotRate int
	factory := func(voice string, rate int) (tts.Speaker, error) {
		gotVoice, gotRate = voice, rate
		return speaker, nil
	}

	code := run(context.Background(), []string{"--no-color", path}, &stdout, &stderr, factory, neverColor)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	if gotVoice != "" || gotRate != 0 {
		t.Fatalf("speaker options = voice %q, rate %d; want system defaults", gotVoice, gotRate)
	}
	if len(speaker.spoken) != 2 {
		t.Fatalf("spoken chunks = %d, want 2", len(speaker.spoken))
	}
	if utf8.RuneCountInString(speaker.spoken[0]) != 500 || speaker.spoken[1] != "末" {
		t.Fatalf("spoken chunk lengths = %d, %d; want 500, 1", utf8.RuneCountInString(speaker.spoken[0]), utf8.RuneCountInString(speaker.spoken[1]))
	}
	for i, visible := range speaker.visibleBeforeSpeak {
		if !visible {
			t.Fatalf("chunk %d was not in terminal output before Speak()", i+1)
		}
	}
	if !strings.Contains(stdout.String(), "✓ Finished 2 speech units.") {
		t.Fatalf("stdout = %q, want finished summary", stdout.String())
	}
}

func TestRunPassesVoiceRateAndCustomLimit(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "123456789")
	var stdout, stderr bytes.Buffer
	speaker := &observingSpeaker{output: &stdout}
	factory := func(voice string, rate int) (tts.Speaker, error) {
		if voice != "Tingting" || rate != 210 {
			t.Fatalf("speaker options = %q, %d", voice, rate)
		}
		return speaker, nil
	}

	code := run(context.Background(), []string{"--no-color", "--voice", "Tingting", "--rate", "210", "--max-chars", "4", path}, &stdout, &stderr, factory, neverColor)
	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %q", code, stderr.String())
	}
	if got := speaker.spoken; len(got) != 3 || got[0] != "1234" || got[1] != "5678" || got[2] != "9" {
		t.Fatalf("spoken chunks = %#v, want 4-rune chunks", got)
	}
}

func TestRunRejectsUsageErrorsBeforeCreatingSpeaker(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing document", args: nil, wantErr: "Usage: say [flags] <document>"},
		{name: "zero max chars", args: []string{"--max-chars", "0", "notes.txt"}, wantErr: "max-chars must be greater than zero"},
		{name: "negative rate", args: []string{"--rate", "-1", "notes.txt"}, wantErr: "rate must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			factoryCalled := false
			factory := func(string, int) (tts.Speaker, error) {
				factoryCalled = true
				return nil, fmt.Errorf("must not be called")
			}

			code := run(context.Background(), tt.args, &stdout, &stderr, factory, neverColor)
			if code != 2 {
				t.Fatalf("run() code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("stderr = %q, want containing %q", stderr.String(), tt.wantErr)
			}
			if factoryCalled {
				t.Fatal("speaker factory was called for invalid usage")
			}
		})
	}
}

func TestRunReportsDocumentAndSpeechFailures(t *testing.T) {
	t.Run("missing document", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"missing.txt"}, &stdout, &stderr, successfulFactory(&stdout), neverColor)
		if code != 1 || !strings.Contains(stderr.String(), "open document") {
			t.Fatalf("run() = %d, stderr %q", code, stderr.String())
		}
	})

	t.Run("speech failure", func(t *testing.T) {
		path := writeDocument(t, "lesson.txt", "hello.")
		var stdout, stderr bytes.Buffer
		factory := func(string, int) (tts.Speaker, error) {
			return &observingSpeaker{output: &stdout, err: fmt.Errorf("audio device unavailable")}, nil
		}

		code := run(context.Background(), []string{"--no-color", path}, &stdout, &stderr, factory, neverColor)
		if code != 1 {
			t.Fatalf("run() code = %d, want 1", code)
		}
		if !strings.Contains(stdout.String(), "✗ audio device unavailable") || !strings.Contains(stderr.String(), "audio device unavailable") {
			t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
		}
	})
}

func TestRunReturns130WhenCanceled(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello.")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer

	code := run(ctx, []string{"--no-color", path}, &stdout, &stderr, successfulFactory(&stdout), neverColor)
	if code != 130 {
		t.Fatalf("run() code = %d, want 130; stderr = %q", code, stderr.String())
	}
}

func TestRunCancelsActiveSpeech(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello.")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	speaker := &blockingSpeaker{started: make(chan struct{})}
	factory := func(string, int) (tts.Speaker, error) { return speaker, nil }
	done := make(chan int, 1)

	go func() {
		done <- run(ctx, []string{"--no-color", path}, &stdout, &stderr, factory, neverColor)
	}()
	<-speaker.started
	cancel()

	if code := <-done; code != 130 {
		t.Fatalf("run() code = %d, want 130; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[1/1] ▶ hello.") {
		t.Fatalf("stdout = %q, want sentence rendered before cancellation", stdout.String())
	}
}

type observingSpeaker struct {
	output             *bytes.Buffer
	spoken             []string
	visibleBeforeSpeak []bool
	err                error
}

type blockingSpeaker struct {
	started chan struct{}
}

func (s *blockingSpeaker) Name() string { return "blocking TTS" }

func (s *blockingSpeaker) Speak(ctx context.Context, _ string) error {
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func (s *observingSpeaker) Name() string { return "test TTS" }

func (s *observingSpeaker) Speak(_ context.Context, text string) error {
	s.spoken = append(s.spoken, text)
	s.visibleBeforeSpeak = append(s.visibleBeforeSpeak, strings.Contains(s.output.String(), text))
	return s.err
}

func successfulFactory(output *bytes.Buffer) speakerFactory {
	return func(string, int) (tts.Speaker, error) {
		return &observingSpeaker{output: output}, nil
	}
}

func neverColor(io.Writer) bool { return false }

func writeDocument(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
