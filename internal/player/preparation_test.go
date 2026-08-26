package player

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func receiveAudio(t *testing.T, source AudioSource) AudioResult {
	t.Helper()
	select {
	case result := <-source.Results():
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prepared audio")
		return AudioResult{}
	}
}

func TestPreparationPrioritizesTargetCancelsOldWorkAndCachesAudio(t *testing.T) {
	blocked, canceled := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var calls []Target
	source := NewPreparation(context.Background(), []string{"One. Two.", "Three.", "Four.", "Five."},
		func(ctx context.Context, target Target, text string) (SentenceTrack, error) {
			mu.Lock()
			calls = append(calls, target)
			mu.Unlock()
			if target == (Target{0, 1}) {
				close(blocked)
				<-ctx.Done()
				close(canceled)
				return SentenceTrack{}, ctx.Err()
			}
			return SentenceTrack{Path: text, Duration: time.Second}, nil
		})
	defer source.Close()
	source.Request(Target{})
	if result := receiveAudio(t, source); result.Target != (Target{}) || result.Err != nil {
		t.Fatalf("first result = %+v", result)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("lookahead did not start")
	}
	source.Suspend()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("suspending navigation did not cancel old synthesis")
	}
	source.Request(Target{3, 0})
	if result := receiveAudio(t, source); result.Target != (Target{3, 0}) || result.Err != nil {
		t.Fatalf("priority result = %+v", result)
	}
	source.Request(Target{3, 0})
	if result := receiveAudio(t, source); result.Audio.Path != "Five." {
		t.Fatalf("cached result = %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(calls) != "[{0 0} {0 1} {3 0}]" {
		t.Fatalf("synthesis calls = %v, want no skipped chapters or duplicate synthesis", calls)
	}
}

func TestPreparationLimitsLookaheadAndDefersPrefetchErrors(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	source := NewPreparation(context.Background(), []string{"One.", "Two.", "Three.", "Four.", "Five."},
		func(_ context.Context, target Target, text string) (SentenceTrack, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			if target.Chapter == 2 {
				return SentenceTrack{}, errors.New("prefetch failed")
			}
			return SentenceTrack{Path: text, Duration: time.Second}, nil
		})
	defer source.Close()
	source.Request(Target{})
	for range 3 {
		if result := receiveAudio(t, source); result.Err != nil || result.Target.Chapter > 3 {
			t.Fatalf("unexpected prefetch result: %+v", result)
		}
	}
	select {
	case result := <-source.Results():
		t.Fatalf("excess lookahead or premature error: %+v", result)
	case <-time.After(30 * time.Millisecond):
	}
	mu.Lock()
	if calls != 4 {
		t.Errorf("calls=%d, want current plus three following sentences", calls)
	}
	mu.Unlock()
	source.Request(Target{2, 0})
	if result := receiveAudio(t, source); result.Err == nil || result.Err.Error() != "prefetch failed" {
		t.Fatalf("demanded failure not reported: %+v", result)
	}
}

func TestPreparationCloseJoinsCanceledWorker(t *testing.T) {
	started, exited := make(chan struct{}), make(chan struct{})
	source := NewPreparation(context.Background(), []string{"One."},
		func(ctx context.Context, _ Target, _ string) (SentenceTrack, error) {
			close(started)
			<-ctx.Done()
			defer close(exited)
			return SentenceTrack{}, ctx.Err()
		})
	source.Request(Target{})
	<-started
	source.Close()
	select {
	case <-exited:
	default:
		t.Fatal("Close returned while a worker still owned temporary files")
	}
}
