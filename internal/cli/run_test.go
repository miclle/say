package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/miclle/say/internal/document"
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
	if synthesizer.options.Provider != tts.ProviderSystem || synthesizer.options.Voice != "" || synthesizer.options.Rate != 0 {
		t.Fatalf("synthesizer options = %#v; want system defaults", synthesizer.options)
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
	if !strings.Contains(stdout.String(), "… preparing audio · 0/2 ready") ||
		!strings.Contains(stdout.String(), "… ready to play · 1/2 prepared") ||
		!strings.Contains(stdout.String(), "✓ Finished 2 speech units.") {
		t.Fatalf("stdout = %q, want preparation and completion", stdout.String())
	}
	if strings.Contains(stdout.String(), "Space Play/Pause") {
		t.Fatalf("redirected output advertised unavailable controls: %q", stdout.String())
	}
}

func TestPrepareTracksSynthesizesSentencesWithoutSplittingLogicalChapter(t *testing.T) {
	tempDir := t.TempDir()
	synthesizer := newFakeSynthesizer()
	durations := []time.Duration{3 * time.Second, 7 * time.Second}
	readDuration := func(path string) (time.Duration, error) {
		for index, synthesizedPath := range synthesizer.paths {
			if path == synthesizedPath {
				return durations[index], nil
			}
		}
		return 0, fmt.Errorf("unknown synthesized path %q", path)
	}

	results, done := prepareTracks(
		context.Background(),
		[]string{"First sentence. Second sentence."},
		tempDir,
		synthesizer,
		readDuration,
	)
	var prepared []player.TrackResult
	for result := range results {
		prepared = append(prepared, result)
	}
	<-done

	if got := len(prepared); got != 2 {
		t.Fatalf("track update count = %d, want one update per sentence", got)
	}
	for _, result := range prepared {
		if result.Err != nil {
			t.Fatalf("prepareTracks() error = %v", result.Err)
		}
	}
	first, result := prepared[0].Track, prepared[1].Track
	if got, want := synthesizer.texts, []string{"First sentence.", "Second sentence."}; !slices.Equal(got, want) {
		t.Fatalf("synthesized texts = %#v, want %#v", got, want)
	}
	if len(first.Sentences) != 1 || first.Complete {
		t.Fatalf("first track update = %#v, want one playable incomplete sentence", first)
	}
	if result.Text != "First sentence. Second sentence." {
		t.Fatalf("logical track text = %q", result.Text)
	}
	if got := len(result.Sentences); got != 2 {
		t.Fatalf("sentence audio count = %d, want 2", got)
	}
	if got, want := filepath.Base(result.Sentences[0].Path), "000001-001.aiff"; got != want {
		t.Fatalf("first sentence path = %q, want %q", got, want)
	}
	if got, want := filepath.Base(result.Sentences[1].Path), "000001-002.aiff"; got != want {
		t.Fatalf("second sentence path = %q, want %q", got, want)
	}
	if result.Duration != 10*time.Second || !result.Complete {
		t.Fatalf("final logical track = %#v, want complete 10s track", result)
	}
}

func TestRunReadsWebSourceBeforePlayback(t *testing.T) {
	const source = "https://example.com/articles/readable"
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	transport := newFakeAudio(&stdout, synthesizer)
	deps := testDependencies(synthesizer, transport)
	deps.readDocument = func(ctx context.Context, gotSource string, _ document.ProgressFunc) (string, string, error) {
		if err := ctx.Err(); err != nil {
			t.Fatalf("document reader context error = %v", err)
		}
		if gotSource != source {
			t.Fatalf("document reader source = %q, want %q", gotSource, source)
		}
		return "Readable article", "first paragraph\n\nsecond paragraph", nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", source}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := synthesizer.texts, []string{"first paragraph", "second paragraph"}; !slices.Equal(got, want) {
		t.Fatalf("synthesized texts = %#v, want %#v", got, want)
	}
	if !strings.Contains(stdout.String(), "say  Readable article") {
		t.Fatalf("stdout = %q, want extracted article title", stdout.String())
	}
}

