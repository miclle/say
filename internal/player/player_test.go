package player

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPlayStartsWhenFirstStreamedTrackIsReady(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- Play(ctx, 2, results, transport, nil, view)
	}()
	results <- trackResult("first", "one.aiff", 4*time.Second)

	transport.waitForEvent(t, "play:one.aiff")
	select {
	case err := <-done:
		t.Fatalf("Play() returned before track 2 was produced: %v", err)
	default:
	}
	wantPrefix := []string{"prepared:1:2", "start:2", "load:one.aiff", "speaking:0:first", "play:one.aiff"}
	if got := transport.snapshot(); !reflect.DeepEqual(got, wantPrefix) {
		t.Fatalf("events = %#v, want %#v", got, wantPrefix)
	}

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Play() error = %v, want context.Canceled", err)
	}
}

func TestPlayShowsPausedWhenToggledBeforeFirstTrackIsReady(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	commands := make(chan Command)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- playStream(ctx, 1, results, transport, commands, view, make(chan time.Time))
	}()
	commands <- Toggle
	results <- trackResult("first", "one.aiff", 4*time.Second)
	transport.waitForEvent(t, "paused:0")
	if transport.isPlaying() {
		t.Fatal("transport started after playback was paused during preparation")
	}
	commands <- Toggle
	transport.waitForEvent(t, "play:one.aiff")

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("playStream() error = %v, want context.Canceled", err)
	}
}

func TestPlayBuffersUntilNextStreamedTrackIsReady(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second, "two.aiff": 6 * time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	ticks := make(chan time.Time)
	done := make(chan error, 1)

	go func() {
		done <- playStream(context.Background(), 2, results, transport, nil, view, ticks)
	}()
	results <- trackResult("first", "one.aiff", 4*time.Second)
	transport.waitForEvent(t, "play:one.aiff")
	transport.finishCurrent()
	ticks <- time.Now()
	transport.waitForEvent(t, "buffering:1:2")

	results <- trackResult("second", "two.aiff", 6*time.Second)
	transport.waitForEvent(t, "play:two.aiff")
	close(results)
	transport.finishCurrent()
	ticks <- time.Now()

	if err := waitResult(t, done); err != nil {
		t.Fatalf("playStream() error = %v", err)
	}
	want := []string{
		"prepared:1:2", "start:2", "load:one.aiff", "speaking:0:first", "play:one.aiff",
		"spoken:0", "buffering:1:2", "prepared:2:2", "load:two.aiff", "speaking:1:second",
		"play:two.aiff", "spoken:1", "finish:2",
	}
	if got := transport.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestPlayForwardSeekWaitsForUnpreparedTrack(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second, "two.aiff": 8 * time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	commands := make(chan Command)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- playStream(ctx, 2, results, transport, commands, view, make(chan time.Time))
	}()
	results <- trackResult("first", "one.aiff", 4*time.Second)
	transport.waitForEvent(t, "play:one.aiff")
	transport.setPosition(time.Second)
	commands <- Forward
	transport.waitForEvent(t, "buffering:1:2")
	if transport.isPlaying() {
		t.Fatal("transport kept playing while a forward seek waited for unprepared audio")
	}

	results <- trackResult("second", "two.aiff", 8*time.Second)
	transport.waitForEvent(t, "seek:two.aiff:2s")
	transport.waitForEvent(t, "seeked:1:5s:6s:12s:true")
	if !transport.isPlaying() {
		t.Fatal("playing forward seek did not resume at the prepared target")
	}

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("playStream() error = %v, want context.Canceled", err)
	}
}

func TestPlayPausedForwardSeekStaysPausedAfterPreparation(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second, "two.aiff": 8 * time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	commands := make(chan Command)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- playStream(ctx, 2, results, transport, commands, view, make(chan time.Time))
	}()
	results <- trackResult("first", "one.aiff", 4*time.Second)
	transport.waitForEvent(t, "play:one.aiff")
	transport.setPosition(time.Second)
	commands <- Toggle
	transport.waitForEvent(t, "paused:0")
	commands <- Forward
	transport.waitForEvent(t, "buffering:1:2")
	results <- trackResult("second", "two.aiff", 8*time.Second)
	transport.waitForEvent(t, "seek:two.aiff:2s")

	if transport.isPlaying() {
		t.Fatal("paused forward seek resumed after target preparation")
	}
	if got := transport.countEvent("play:"); got != 1 {
		t.Fatalf("play event count = %d, want only initial playback", got)
	}

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("playStream() error = %v, want context.Canceled", err)
	}
}

