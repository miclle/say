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
	"testing/synctest"
	"time"

	"github.com/miclle/say/internal/player"
	"github.com/miclle/say/internal/tts"
)

type functionSynthesizer func(context.Context, string, string) error

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (functionSynthesizer) Name() string      { return "test TTS" }
func (functionSynthesizer) Extension() string { return ".aiff" }
func (f functionSynthesizer) Synthesize(ctx context.Context, text, path string) error {
	return f(ctx, text, path)
}

func TestRunRapidArrowsPrepareOnlyFinalTargetAndReuseCachedReturn(t *testing.T) {
	chapters := []string{"First.", "Blocked."}
	for i := 3; i < 12; i++ {
		chapters = append(chapters, fmt.Sprintf("Skipped chapter %d.", i))
	}
	chapters = append(chapters, "Target.")
	path := writeDocument(t, "chapters.txt", strings.Join(chapters, "\n\n"))
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		input, keys := io.Pipe()
		defer input.Close()
		defer keys.Close()
		var stdout synchronizedBuffer
		var stderr bytes.Buffer
		calls := make(chan string, 32)
		blocked, canceled := make(chan struct{}, 1), make(chan string, 1)
		synthesizer := functionSynthesizer(func(ctx context.Context, text, output string) error {
			calls <- text
			if text == "Blocked." {
				if err := os.WriteFile(output, []byte("partial"), 0o600); err != nil {
					return err
				}
				blocked <- struct{}{}
				<-ctx.Done()
				canceled <- output
				return ctx.Err()
			}
			return os.WriteFile(output, []byte("complete"), 0o600)
		})
		transport := newFakeAudio(&stdout, nil)
		transport.finishAfterPlay = 1000
		deps := testDependencies(newFakeSynthesizer(), transport)
		deps.input = input
		deps.newSynthesizer = func(tts.Options) (tts.Synthesizer, error) { return synthesizer, nil }
		deps.supportsTerminal = func(any) bool { return true }
		deps.beginRaw = func(io.Reader) (func() error, error) { return func() error { return nil }, nil }
		done := make(chan int, 1)
		go func() {
			done <- runWithDependencies(ctx, []string{"--provider", "system", path}, &stdout, &stderr, deps)
		}()
		<-blocked
		synctest.Wait()
		if _, err := keys.Write([]byte(strings.Repeat("\x1b[B", 11))); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		partial := <-canceled
		synctest.Wait()
		if _, err := os.Stat(partial); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled partial file remains: %v", err)
		}
		if !strings.Contains(stdout.String(), "selecting speech unit 12/12") {
			t.Fatalf("no final text preview: %q", stdout.String())
		}
		if strings.Contains(stdout.String(), "buffering") {
			t.Fatal("buffering appeared before navigation settled")
		}
		if len(calls) != 2 {
			t.Fatalf("synthesis occurred during selection: %d calls", len(calls))
		}
		time.Sleep(199 * time.Millisecond)
		synctest.Wait()
		if len(calls) != 2 {
			t.Fatal("synthesis started before the debounce deadline")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if len(calls) != 3 {
			t.Fatalf("synthesis calls=%d, want first, canceled prefetch, and final target", len(calls))
		}
		for _, want := range []string{"First.", "Blocked.", "Target."} {
			if got := <-calls; got != want {
				t.Fatalf("synthesized %q, want %q", got, want)
			}
		}
		transport.mu.Lock()
		playedPath, playing := transport.path, transport.playing
		transport.mu.Unlock()
		if filepath.Base(playedPath) != "000012-001.aiff" || !playing {
			t.Fatalf("wrong playback target: %s", playedPath)
		}
		// Return to the first cached sentence, then reverse to the final cached
		// sentence before settling; no additional synthesis or playback occurs.
		if _, err := keys.Write([]byte(" " + strings.Repeat("\x1b[A", 11) + strings.Repeat("\x1b[B", 11))); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if len(calls) != 0 || transport.IsPlaying() {
			t.Fatal("cached paused navigation synthesized or resumed audio")
		}
		cancel()
		synctest.Wait()
		if code := <-done; code != 130 {
			t.Fatalf("exit=%d stderr=%q", code, stderr.String())
		}
		if _, err := os.Stat(filepath.Dir(partial)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary directory remains: %v", err)
		}
	})
}

func TestPreparationRemovesFailedOrInvalidAudio(t *testing.T) {
	for _, failure := range []string{"synthesis", "duration", "zero"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			synth := functionSynthesizer(func(_ context.Context, _, path string) error {
				if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
					return err
				}
				if failure == "synthesis" {
					return errors.New("synthesis failure")
				}
				return nil
			})
			source := prepareAudio(context.Background(), []string{"One."}, dir, synth, func(string) (time.Duration, error) {
				if failure == "duration" {
					return 0, errors.New("duration failure")
				}
				return 0, nil
			})
			defer source.Close()
			source.Request(player.Target{})
			result := <-source.Results()
			if result.Err == nil {
				t.Fatal("invalid audio accepted")
			}
			if _, err := os.Stat(filepath.Join(dir, "000001-001.aiff")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed output remains: %v", err)
			}
		})
	}
}
