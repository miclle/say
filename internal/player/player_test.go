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

func TestPlayAdvancesTracksAndRendersBeforeAudio(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second, "two.aiff": 6 * time.Second})
	view := &recordingView{events: &transport.events}
	ticks := make(chan time.Time)
	done := make(chan error, 1)

	go func() {
		done <- play(context.Background(), []Track{
			{Text: "第一段。", Path: "one.aiff", Duration: 4 * time.Second},
			{Text: "Second paragraph.", Path: "two.aiff", Duration: 6 * time.Second},
		}, transport, nil, view, ticks)
	}()

	transport.waitForEvent(t, "play:one.aiff")
	transport.finishCurrent()
	ticks <- time.Now()
	transport.waitForEvent(t, "play:two.aiff")
	transport.finishCurrent()
	ticks <- time.Now()

	if err := waitResult(t, done); err != nil {
		t.Fatalf("play() error = %v", err)
	}
	want := []string{
		"start:2", "load:one.aiff", "speaking:0:第一段。", "play:one.aiff", "spoken:0",
		"load:two.aiff", "speaking:1:Second paragraph.", "play:two.aiff", "spoken:1", "finish:2",
	}
	if got := transport.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestPlayHandlesPauseResumeAndSameTrackSeek(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 20 * time.Second})
	view := &recordingView{events: &transport.events}
	commands := make(chan Command)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- play(ctx, []Track{{Text: "paragraph", Path: "one.aiff", Duration: 20 * time.Second}}, transport, commands, view, ticks)
	}()
	transport.waitForEvent(t, "play:one.aiff")
	transport.setPosition(8 * time.Second)

	commands <- Toggle
	transport.waitForEvent(t, "paused:0")
	commands <- Forward
	transport.waitForEvent(t, "seeked:0:5s:13s:20s")
	if transport.isPlaying() {
		t.Fatal("seek while paused resumed playback")
	}
	commands <- Toggle
	transport.waitForEvent(t, "resumed:0")
	commands <- Backward
	transport.waitForEvent(t, "seeked:0:-5s:8s:20s")
	if !transport.isPlaying() {
		t.Fatal("seek while playing paused playback")
	}

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("play() error = %v, want context.Canceled", err)
	}
}

func TestPlaySeeksAcrossTracksAndClampsAtStart(t *testing.T) {
	transport := newFakeTransport(map[string]time.Duration{"one.aiff": 4 * time.Second, "two.aiff": 8 * time.Second})
	view := &recordingView{events: &transport.events}
	commands := make(chan Command)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- play(ctx, []Track{
			{Text: "one", Path: "one.aiff", Duration: 4 * time.Second},
			{Text: "two", Path: "two.aiff", Duration: 8 * time.Second},
		}, transport, commands, view, make(chan time.Time))
	}()
	transport.waitForEvent(t, "play:one.aiff")
	transport.setPosition(time.Second)
	commands <- Forward
	transport.waitForEvent(t, "seeked:1:5s:6s:12s")
	transport.waitForEvent(t, "seek:two.aiff:2s")
	transport.setPosition(time.Second)
	commands <- Backward
	transport.waitForEvent(t, "seeked:0:-5s:0s:12s")
	if path, position := transport.current(); path != "one.aiff" || position != 0 {
		t.Fatalf("transport = %q at %s, want one.aiff at 0s", path, position)
	}

	cancel()
	if err := waitResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("play() error = %v, want context.Canceled", err)
	}
}

func TestPlayRejectsInvalidTracksBeforeRendering(t *testing.T) {
	tests := []struct {
		name   string
		tracks []Track
		want   string
	}{
		{name: "none", want: "at least one audio track"},
		{name: "blank text", tracks: []Track{{Text: " ", Path: "one", Duration: time.Second}}, want: "track 1 text is empty"},
		{name: "blank path", tracks: []Track{{Text: "one", Duration: time.Second}}, want: "track 1 path is empty"},
		{name: "duration", tracks: []Track{{Text: "one", Path: "one"}}, want: "track 1 duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newFakeTransport(nil)
			err := play(context.Background(), tt.tracks, transport, nil, &recordingView{events: &transport.events}, make(chan time.Time))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("play() error = %v, want containing %q", err, tt.want)
			}
			if got := transport.snapshot(); len(got) != 0 {
				t.Fatalf("events = %#v, want none", got)
			}
		})
	}
}

type fakeTransport struct {
	mu        sync.Mutex
	events    eventLog
	durations map[string]time.Duration
	path      string
	position  time.Duration
	playing   bool
	err       error
}

func newFakeTransport(durations map[string]time.Duration) *fakeTransport {
	return &fakeTransport{durations: durations, events: eventLog{changed: make(chan struct{}, 1)}}
}

func (t *fakeTransport) Load(path string) error {
	t.mu.Lock()
	t.path, t.position, t.playing = path, 0, false
	t.mu.Unlock()
	t.events.add("load:" + path)
	return t.err
}

func (t *fakeTransport) Play() error {
	t.mu.Lock()
	t.playing = true
	path := t.path
	t.mu.Unlock()
	t.events.add("play:" + path)
	return t.err
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
	return t.err
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

func (t *fakeTransport) Close() error {
	t.events.add("close")
	return nil
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
func (t *fakeTransport) waitForEvent(tb *testing.T, e string) {
	tb.Helper()
	t.events.waitFor(tb, e)
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
func (v *recordingView) Seeked(index, _ int, delta, position, duration time.Duration) error {
	v.events.add(fmt.Sprintf("seeked:%d:%s:%s:%s", index, delta, position, duration))
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
