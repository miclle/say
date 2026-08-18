package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miclle/say/internal/player"
	"github.com/miclle/say/internal/tts"
)

func TestRunSynthesizesEveryBoundedChunkAndCleansTemporaryAudio(t *testing.T) {
	path := writeDocument(t, "lesson.txt", strings.Repeat("界", 500)+"末")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	transport := newFakeAudio(&stdout, synthesizer)
	deps := testDependencies(synthesizer, transport)

	code := runWithDependencies(context.Background(), []string{"--no-color", path}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if synthesizer.voice != "" || synthesizer.rate != 0 {
		t.Fatalf("synthesizer options = voice %q, rate %d; want system defaults", synthesizer.voice, synthesizer.rate)
	}
	if got := synthesizer.texts; len(got) != 2 || len([]rune(got[0])) != 500 || got[1] != "末" {
		t.Fatalf("synthesized chunks = %#v, want lengths 500 and 1", got)
	}
	for i, visible := range transport.visibleBeforePlay {
		if !visible {
			t.Fatalf("track %d text was not visible before Play()", i+1)
		}
	}
	for _, outputPath := range synthesizer.paths {
		if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary audio %q still exists; stat error = %v", outputPath, err)
		}
	}
	if !strings.Contains(stdout.String(), "… preparing 2 audio tracks") ||
		!strings.Contains(stdout.String(), "✓ Finished 2 speech units.") {
		t.Fatalf("stdout = %q, want preparation and completion", stdout.String())
	}
	if strings.Contains(stdout.String(), "Space 播放/暂停") {
		t.Fatalf("redirected output advertised unavailable controls: %q", stdout.String())
	}
}

func TestRunPassesVoiceRateAndCustomLimit(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "123456789")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	transport := newFakeAudio(&stdout, synthesizer)

	code := runWithDependencies(
		context.Background(),
		[]string{"--no-color", "--voice", "Tingting", "--rate", "210", "--max-chars", "4", path},
		&stdout,
		&stderr,
		testDependencies(synthesizer, transport),
	)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if synthesizer.voice != "Tingting" || synthesizer.rate != 210 {
		t.Fatalf("synthesizer options = %q, %d", synthesizer.voice, synthesizer.rate)
	}
	if got := synthesizer.texts; len(got) != 3 || got[0] != "1234" || got[1] != "5678" || got[2] != "9" {
		t.Fatalf("synthesized chunks = %#v, want 4-rune chunks", got)
	}
}

func TestRunEnablesInteractiveShortcutsAndRestoresTerminal(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "A paragraph long enough for seeking.")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	transport := newFakeAudio(&stdout, synthesizer)
	transport.finishAfterPlay = 2
	restored := false
	deps := testDependencies(synthesizer, transport)
	deps.input = bytes.NewBufferString(" \x1b[C\x1b[D ")
	deps.supportsTerminal = func(any) bool { return true }
	deps.beginRaw = func(io.Reader) (func() error, error) {
		return func() error {
			restored = true
			return nil
		}, nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", path}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if !restored {
		t.Fatal("terminal restore function was not called")
	}
	if !transport.hasEvent("pause") || !transport.hasEvent("seek:5s") || !transport.hasEvent("seek:0s") {
		t.Fatalf("transport events = %#v, want pause and ±5s seeks", transport.snapshot())
	}
	if !strings.Contains(stdout.String(), "Space 播放/暂停 · ← 回退 5s · → 快进 5s") {
		t.Fatalf("stdout = %q, want shortcut help", stdout.String())
	}
}

func TestRunCleansTemporaryAudioAfterSynthesisFailure(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "first paragraph\n\nsecond paragraph")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	synthesizer.failAt = 1
	transport := newFakeAudio(&stdout, synthesizer)

	code := runWithDependencies(context.Background(), []string{"--no-color", path}, &stdout, &stderr, testDependencies(synthesizer, transport))
	if code != 1 || !strings.Contains(stderr.String(), "synthesize track 2 of 2") {
		t.Fatalf("runWithDependencies() = %d, stderr = %q", code, stderr.String())
	}
	for _, outputPath := range synthesizer.paths {
		if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary audio %q still exists; stat error = %v", outputPath, err)
		}
	}
}

func TestRunRejectsUsageAndDocumentErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{name: "missing document argument", wantCode: 2, wantErr: "Usage: say [flags] <document>"},
		{name: "zero max chars", args: []string{"--max-chars", "0", "notes.txt"}, wantCode: 2, wantErr: "max-chars must be greater than zero"},
		{name: "negative rate", args: []string{"--rate", "-1", "notes.txt"}, wantCode: 2, wantErr: "rate must not be negative"},
		{name: "missing document", args: []string{"missing.txt"}, wantCode: 1, wantErr: "open document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			synthesizer := newFakeSynthesizer()
			code := runWithDependencies(context.Background(), tt.args, &stdout, &stderr, testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer)))
			if code != tt.wantCode || !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("runWithDependencies() = %d, stderr = %q; want %d containing %q", code, stderr.String(), tt.wantCode, tt.wantErr)
			}
			if len(synthesizer.texts) != 0 {
				t.Fatalf("synthesizer called for invalid input: %#v", synthesizer.texts)
			}
		})
	}
}

func TestRunReturns130WhenAlreadyCanceled(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello.")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()

	code := runWithDependencies(ctx, []string{"--no-color", path}, &stdout, &stderr, testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer)))
	if code != 130 {
		t.Fatalf("runWithDependencies() code = %d, want 130; stderr = %q", code, stderr.String())
	}
}

func testDependencies(synthesizer *fakeSynthesizer, transport *fakeAudio) dependencies {
	return dependencies{
		input: bytes.NewReader(nil),
		newSynthesizer: func(voice string, rate int) (tts.Synthesizer, error) {
			synthesizer.voice, synthesizer.rate = voice, rate
			return synthesizer, nil
		},
		newTransport:     func() (audioTransport, error) { return transport, nil },
		supportsTerminal: func(any) bool { return false },
		beginRaw: func(io.Reader) (func() error, error) {
			return nil, fmt.Errorf("must not enable raw input")
		},
	}
}

type fakeSynthesizer struct {
	voice  string
	rate   int
	texts  []string
	paths  []string
	byPath map[string]string
	failAt int
}

func newFakeSynthesizer() *fakeSynthesizer {
	return &fakeSynthesizer{byPath: make(map[string]string), failAt: -1}
}

func (s *fakeSynthesizer) Name() string { return "test TTS" }

func (s *fakeSynthesizer) Synthesize(_ context.Context, text, outputPath string) error {
	index := len(s.texts)
	s.texts = append(s.texts, text)
	s.paths = append(s.paths, outputPath)
	s.byPath[outputPath] = text
	if index == s.failAt {
		return fmt.Errorf("synthesis failed")
	}
	return os.WriteFile(outputPath, []byte("audio"), 0o600)
}

type fakeAudio struct {
	mu                sync.Mutex
	output            *bytes.Buffer
	synthesizer       *fakeSynthesizer
	path              string
	position          time.Duration
	playing           bool
	playCount         int
	finishAfterPlay   int
	events            []string
	visibleBeforePlay []bool
}

func newFakeAudio(output *bytes.Buffer, synthesizer *fakeSynthesizer) *fakeAudio {
	return &fakeAudio{output: output, synthesizer: synthesizer, finishAfterPlay: 1}
}

func (a *fakeAudio) Duration(string) (time.Duration, error) { return 20 * time.Second, nil }
func (a *fakeAudio) Load(path string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.path, a.position, a.playing = path, 0, false
	a.events = append(a.events, "load")
	return nil
}
func (a *fakeAudio) Play() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.playCount++
	a.playing = a.playCount < a.finishAfterPlay
	a.events = append(a.events, "play")
	a.visibleBeforePlay = append(a.visibleBeforePlay, strings.Contains(a.output.String(), a.synthesizer.byPath[a.path]))
	return nil
}
func (a *fakeAudio) Pause() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.playing = false
	a.events = append(a.events, "pause")
}
func (a *fakeAudio) Seek(position time.Duration) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.position = position
	a.events = append(a.events, "seek:"+position.String())
	return nil
}
func (a *fakeAudio) Position() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.position
}
func (a *fakeAudio) IsPlaying() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.playing
}
func (a *fakeAudio) Close() error { return nil }
func (a *fakeAudio) hasEvent(want string) bool {
	for _, event := range a.snapshot() {
		if event == want {
			return true
		}
	}
	return false
}
func (a *fakeAudio) snapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.events...)
}

var _ player.Transport = (*fakeAudio)(nil)

func writeDocument(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