func TestRunStopsBeforeTTSWhenWebSourceFails(t *testing.T) {
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	deps := testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer))
	deps.readDocument = func(context.Context, string, document.ProgressFunc) (string, string, error) {
		return "", "", fmt.Errorf("fetch web page: HTTP 503 Service Unavailable")
	}

	code := runWithDependencies(context.Background(), []string{"https://example.com/article"}, &stdout, &stderr, deps)
	if code != 1 || !strings.Contains(stderr.String(), "HTTP 503 Service Unavailable") {
		t.Fatalf("runWithDependencies() = %d, stderr = %q; want source failure", code, stderr.String())
	}
	if synthesizer.options != (tts.Options{}) || len(synthesizer.texts) != 0 {
		t.Fatalf("synthesizer initialized after source failure: options=%#v texts=%#v", synthesizer.options, synthesizer.texts)
	}
}

func TestRunShowsSourceLoadingStagesOnTerminal(t *testing.T) {
	const source = "https://example.com/articles/loading"
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	deps := testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer))
	deps.supportsTerminal = func(value any) bool { return value == &stdout }
	deps.readDocument = func(ctx context.Context, gotSource string, progress document.ProgressFunc) (string, string, error) {
		if err := ctx.Err(); err != nil {
			t.Fatalf("document reader context error = %v", err)
		}
		if gotSource != source {
			t.Fatalf("document reader source = %q, want %q", gotSource, source)
		}
		progress(document.StageReadingWebPage)
		progress(document.StageExtractingWebPage)
		return "Loading article", "readable paragraph", nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", source}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	reading := strings.Index(output, "⠋ Reading webpage…")
	extracting := strings.Index(output, "Extracting webpage content…")
	header := strings.Index(output, "say  Loading article")
	if reading < 0 || extracting <= reading || header <= extracting {
		t.Fatalf("stdout = %q, want ordered reading, extraction, and playback UI", output)
	}
	if clear := strings.LastIndex(output[:header], "\r\x1b[2K"); clear < 0 {
		t.Fatalf("stdout before header = %q, want cleared loading row", output[:header])
	}
}

func TestRunHidesSourceLoadingWhenOutputIsRedirected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	deps := testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer))
	deps.readDocument = func(context.Context, string, document.ProgressFunc) (string, string, error) {
		return "Redirected article", "readable paragraph", nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", "https://example.com/article"}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if output := stdout.String(); strings.ContainsAny(output, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") ||
		strings.Contains(output, "Reading webpage") || strings.Contains(output, "Extracting webpage") || strings.Contains(output, "\x1b[2K") {
		t.Fatalf("redirected stdout = %q, want no loading UI", output)
	}
}

func TestRunCancelsSourceLoadingAndClearsRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	deps := testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer))
	deps.supportsTerminal = func(value any) bool { return value == &stdout }
	started := make(chan struct{})
	deps.readDocument = func(ctx context.Context, _ string, progress document.ProgressFunc) (string, string, error) {
		progress(document.StageReadingWebPage)
		close(started)
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	done := make(chan int, 1)

	go func() {
		done <- runWithDependencies(ctx, []string{"--no-color", "https://example.com/article"}, &stdout, &stderr, deps)
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("document reader did not start")
	}
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("runWithDependencies() code = %d, stderr = %q; want 130", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWithDependencies() did not stop after cancellation")
	}
	if !strings.HasSuffix(stdout.String(), "\r\x1b[2K") {
		t.Fatalf("stdout = %q, want loading row cleared", stdout.String())
	}
	if synthesizer.options != (tts.Options{}) {
		t.Fatalf("synthesizer initialized after source cancellation: %#v", synthesizer.options)
	}
}

func TestReadDocumentWithLoadingPrefersCancellationOverLateSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	read := func(context.Context, string, document.ProgressFunc) (string, string, error) {
		cancel()
		return "late title", "late text", nil
	}

	name, text, err := readDocumentWithLoading(ctx, "notes.txt", io.Discard, false, false, read)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readDocumentWithLoading() = %q, %q, %v; want context.Canceled", name, text, err)
	}
}