func TestPlayBackwardSeekAcrossPreparedTracksIsImmediate(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second, "two.aiff": 8 * time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	commands := make(chan Command)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- playStream(ctx, 2, results, transport, commands, view, make(chan time.Time))
	}()
	results <- trackResult("first", "one.aiff", 4*time.Second)
	transport.waitForEvent(t, "play:one.aiff")
	results <- trackResult("second", "two.aiff", 8*time.Second)
	transport.waitForEvent(t, "prepared:2:2")

	transport.setPosition(3500 * time.Millisecond)
	commands <- Forward
	transport.waitForEvent(t, "seeked:1:5s:8.5s:12s:true")
	transport.setPosition(time.Second)
	commands <- Backward
	transport.waitForEvent(t, "seeked:0:-5s:0s:12s:true")
	if path, position := transport.current(); path != "one.aiff" || position != 0 {
		t.Fatalf("transport = %q at %s, want one.aiff at 0s", path, position)
	}

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("playStream() error = %v, want context.Canceled", err)
	}
}

func TestPlayReportsProducerFailure(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult)
	done := make(chan error, 1)

	go func() {
		done <- playStream(context.Background(), 2, results, transport, nil, view, make(chan time.Time))
	}()
	results <- trackResult("first", "one.aiff", time.Second)
	transport.waitForEvent(t, "play:one.aiff")
	results <- TrackResult{Err: errors.New("synthesis failed")}

	err := waitResult(t, done)
	if err == nil || !strings.Contains(err.Error(), "synthesis failed") {
		t.Fatalf("playStream() error = %v, want synthesis failure", err)
	}
	transport.waitForEvent(t, "failed:0:synthesis failed")
}

func TestPlayRejectsStreamThatClosesBeforeTotal(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": time.Second})
	view := &recordingView{events: &transport.events}
	results := make(chan TrackResult, 1)
	results <- trackResult("first", "one.aiff", time.Second)
	close(results)

	err := playStream(context.Background(), 2, results, transport, nil, view, make(chan time.Time))
	if err == nil || !strings.Contains(err.Error(), "prepared 1 of 2") {
		t.Fatalf("playStream() error = %v, want incomplete-stream error", err)
	}
}

func TestPlayCanCancelWhileWaitingForFirstTrack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan TrackResult)
	transport := newFakeTransport(nil)
	view := &recordingView{events: &transport.events}
	done := make(chan error, 1)

	go func() {
		done <- playStream(ctx, 1, results, transport, nil, view, make(chan time.Time))
	}()
	cancel()

	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("playStream() error = %v, want context.Canceled", err)
	}
	if got := transport.snapshot(); len(got) != 0 {
		t.Fatalf("events = %#v, want none", got)
	}
}

func TestPlayRejectsInvalidStreamInputs(t *testing.T) {
	tests := []struct {
		name    string
		total   int
		results <-chan TrackResult
		want    string
	}{
		{name: "zero total", results: make(chan TrackResult), want: "total tracks must be greater than zero"},
		{name: "nil stream", total: 1, want: "track result stream is required"},
		{name: "closed before first", total: 1, results: closedResults(), want: "prepared 0 of 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newFakeTransport(nil)
			err := playStream(context.Background(), tt.total, tt.results, transport, nil, &recordingView{events: &transport.events}, make(chan time.Time))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("playStream() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func trackResult(text, path string, duration time.Duration) TrackResult {
	return TrackResult{Track: Track{Text: text, Path: path, Duration: duration}}
}

func closedResults() <-chan TrackResult {
	results := make(chan TrackResult)
	close(results)
	return results
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
func (v *recordingView) Seeked(index, _ int, delta, position, duration time.Duration, complete bool) error {
	v.events.add(fmt.Sprintf("seeked:%d:%s:%s:%s:%t", index, delta, position, duration, complete))
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
