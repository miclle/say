package player_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/miclle/say/internal/player"
	"github.com/miclle/say/internal/terminal"
)

// Control only audio availability and completion; exercise the real player
// and terminal view together so stale presentation state remains observable.
type viewAudioSource struct{ results chan player.AudioResult }

func (s *viewAudioSource) Results() <-chan player.AudioResult { return s.results }
func (*viewAudioSource) Request(player.Target)                {}
func (*viewAudioSource) Suspend()                             {}

type viewTransport struct {
	mu      sync.Mutex
	playing bool
}

func (*viewTransport) Load(string) error        { return nil }
func (p *viewTransport) Play() error            { p.mu.Lock(); defer p.mu.Unlock(); p.playing = true; return nil }
func (p *viewTransport) Pause()                 { p.mu.Lock(); defer p.mu.Unlock(); p.playing = false }
func (*viewTransport) Seek(time.Duration) error { return nil }
func (*viewTransport) Position() time.Duration  { return 0 }
func (p *viewTransport) IsPlaying() bool        { p.mu.Lock(); defer p.mu.Unlock(); return p.playing }

type viewBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *viewBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *viewBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
func (b *viewBuffer) Reset()         { b.mu.Lock(); defer b.mu.Unlock(); b.buffer.Reset() }

func TestBufferedChapterPauseResumeDoesNotInheritCompletion(t *testing.T) {
	for _, toggles := range []int{1, 2} {
		name := "pause"
		if toggles == 2 {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				source := &viewAudioSource{make(chan player.AudioResult, 1)}
				transport := &viewTransport{}
				commands := make(chan player.Command, 1)
				var output viewBuffer
				view := terminal.New(&output, false, "fixture", "test")
				chapters := []string{"One.", "Two. Three."}
				view.SetChapters(chapters)
				done := make(chan error, 1)
				go func() { done <- player.Play(ctx, chapters, source, transport, commands, view) }()
				source.results <- player.AudioResult{Target: player.Target{}, Audio: player.SentenceTrack{Path: "one", Duration: time.Second}}
				synctest.Wait()
				transport.Pause()
				time.Sleep(25 * time.Millisecond)
				synctest.Wait()
				for range toggles {
					output.Reset()
					commands <- player.Toggle
					synctest.Wait()
				}
				icon := "⏸"
				if toggles == 2 {
					icon = "▶"
				}
				frame := output.String()
				if !strings.Contains(frame, "[1/2] ✓ One.") ||
					!strings.Contains(frame, "[2/2] "+icon+" Two. Three.") ||
					!strings.Contains(frame, "buffering speech unit 2/2") {
					t.Errorf("buffered target inherited the previous chapter's completion: %q", frame)
				}
				source.results <- player.AudioResult{Target: player.Target{Chapter: 1}, Audio: player.SentenceTrack{Path: "two", Duration: time.Second}}
				synctest.Wait()
				if transport.IsPlaying() != (toggles == 2) {
					t.Error("audio arrival changed pause intent")
				}
				cancel()
				synctest.Wait()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("exit error=%v", err)
				}
			})
		})
	}
}