func TestRunStartsFirstSentenceBeforeSecondSentenceFinishesSynthesis(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "First sentence. Second sentence.")
	var stdout, stderr bytes.Buffer
	synthesizer := newBlockingSecondSynthesizer()
	transport := newFakeAudio(&stdout, nil)
	transport.playStarted = make(chan struct{})
	deps := dependencies{
		input:        bytes.NewReader(nil),
		readDocument: document.ReadSourceWithProgress,
		newSynthesizer: func(tts.Options) (tts.Synthesizer, error) {
			return synthesizer, nil
		},
		newTransport:     func() (audioTransport, error) { return transport, nil },
		readDuration:     func(string) (time.Duration, error) { return 20 * time.Second, nil },
		supportsTerminal: func(any) bool { return false },
		beginRaw: func(io.Reader) (func() error, error) {
			return nil, fmt.Errorf("must not enable raw input")
		},
	}
	done := make(chan int, 1)

	go func() {
		done <- runWithDependencies(context.Background(), []string{"--no-color", path}, &stdout, &stderr, deps)
	}()
	select {
	case <-synthesizer.secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second synthesis did not start")
	}

	select {
	case <-transport.playStarted:
	case <-time.After(300 * time.Millisecond):
		close(synthesizer.releaseSecond)
		<-done
		t.Fatal("first sentence did not start before second sentence synthesis completed")
	}
	close(synthesizer.releaseSecond)
	if code := <-done; code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
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
	if synthesizer.options.Voice != "Tingting" || synthesizer.options.Rate != 210 {
		t.Fatalf("synthesizer options = %#v", synthesizer.options)
	}
	if got := synthesizer.texts; len(got) != 3 || got[0] != "1234" || got[1] != "5678" || got[2] != "9" {
		t.Fatalf("synthesized chunks = %#v, want 4-rune chunks", got)
	}
}

func TestRunPassesEdgeOptionsAndUsesMP3Tracks(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	synthesizer.extension = ".mp3"
	transport := newFakeAudio(&stdout, synthesizer)

	code := runWithDependencies(
		context.Background(),
		[]string{"--no-color", "--provider", "edge", "--voice", "en-US-AriaNeural", "--speed", "1.25", path},
		&stdout,
		&stderr,
		testDependencies(synthesizer, transport),
	)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := synthesizer.options, (tts.Options{Provider: tts.ProviderEdge, Voice: "en-US-AriaNeural", Speed: 1.25}); got != want {
		t.Fatalf("synthesizer options = %#v, want %#v", got, want)
	}
	if len(synthesizer.paths) != 1 || filepath.Ext(synthesizer.paths[0]) != ".mp3" {
		t.Fatalf("synthesized paths = %#v, want one MP3 path", synthesizer.paths)
	}
}

func TestRunUsesTUISelectedProviderWhenFlagIsOmitted(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	synthesizer.extension = ".mp3"
	transport := newFakeAudio(&stdout, synthesizer)
	deps := testDependencies(synthesizer, transport)
	deps.supportsTerminal = func(any) bool { return true }
	selectionCalls := 0
	deps.selectProvider = func(context.Context, io.Reader, io.Writer) (tts.Provider, error) {
		selectionCalls++
		return tts.ProviderEdge, nil
	}
	rawCalls, restoreCalls := 0, 0
	deps.beginRaw = func(io.Reader) (func() error, error) {
		rawCalls++
		return func() error {
			restoreCalls++
			return nil
		}, nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", "--speed", "1.25", path}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if selectionCalls != 1 {
		t.Fatalf("provider selector calls = %d, want 1", selectionCalls)
	}
	if got, want := synthesizer.options, (tts.Options{Provider: tts.ProviderEdge, Speed: 1.25}); got != want {
		t.Fatalf("synthesizer options = %#v, want %#v", got, want)
	}
	if len(synthesizer.paths) != 1 || filepath.Ext(synthesizer.paths[0]) != ".mp3" {
		t.Fatalf("synthesized paths = %#v, want one MP3 path", synthesizer.paths)
	}
	if rawCalls != 2 || restoreCalls != 2 {
		t.Fatalf("raw/restore calls = %d/%d, want selector and playback restoration", rawCalls, restoreCalls)
	}
}

