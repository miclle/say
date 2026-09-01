package player

import (
	"errors"
	"testing"
	"time"
)

func TestCombinedViewForwardsCallbacksInOrder(t *testing.T) {
	events := &eventLog{changed: make(chan struct{}, 1)}
	first := &recordingView{events: events}
	second := &recordingView{events: events}
	view := CombineViews(first, second)

	if err := view.Start(3); err != nil {
		t.Fatal(err)
	}
	if got, want := events.snapshot(), []string{"start:3", "start:3"}; !equalStrings(got, want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
}

func TestCombinedViewStopsAtFirstErrorAndIgnoresNilViews(t *testing.T) {
	want := errors.New("render failed")
	calls := 0
	view := CombineViews(nil, errorView{err: want}, countingView{calls: &calls})

	if err := view.Start(1); !errors.Is(err, want) {
		t.Fatalf("Start() error=%v want=%v", err, want)
	}
	if calls != 0 {
		t.Fatalf("later view calls=%d want=0", calls)
	}
	if err := CombineViews().Start(1); err != nil {
		t.Fatalf("empty combined view error=%v", err)
	}
}

type errorView struct {
	noopView
	err error
}

func (v errorView) Start(int) error { return v.err }

type countingView struct {
	noopView
	calls *int
}

func (v countingView) Start(int) error {
	*v.calls++
	return nil
}

type noopView struct{}

func (noopView) Prepared(int, int) error              { return nil }
func (noopView) Start(int) error                      { return nil }
func (noopView) Speaking(int, int, string) error      { return nil }
func (noopView) Progress(int, int, int) error         { return nil }
func (noopView) Spoken(int, int) error                { return nil }
func (noopView) Paused(int, int) error                { return nil }
func (noopView) Resumed(int, int) error               { return nil }
func (noopView) Buffering(int, int) error             { return nil }
func (noopView) Selected(int, int, string, int) error { return nil }
func (noopView) Seeked(int, int, string, int, bool, time.Duration, time.Duration, time.Duration, bool) error {
	return nil
}
func (noopView) Failed(int, int, error) error { return nil }
func (noopView) Finish(int) error             { return nil }

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
