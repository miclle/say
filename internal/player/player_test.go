package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type demandSource struct {
	results chan AudioResult
	events  *eventLog
}

func (s *demandSource) Results() <-chan AudioResult { return s.results }
func (s *demandSource) Request(target Target) {
	s.events.add(fmt.Sprintf("request:%d:%d", target.Chapter, target.Sentence))
}
func (s *demandSource) Suspend() { s.events.add("suspend") }

type demandHarness struct {
	t         *testing.T
	source    *demandSource
	transport *fakeTransport
	commands  chan Command
	done      chan error
}

func newDemandHarness(t *testing.T, chapters []string) *demandHarness {
	t.Helper()
	transport := newFakeTransport(nil)
	source := &demandSource{results: make(chan AudioResult, 32), events: &transport.events}
	commands := make(chan Command, 32)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Play(ctx, chapters, source, transport, commands, &recordingView{events: &transport.events})
	}()
	t.Cleanup(func() { cancel(); synctest.Wait() })
	synctest.Wait()
	return &demandHarness{t, source, transport, commands, done}
}
func (h *demandHarness) ready(target Target, path string) {
	h.source.results <- AudioResult{Target: target, Audio: SentenceTrack{Path: path, Duration: time.Second}}
	synctest.Wait()
}
func (h *demandHarness) command(command Command) { h.commands <- command; synctest.Wait() }
func (h *demandHarness) settle()                 { time.Sleep(200 * time.Millisecond); synctest.Wait() }
func (h *demandHarness) finishSentence() {
	h.transport.finishCurrent()
	time.Sleep(25 * time.Millisecond)
	synctest.Wait()
}
func (h *demandHarness) assertCount(prefix string, want int) {
	h.t.Helper()
	if got := h.transport.countEvent(prefix); got != want {
		h.t.Fatalf("%s count=%d, want %d; events=%v", prefix, got, want, h.transport.snapshot())
	}
}
func (h *demandHarness) assertPath(want string, playing bool) {
	h.t.Helper()
	if path, pos := h.transport.current(); path != want || pos != 0 || h.transport.isPlaying() != playing {
		h.t.Fatalf("audio=%s at %s playing=%t; want %s playing=%t; events=%v", path, pos, h.transport.isPlaying(), want, playing, h.transport.snapshot())
	}
}

func TestPlayStartsWithFirstSentenceWhileLaterAudioIsMissing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"First. Second.", "Third."})
		h.ready(Target{}, "first.aiff")
		h.assertPath("first.aiff", true)
		h.assertCount("start:2", 1)
		h.assertCount("speaking:0:First. Second.", 1)
		h.finishSentence()
		h.assertCount("spoken:", 0)
		h.assertCount("buffering:0:2", 1)
		h.ready(Target{0, 1}, "second.aiff")
		h.assertPath("second.aiff", true)
		h.assertCount("progress:0:1", 1)
		h.finishSentence()
		h.assertCount("spoken:0", 1)
		h.assertCount("buffering:1:2", 1)
		h.command(Toggle)
		h.ready(Target{1, 0}, "third.aiff")
		h.assertPath("third.aiff", false)
		h.assertCount("paused:1", 2)
		h.command(Toggle)
		h.assertPath("third.aiff", true)
		h.finishSentence()
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
		h.assertCount("finish:2", 1)
	})
}

func TestPlayAdvancesOnlyAfterActualAudioEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"First. Second."})
		h.ready(Target{}, "first.aiff")
		h.ready(Target{0, 1}, "second.aiff")
		time.Sleep(time.Second)
		synctest.Wait()
		h.assertCount("load:", 1)
		h.finishSentence()
		h.assertPath("second.aiff", true)
		h.assertCount("buffering:", 0)
	})
}

func TestPlayInitialPauseAndInputClosure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"First."})
		h.command(Toggle)
		h.ready(Target{}, "first.aiff")
		h.assertPath("first.aiff", false)
		h.command(Toggle)
		close(h.commands)
		h.finishSentence()
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPlayRejectsInvalidInputsAndAudio(t *testing.T) {
	for _, tt := range []struct {
		name     string
		chapters []string
		result   AudioResult
		want     string
	}{
		{"empty", nil, AudioResult{}, "at least one chapter"},
		{"empty chapter", []string{" "}, AudioResult{}, "no sentences"},
		{"bad target", []string{"One."}, AudioResult{Target: Target{1, 0}}, "invalid audio target"},
		{"missing path", []string{"One."}, AudioResult{Audio: SentenceTrack{Duration: time.Second}}, "invalid sentence audio"},
		{"missing duration", []string{"One."}, AudioResult{Audio: SentenceTrack{Path: "one"}}, "invalid sentence audio"},
		{"failure", []string{"One."}, AudioResult{Err: errors.New("synthesis failed")}, "synthesis failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newDemandHarness(t, tt.chapters)
				h.source.results <- tt.result
				synctest.Wait()
				if err := <-h.done; err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error=%v want %q", err, tt.want)
				}
			})
		})
	}
	transport := newFakeTransport(nil)
	view := &recordingView{events: &transport.events}
	if err := Play(context.Background(), []string{"One."}, nil, transport, nil, view); err == nil {
		t.Fatal("accepted nil source")
	}
	if err := Play(context.Background(), []string{"One."}, &demandSource{}, transport, nil, view); err == nil {
		t.Fatal("accepted nil results")
	}
}

func TestPlayClosedSourceAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One."})
		close(h.source.results)
		synctest.Wait()
		if err := <-h.done; err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("error=%v", err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Play(ctx, nil, nil, nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestPlayIgnoresObsoleteErrorDuringNavigation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newDemandHarness(t, []string{"One.", "Two."})
		h.ready(Target{}, "one.aiff")
		h.command(NextChapter)
		h.source.results <- AudioResult{Target: Target{}, Err: errors.New("obsolete")}
		synctest.Wait()
		h.settle()
		h.ready(Target{1, 0}, "two.aiff")
		h.assertPath("two.aiff", true)
	})
}

type fakeTransport struct {
	mu        sync.Mutex
	events    eventLog
	durations map[string]time.Duration
	path      string
	position  time.Duration
	playing   bool
}

func newFakeTransport(durations map[string]time.Duration) *fakeTransport {
	return &fakeTransport{durations: durations, events: eventLog{changed: make(chan struct{}, 1)}}
}

func (t *fakeTransport) Load(path string) error {
	t.mu.Lock()
	t.path, t.position, t.playing = path, 0, false
	t.mu.Unlock()
	t.events.add("load:" + path)
	return nil
}

func (t *fakeTransport) Play() error {
	t.mu.Lock()
	t.playing = true
	path := t.path
	t.mu.Unlock()
	t.events.add("play:" + path)
	return nil
}

func (t *fakeTransport) Pause() {
	t.mu.Lock()
	t.playing = false
	t.mu.Unlock()
	t.events.add("pause")
}

func (t *fakeTransport) Seek(position time.Duration) error {
	t.mu.Lock()
	t.position = position
	path := t.path
	t.mu.Unlock()
	t.events.add(fmt.Sprintf("seek:%s:%s", path, position))
	return nil
}

func (t *fakeTransport) Position() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.position
}

func (t *fakeTransport) IsPlaying() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.playing
}

func (t *fakeTransport) finishCurrent() {
	t.mu.Lock()
	t.position = t.durations[t.path]
	t.playing = false
	t.mu.Unlock()
}

func (t *fakeTransport) setPosition(position time.Duration) {
	t.mu.Lock()
	t.position = position
	t.mu.Unlock()
}

func (t *fakeTransport) isPlaying() bool    { return t.IsPlaying() }
func (t *fakeTransport) snapshot() []string { return t.events.snapshot() }
func (t *fakeTransport) current() (string, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.path, t.position
}
func (t *fakeTransport) waitForEvent(tb *testing.T, event string) {
	tb.Helper()
	t.events.waitFor(tb, event)
}
func (t *fakeTransport) countEvent(prefix string) int {
	count := 0
	for _, event := range t.snapshot() {
		if strings.HasPrefix(event, prefix) {
			count++
		}
	}
	return count
}

type eventLog struct {
	mu      sync.Mutex
	items   []string
	changed chan struct{}
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	l.items = append(l.items, event)
	l.mu.Unlock()
	select {
	case l.changed <- struct{}{}:
	default:
	}
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

func (l *eventLog) waitFor(t *testing.T, event string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		for _, item := range l.snapshot() {
			if item == event {
				return
			}
		}
		select {
		case <-l.changed:
		case <-deadline.C:
			t.Fatalf("timed out waiting for event %q; events = %#v", event, l.snapshot())
		}
	}
}

type recordingView struct{ events *eventLog }

func (v *recordingView) Prepared(prepared, total int) error {
	v.events.add(fmt.Sprintf("prepared:%d:%d", prepared, total))
	return nil
}
func (v *recordingView) Start(total int) error {
	v.events.add(fmt.Sprintf("start:%d", total))
	return nil
}
func (v *recordingView) Speaking(index, _ int, text string) error {
	v.events.add(fmt.Sprintf("speaking:%d:%s", index, text))
	return nil
}
func (v *recordingView) Progress(index, _ int, sentence int) error {
	v.events.add(fmt.Sprintf("progress:%d:%d", index, sentence))
	return nil
}
func (v *recordingView) Spoken(index, _ int) error {
	v.events.add(fmt.Sprintf("spoken:%d", index))
	return nil
}
func (v *recordingView) Paused(index, _ int) error {
	v.events.add(fmt.Sprintf("paused:%d", index))
	return nil
}
func (v *recordingView) Resumed(index, _ int) error {
	v.events.add(fmt.Sprintf("resumed:%d", index))
	return nil
}
func (v *recordingView) Buffering(index, total int) error {
	v.events.add(fmt.Sprintf("buffering:%d:%d", index, total))
	return nil
}
func (v *recordingView) Seeked(index, _ int, _ string, sentence int, _ bool, delta, position, duration time.Duration, complete bool) error {
	v.events.add(fmt.Sprintf("seeked:%d:%d:%s:%s:%s:%t", index, sentence, delta, position, duration, complete))
	return nil
}
func (v *recordingView) Failed(index, _ int, err error) error {
	v.events.add(fmt.Sprintf("failed:%d:%s", index, err))
	return nil
}
func (v *recordingView) Finish(total int) error {
	v.events.add(fmt.Sprintf("finish:%d", total))
	return nil
}

func waitResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for playback result")
		return nil
	}
}

func (v *recordingView) Selected(index, _ int, _ string, sentence int) error {
	v.events.add(fmt.Sprintf("selected:%d:%d", index, sentence))
	return nil
}