func TestTTSProviderSelectorFitsStandardTerminalWidth(t *testing.T) {
	var output bytes.Buffer
	selected, err := selectTTSProvider(context.Background(), bytes.NewBufferString("\x1b[B\r"), &output)
	if err != nil {
		t.Fatalf("selectTTSProvider() error = %v", err)
	}
	if selected != tts.ProviderEdge {
		t.Fatalf("selectTTSProvider() = %q, want %q", selected, tts.ProviderEdge)
	}

	frames := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\r\x1b[2K")
	for index, frame := range frames {
		if columns := utf8.RuneCountInString(frame); columns > 80 {
			t.Fatalf("selection frame %d uses %d columns, want at most 80: %q", index+1, columns, frame)
		}
	}
}

func TestRunExplicitProviderSkipsTUISelection(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	synthesizer.extension = ".mp3"
	transport := newFakeAudio(&stdout, synthesizer)
	deps := testDependencies(synthesizer, transport)
	deps.supportsTerminal = func(any) bool { return true }
	deps.selectProvider = func(context.Context, io.Reader, io.Writer) (tts.Provider, error) {
		t.Fatal("provider selector called for explicit --provider")
		return "", nil
	}
	deps.beginRaw = func(io.Reader) (func() error, error) {
		return func() error { return nil }, nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", "--provider", "edge", path}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if synthesizer.options.Provider != tts.ProviderEdge {
		t.Fatalf("synthesizer provider = %q, want edge", synthesizer.options.Provider)
	}
}

func TestRunRestoresTerminalWhenProviderSelectionIsCanceled(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	deps := testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer))
	deps.supportsTerminal = func(any) bool { return true }
	deps.selectProvider = func(context.Context, io.Reader, io.Writer) (tts.Provider, error) {
		return "", context.Canceled
	}
	restored := false
	deps.beginRaw = func(io.Reader) (func() error, error) {
		return func() error {
			restored = true
			return nil
		}, nil
	}

	code := runWithDependencies(context.Background(), []string{"--no-color", path}, &stdout, &stderr, deps)
	if code != 130 || !strings.Contains(stderr.String(), "provider selection interrupted") {
		t.Fatalf("runWithDependencies() = %d, stderr = %q; want interrupted selection", code, stderr.String())
	}
	if !restored {
		t.Fatal("terminal was not restored after provider selection cancellation")
	}
	if synthesizer.options != (tts.Options{}) {
		t.Fatalf("synthesizer initialized after canceled selection: %#v", synthesizer.options)
	}
}

func TestRunRejectsProviderSpecificFlagConflicts(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello")
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown provider", args: []string{"--provider", "azure", path}, want: `provider must be "system" or "edge"`},
		{name: "rate with edge", args: []string{"--provider", "edge", "--rate", "210", path}, want: "rate is only supported by the system provider"},
		{name: "speed with system", args: []string{"--speed", "1.25", path}, want: "speed is only supported by the edge provider"},
		{name: "edge speed below range", args: []string{"--provider", "edge", "--speed", "0.49", path}, want: "speed must be between 0.5 and 2.0"},
		{name: "edge speed is not a number", args: []string{"--provider", "edge", "--speed", "NaN", path}, want: "speed must be between 0.5 and 2.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			synthesizer := newFakeSynthesizer()
			code := runWithDependencies(context.Background(), tt.args, &stdout, &stderr, testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer)))
			if code != 2 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("runWithDependencies() = %d, stderr = %q; want 2 containing %q", code, stderr.String(), tt.want)
			}
			if synthesizer.options != (tts.Options{}) {
				t.Fatalf("synthesizer initialized for invalid options: %#v", synthesizer.options)
			}
		})
	}
}

func TestRunEnablesInteractiveShortcutsAndRestoresTerminal(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "A paragraph long enough for seeking.")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	transport := newFakeAudio(&stdout, synthesizer)
	transport.finishAfterPlay = 2
	transport.playStarted = make(chan struct{})
	restored := false
	deps := testDependencies(synthesizer, transport)
	deps.input = &gatedReader{
		gate:   transport.playStarted,
		reader: bytes.NewBufferString(" \x1b[C\x1b[D "),
	}
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
	if !strings.Contains(stdout.String(), "Space Play/Pause · ← Back 5s · → Forward 5s") {
		t.Fatalf("stdout = %q, want shortcut help", stdout.String())
	}
}

