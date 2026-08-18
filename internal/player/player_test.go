package player

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPlayPrintsEachChunkBeforeSpeaking(t *testing.T) {
	var events []string
	speaker := &recordingSpeaker{events: &events, name: "test TTS", failAt: -1}
	view := &recordingView{events: &events}

	err := Play(context.Background(), []string{"第一句。", "Second."}, speaker, view)
	if err != nil {
		t.Fatalf("Play() error = %v", err)
	}
	want := []string{
		"start:2", "speaking:0:第一句。", "speak:第一句。", "spoken:0",
		"speaking:1:Second.", "speak:Second.", "spoken:1", "finish:2",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestPlayStopsAndReportsSpeechFailure(t *testing.T) {
	var events []string
	speaker := &recordingSpeaker{events: &events, name: "test TTS", failAt: 1}
	view := &recordingView{events: &events}

	err := Play(context.Background(), []string{"first", "second", "third"}, speaker, view)
	if err == nil || !strings.Contains(err.Error(), "speech failed") {
		t.Fatalf("Play() error = %v, want speech failure", err)
	}
	want := []string{
		"start:3", "speaking:0:first", "speak:first", "spoken:0",
		"speaking:1:second", "speak:second", "failed:1:speech failed",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestPlayDoesNotStartWhenContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []string

	err := Play(ctx, []string{"first"}, &recordingSpeaker{events: &events, failAt: -1}, &recordingView{events: &events})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Play() error = %v, want context.Canceled", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestPlayRejectsBlankChunk(t *testing.T) {
	err := Play(context.Background(), []string{"first", " \n"}, &recordingSpeaker{failAt: -1}, &recordingView{})
	if err == nil || !strings.Contains(err.Error(), "chunk 2 is empty") {
		t.Fatalf("Play() error = %v, want empty-chunk error", err)
	}
}

func TestPlayDoesNotSpeakWhenSpeakingOutputFails(t *testing.T) {
	var events []string
	wantErr := errors.New("output closed")
	speaker := &recordingSpeaker{events: &events, failAt: -1}
	view := &recordingView{events: &events, speakingErr: wantErr}

	err := Play(context.Background(), []string{"must not be spoken"}, speaker, view)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Play() error = %v, want %v", err, wantErr)
	}
	wantEvents := []string{"start:1", "speaking:0:must not be spoken"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
}

type recordingSpeaker struct {
	events *[]string
	name   string
	index  int
	failAt int
}

func (s *recordingSpeaker) Name() string {
	return s.name
}

func (s *recordingSpeaker) Speak(_ context.Context, text string) error {
	if s.events != nil {
		*s.events = append(*s.events, "speak:"+text)
	}
	index := s.index
	s.index++
	if index == s.failAt {
		return fmt.Errorf("speech failed")
	}
	return nil
}

type recordingView struct {
	events      *[]string
	speakingErr error
}

func (v *recordingView) add(event string) {
	if v.events != nil {
		*v.events = append(*v.events, event)
	}
}

func (v *recordingView) Start(total int) error {
	v.add(fmt.Sprintf("start:%d", total))
	return nil
}

func (v *recordingView) Speaking(index, _ int, text string) error {
	v.add(fmt.Sprintf("speaking:%d:%s", index, text))
	return v.speakingErr
}

func (v *recordingView) Spoken(index, _ int) error {
	v.add(fmt.Sprintf("spoken:%d", index))
	return nil
}

func (v *recordingView) Failed(index, _ int, err error) error {
	v.add(fmt.Sprintf("failed:%d:%s", index, err))
	return nil
}

func (v *recordingView) Finish(total int) error {
	v.add(fmt.Sprintf("finish:%d", total))
	return nil
}