func TestRunRendersContiguousInteractiveChapterListBeforePlayback(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "first paragraph\n\nsecond paragraph\n\nthird paragraph")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	transport := newFakeAudio(&stdout, synthesizer)
	deps := testDependencies(synthesizer, transport)
	deps.input = bytes.NewReader(nil)
	deps.supportsTerminal = func(any) bool { return true }
	deps.beginRaw = func(io.Reader) (func() error, error) {
		return func() error { return nil }, nil
	}

	code := runWithDependencies(context.Background(), []string{"--provider", "system", "--no-color", path}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	firstFrame := transport.firstOutputBeforePlay()
	for _, line := range []string{
		"[1/3] ▶ first paragraph",
		"[2/3] · second paragraph",
		"[3/3] · third paragraph",
	} {
		if !strings.Contains(firstFrame, line) {
			t.Fatalf("first playback frame = %q, want contiguous chapter line %q", firstFrame, line)
		}
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

func TestRunTreatsProviderTimeoutAsPlaybackFailure(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "hello")
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	synthesizer.failAt = 0
	synthesizer.failure = context.DeadlineExceeded

	code := runWithDependencies(context.Background(), []string{"--no-color", path}, &stdout, &stderr, testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer)))
	if code != 1 || !strings.Contains(stderr.String(), "playback failed") || strings.Contains(stderr.String(), "playback interrupted") {
		t.Fatalf("runWithDependencies() = %d, stderr = %q; want provider failure with exit code 1", code, stderr.String())
	}
}

func TestRunCancellationStopsPreparationBeforeCleanupAndRestoresTerminal(t *testing.T) {
	path := writeDocument(t, "lesson.txt", "first paragraph\n\nsecond paragraph")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	synthesizer := newCancellationSynthesizer()
	transport := newFakeAudio(&stdout, nil)
	restored := false
	deps := dependencies{
		input:        bytes.NewReader(nil),
		readDocument: document.ReadSourceWithProgress,
		newSynthesizer: func(tts.Options) (tts.Synthesizer, error) {
			return synthesizer, nil
		},
		newTransport: func() (audioTransport, error) { return transport, nil },
		readDuration: func(string) (time.Duration, error) {
			return 20 * time.Second, nil
		},
		supportsTerminal: func(any) bool { return true },
		selectProvider: func(context.Context, io.Reader, io.Writer) (tts.Provider, error) {
			return tts.ProviderSystem, nil
		},
		beginRaw: func(io.Reader) (func() error, error) {
			return func() error {
				restored = true
				return nil
			}, nil
		},
	}
	done := make(chan int, 1)

	go func() {
		done <- runWithDependencies(ctx, []string{"--no-color", path}, &stdout, &stderr, deps)
	}()
	select {
	case <-synthesizer.secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second synthesis did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runWithDependencies() did not stop after cancellation")
	}
	select {
	case <-synthesizer.exited:
	default:
		t.Fatal("run returned before the blocked synthesizer exited")
	}
	if !synthesizer.tempPresentWhenCanceled() {
		t.Fatal("temporary audio was removed before the synthesizer exited")
	}
	if firstPath := synthesizer.firstOutputPath(); firstPath == "" {
		t.Fatal("first output path was not recorded")
	} else if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary audio %q still exists; stat error = %v", firstPath, err)
	}
	if !restored {
		t.Fatal("terminal restore function was not called")
	}
}

func TestRunRejectsUsageAndDocumentErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{name: "missing source argument", wantCode: 2, wantErr: "Usage: say [flags] <document-or-url>"},
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

func TestRunHelpDescribesInteractiveProviderSelection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	synthesizer := newFakeSynthesizer()
	code := runWithDependencies(context.Background(), []string{"--help"}, &stdout, &stderr, testDependencies(synthesizer, newFakeAudio(&stdout, synthesizer)))
	if code != 0 {
		t.Fatalf("runWithDependencies() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "interactive: choose; non-interactive: system") ||
		!strings.Contains(stderr.String(), "local UTF-8 document or HTTP(S) web article") ||
		strings.Contains(stderr.String(), `provider string\n\tTTS provider: system or edge (default "system")`) {
		t.Fatalf("help output = %q, want source and provider-selection guidance", stderr.String())
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
		input:        bytes.NewReader(nil),
		readDocument: document.ReadSourceWithProgress,
		newSynthesizer: func(options tts.Options) (tts.Synthesizer, error) {
			synthesizer.options = options
			return synthesizer, nil
		},
		newTransport:     func() (audioTransport, error) { return transport, nil },
		readDuration:     func(string) (time.Duration, error) { return 20 * time.Second, nil },
		supportsTerminal: func(any) bool { return false },
		selectProvider: func(context.Context, io.Reader, io.Writer) (tts.Provider, error) {
			return tts.ProviderSystem, nil
		},
		beginRaw: func(io.Reader) (func() error, error) {
			return nil, fmt.Errorf("must not enable raw input")
		},
	}
}

type fakeSynthesizer struct {
	mu        sync.Mutex
	options   tts.Options
	extension string
	texts     []string
	paths     []string
	byPath    map[string]string
	failAt    int
	failure   error
}

type blockingSecondSynthesizer struct {
	mu            sync.Mutex
	calls         int
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

type cancellationSynthesizer struct {
	mu                  sync.Mutex
	calls               int
	firstPath           string
	tempPresentOnCancel bool
	secondStarted       chan struct{}
	exited              chan struct{}
}

func newCancellationSynthesizer() *cancellationSynthesizer {
	return &cancellationSynthesizer{
		secondStarted: make(chan struct{}),
		exited:        make(chan struct{}),
	}
}

func (s *cancellationSynthesizer) Name() string      { return "canceling test TTS" }
func (s *cancellationSynthesizer) Extension() string { return ".aiff" }

func (s *cancellationSynthesizer) Synthesize(ctx context.Context, text, outputPath string) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	if call == 1 {
		s.firstPath = outputPath
	}
	s.mu.Unlock()
	if call == 1 {
		return os.WriteFile(outputPath, []byte(text), 0o600)
	}
	close(s.secondStarted)
	<-ctx.Done()
	_, err := os.Stat(s.firstOutputPath())
	s.mu.Lock()
	s.tempPresentOnCancel = err == nil
	s.mu.Unlock()
	close(s.exited)
	return ctx.Err()
}

func (s *cancellationSynthesizer) firstOutputPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstPath
}

func (s *cancellationSynthesizer) tempPresentWhenCanceled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tempPresentOnCancel
}

func newBlockingSecondSynthesizer() *blockingSecondSynthesizer {
	return &blockingSecondSynthesizer{
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
}

func (s *blockingSecondSynthesizer) Name() string      { return "blocking test TTS" }
func (s *blockingSecondSynthesizer) Extension() string { return ".aiff" }

func (s *blockingSecondSynthesizer) Synthesize(ctx context.Context, text, outputPath string) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 2 {
		close(s.secondStarted)
		select {
		case <-s.releaseSecond:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return os.WriteFile(outputPath, []byte(text), 0o600)
}

func newFakeSynthesizer() *fakeSynthesizer {
	return &fakeSynthesizer{extension: ".aiff", byPath: make(map[string]string), failAt: -1}
}

func (s *fakeSynthesizer) Name() string      { return "test TTS" }
func (s *fakeSynthesizer) Extension() string { return s.extension }

func (s *fakeSynthesizer) Synthesize(_ context.Context, text, outputPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := len(s.texts)
	s.texts = append(s.texts, text)
	s.paths = append(s.paths, outputPath)
	s.byPath[outputPath] = text
	if index == s.failAt {
		if s.failure != nil {
			return s.failure
		}
		return fmt.Errorf("synthesis failed")
	}
	return os.WriteFile(outputPath, []byte("audio"), 0o600)
}

func (s *fakeSynthesizer) textForPath(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byPath[path]
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
	outputBeforePlay  []string
	playStarted       chan struct{}
	playStartedOnce   sync.Once
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
	a.outputBeforePlay = append(a.outputBeforePlay, a.output.String())
	if a.synthesizer != nil {
		a.visibleBeforePlay = append(a.visibleBeforePlay, strings.Contains(a.output.String(), a.synthesizer.textForPath(a.path)))
	}
	if a.playStarted != nil {
		a.playStartedOnce.Do(func() { close(a.playStarted) })
	}
	return nil
}

func (a *fakeAudio) firstOutputBeforePlay() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.outputBeforePlay) == 0 {
		return ""
	}
	return a.outputBeforePlay[0]
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

type gatedReader struct {
	gate   <-chan struct{}
	reader io.Reader
	once   sync.Once
}

func (r *gatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { <-r.gate })
	return r.reader.Read(p)
}

func writeDocument(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
