package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/miclle/say/internal/textchunk"
)

const pollInterval = 25 * time.Millisecond

// Command describes an interactive playback action.
type Command uint8

const (
	// Toggle switches between playing and paused states.
	Toggle Command = iota + 1
	// Backward jumps to the beginning of the previous sentence.
	Backward
	// Forward jumps to the beginning of the next sentence.
	Forward
	// PreviousChapter jumps to the beginning of the previous chapter.
	PreviousChapter
	// NextChapter jumps to the beginning of the next chapter.
	NextChapter
)

// SentenceTrack binds one displayed sentence to its synthesized audio file.
type SentenceTrack struct {
	Path     string
	Duration time.Duration
}

// Track binds one displayed text unit to its ordered sentence audio files.
type Track struct {
	Text      string
	Sentences []SentenceTrack
	Duration  time.Duration
	Complete  bool
}

// TrackResult carries one ordered prepared track or a producer failure.
type TrackResult struct {
	Track Track
	Err   error
}

// Transport controls one active audio file.
type Transport interface {
	Load(path string) error
	Play() error
	Pause()
	Seek(position time.Duration) error
	Position() time.Duration
	IsPlaying() bool
}

// View receives preparation, playback, and control events.
type View interface {
	Prepared(prepared, total int) error
	Start(total int) error
	Speaking(index, total int, text string) error
	Progress(index, total, sentence int) error
	Spoken(index, total int) error
	Paused(index, total int) error
	Resumed(index, total int) error
	Buffering(index, total int) error
	Seeked(index, total int, text string, sentence int, playing bool, delta, position, duration time.Duration, complete bool) error
	Failed(index, total int, err error) error
	Finish(total int) error
}

// Play renders and controls an ordered stream of synthesized audio tracks.
// Source chapters define every navigation boundary before audio is ready.
func Play(ctx context.Context, chapters []string, results <-chan TrackResult, transport Transport, commands <-chan Command, view View) error {
	sentenceCounts := make([]int, len(chapters))
	for index, text := range chapters {
		sentenceCounts[index] = len(textchunk.Sentences(text))
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	return playStream(ctx, sentenceCounts, results, transport, commands, view, ticker.C)
}

type navigationTarget struct {
	chapter  int
	sentence int
}

type streamPlayer struct {
	total           int
	results         <-chan TrackResult
	transport       Transport
	commands        <-chan Command
	view            View
	ticks           <-chan time.Time
	tracks          []Track
	sentenceCounts  []int
	offsets         []time.Duration
	current         int
	currentSentence int
	playing         bool
	active          bool
	spoken          bool
	waiting         bool
	pending         *navigationTarget
}

func playStream(ctx context.Context, sentenceCounts []int, results <-chan TrackResult, transport Transport, commands <-chan Command, view View, ticks <-chan time.Time) error {
	total := len(sentenceCounts)
	if err := validateStream(ctx, total, results, transport, view); err != nil {
		return err
	}
	for index, count := range sentenceCounts {
		if count <= 0 {
			return fmt.Errorf("chapter %d has no sentences", index+1)
		}
	}
	p := &streamPlayer{
		total:           total,
		sentenceCounts:  sentenceCounts,
		results:         results,
		transport:       transport,
		commands:        commands,
		view:            view,
		ticks:           ticks,
		offsets:         []time.Duration{0},
		current:         -1,
		currentSentence: -1,
		playing:         true,
	}

	for {
		select {
		case <-ctx.Done():
			if p.current < 0 {
				return ctx.Err()
			}
			return reportFailure(view, p.current, total, ctx.Err())
		case result, ok := <-p.results:
			if !ok {
				if err := ctx.Err(); err != nil {
					if p.current < 0 {
						return err
					}
					return reportFailure(view, p.current, total, err)
				}
				p.results = nil
				if len(p.tracks) != total {
					return p.fail(fmt.Errorf("track stream prepared %d of %d tracks", len(p.tracks), total))
				}
				if !p.complete() {
					return p.fail(fmt.Errorf("track stream closed before track %d sentence audio completed", len(p.tracks)))
				}
				finished, err := p.resolveWait()
				if err != nil {
					return p.fail(err)
				}
				if finished {
					return p.finish()
				}
				continue
			}
			if result.Err != nil {
				return p.fail(result.Err)
			}
			finished, err := p.addTrack(result.Track)
			if err != nil {
				return p.fail(err)
			}
			if finished {
				return p.finish()
			}
		case command, ok := <-p.commands:
			if !ok {
				p.commands = nil
				continue
			}
			finished, err := p.handleCommands(command)
			if err != nil {
				return p.fail(err)
			}
			if finished {
				return p.finish()
			}
		case <-p.ticks:
			finished, err := p.handleTick()
			if err != nil {
				return p.fail(err)
			}
			if finished {
				return p.finish()
			}
		}
	}
}

func validateStream(ctx context.Context, total int, results <-chan TrackResult, transport Transport, view View) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if total <= 0 {
		return fmt.Errorf("total tracks must be greater than zero")
	}
	if results == nil {
		return fmt.Errorf("track result stream is required")
	}
	if transport == nil {
		return fmt.Errorf("audio transport is required")
	}
	if view == nil {
		return fmt.Errorf("view is required")
	}
	return nil
}

func (p *streamPlayer) addTrack(track Track) (bool, error) {
	if len(p.tracks) > 0 && !p.tracks[len(p.tracks)-1].Complete {
		return p.updateTrack(track)
	}
	index := len(p.tracks)
	if index >= p.total {
		return false, fmt.Errorf("track stream produced more than %d tracks", p.total)
	}
	if err := validateTrack(track, index, p.sentenceCounts[index]); err != nil {
		return false, err
	}
	previous := p.offsets[len(p.offsets)-1]
	if track.Duration > time.Duration(1<<63-1)-previous {
		return false, fmt.Errorf("total audio duration overflows")
	}
	p.tracks = append(p.tracks, track)
	p.offsets = append(p.offsets, previous+track.Duration)
	if err := p.view.Prepared(len(p.tracks), p.total); err != nil {
		return false, fmt.Errorf("render preparation progress: %w", err)
	}

	if p.current < 0 {
		if err := p.view.Start(p.total); err != nil {
			return false, fmt.Errorf("render playback header: %w", err)
		}
		p.current = 0
		if err := p.activate(0); err != nil {
			return false, err
		}
		return false, nil
	}
	return p.resolveWait()
}

func (p *streamPlayer) updateTrack(track Track) (bool, error) {
	index := len(p.tracks) - 1
	if err := validateTrack(track, index, p.sentenceCounts[index]); err != nil {
		return false, err
	}
	previous := p.tracks[index]
	if track.Text != previous.Text {
		return false, fmt.Errorf("track %d text changed while preparing sentence audio", index+1)
	}
	if len(track.Sentences) != len(previous.Sentences)+1 {
		return false, fmt.Errorf("track %d sentence audio update has %d segments, want %d", index+1, len(track.Sentences), len(previous.Sentences)+1)
	}
	for sentenceIndex, sentence := range previous.Sentences {
		if track.Sentences[sentenceIndex] != sentence {
			return false, fmt.Errorf("track %d sentence %d changed while preparing audio", index+1, sentenceIndex+1)
		}
	}
	base := p.offsets[index]
	if track.Duration > time.Duration(1<<63-1)-base {
		return false, fmt.Errorf("total audio duration overflows")
	}
	p.tracks[index] = track
	p.offsets[index+1] = base + track.Duration
	return p.resolveWait()
}

func (p *streamPlayer) resolveWait() (bool, error) {
	if p.pending != nil {
		return p.resolveNavigation()
	}
	if p.waiting && !p.spoken && p.current >= 0 && p.currentSentence+1 < len(p.tracks[p.current].Sentences) {
		p.waiting = false
		if err := p.advanceSentence(); err != nil {
			return false, err
		}
		return false, nil
	}
	if p.waiting && p.current+1 < len(p.tracks) {
		p.current++
		p.waiting = false
		p.spoken = false
		if err := p.activate(p.current); err != nil {
			return false, err
		}
	}
	if p.waiting && p.complete() && p.current == len(p.tracks)-1 {
		return true, nil
	}
	return false, nil
}

func (p *streamPlayer) handleCommands(command Command) (bool, error) {
	// Drain only a bounded snapshot. Holding a key must not build a queue of
	// expensive native seeks or starve audio preparation and cancellation.
	batch := []Command{command}
drain:
	for len(batch) < 32 {
		select {
		case next, ok := <-p.commands:
			if !ok {
				p.commands = nil
				break drain
			}
			batch = append(batch, next)
		default:
			break drain
		}
	}
	for index := 0; index < len(batch); {
		command := batch[index]
		count := 1
		if command != Toggle {
			for index+count < len(batch) && batch[index+count] == command {
				count++
			}
		}
		if finished, err := p.handleRepeatedCommand(command, count); err != nil || finished {
			return finished, err
		}
		index += count
	}
	return false, nil
}

func (p *streamPlayer) handleRepeatedCommand(command Command, count int) (bool, error) {
	if p.current < 0 {
		if command == Toggle {
			p.playing = !p.playing
		}
		return false, nil
	}
	switch command {
	case Toggle:
		if p.playing {
			if p.active {
				p.transport.Pause()
			}
			p.playing = false
			if err := p.view.Paused(p.current, p.total); err != nil {
				return false, fmt.Errorf("render paused state: %w", err)
			}
			return false, nil
		}
		p.playing = true
		if p.active {
			if err := p.transport.Play(); err != nil {
				return false, fmt.Errorf("resume track %d of %d: %w", p.current+1, p.total, err)
			}
		}
		if err := p.view.Resumed(p.current, p.total); err != nil {
			return false, fmt.Errorf("render resumed state: %w", err)
		}
	case Backward, Forward:
		direction := -count
		if command == Forward {
			direction = count
		}
		return p.requestSentence(direction)
	case PreviousChapter:
		return p.requestChapter(-count)
	case NextChapter:
		return p.requestChapter(count)
	}
	return false, nil
}

func (p *streamPlayer) handleTick() (bool, error) {
	if p.current < 0 || !p.playing || !p.active {
		return false, nil
	}
	if p.transport.IsPlaying() {
		return false, nil
	}
	track := p.tracks[p.current]
	if p.currentSentence+1 < len(track.Sentences) {
		if err := p.advanceSentence(); err != nil {
			return false, err
		}
		return false, nil
	}
	if !track.Complete {
		p.active = false
		if !p.waiting {
			p.waiting = true
			if err := p.view.Buffering(p.current, p.total); err != nil {
				return false, fmt.Errorf("render buffering state: %w", err)
			}
		}
		return false, nil
	}
	if !p.spoken {
		if err := p.view.Spoken(p.current, p.total); err != nil {
			return false, fmt.Errorf("render completion for track %d of %d: %w", p.current+1, p.total, err)
		}
		p.spoken = true
		p.currentSentence = -1
	}
	p.active = false
	if p.current+1 < len(p.tracks) {
		p.current++
		p.spoken = false
		if err := p.activate(p.current); err != nil {
			return false, err
		}
		return false, nil
	}
	if p.complete() {
		return true, nil
	}
	if !p.waiting {
		p.waiting = true
		if err := p.view.Buffering(p.current+1, p.total); err != nil {
			return false, fmt.Errorf("render buffering state: %w", err)
		}
	}
	return false, nil
}

func (p *streamPlayer) advanceSentence() error {
	p.currentSentence++
	sentence := p.tracks[p.current].Sentences[p.currentSentence]
	if err := p.transport.Load(sentence.Path); err != nil {
		return fmt.Errorf("load sentence %d of track %d of %d: %w", p.currentSentence+1, p.current+1, p.total, err)
	}
	if err := p.view.Progress(p.current, p.total, p.currentSentence); err != nil {
		return fmt.Errorf("render sentence progress for track %d of %d: %w", p.current+1, p.total, err)
	}
	if p.playing {
		if err := p.transport.Play(); err != nil {
			return fmt.Errorf("play sentence %d of track %d of %d: %w", p.currentSentence+1, p.current+1, p.total, err)
		}
	}
	p.active = true
	return nil
}

func (p *streamPlayer) activate(index int) error {
	track := p.tracks[index]
	if err := p.transport.Load(track.Sentences[0].Path); err != nil {
		return fmt.Errorf("load first sentence of track %d of %d: %w", index+1, p.total, err)
	}
	if err := p.view.Speaking(index, p.total, track.Text); err != nil {
		return fmt.Errorf("render track %d of %d before playback: %w", index+1, p.total, err)
	}
	if !p.playing {
		if err := p.view.Paused(index, p.total); err != nil {
			return fmt.Errorf("render paused state: %w", err)
		}
	}
	p.currentSentence = 0
	if p.playing {
		if err := p.transport.Play(); err != nil {
			return fmt.Errorf("play track %d of %d: %w", index+1, p.total, err)
		}
	}
	p.active = true
	return nil
}

func (p *streamPlayer) knownDuration() time.Duration {
	return p.offsets[len(p.offsets)-1]
}

func (p *streamPlayer) complete() bool {
	return len(p.tracks) == p.total && p.tracks[len(p.tracks)-1].Complete
}

func (p *streamPlayer) fail(err error) error {
	if p.current < 0 {
		return err
	}
	return reportFailure(p.view, p.current, p.total, err)
}

func (p *streamPlayer) finish() error {
	if err := p.view.Finish(p.total); err != nil {
		return fmt.Errorf("render playback summary: %w", err)
	}
	return nil
}

func validateTrack(track Track, index, sentenceCount int) error {
	if strings.TrimSpace(track.Text) == "" {
		return fmt.Errorf("track %d text is empty", index+1)
	}
	if len(track.Sentences) == 0 {
		return fmt.Errorf("track %d has no sentence audio", index+1)
	}
	if len(track.Sentences) > sentenceCount || track.Complete && len(track.Sentences) != sentenceCount {
		return fmt.Errorf("track %d audio sentence count %d does not match source count %d", index+1, len(track.Sentences), sentenceCount)
	}
	if track.Duration <= 0 {
		return fmt.Errorf("track %d duration must be greater than zero", index+1)
	}
	total := time.Duration(0)
	for sentenceIndex, sentence := range track.Sentences {
		if strings.TrimSpace(sentence.Path) == "" {
			return fmt.Errorf("track %d sentence %d path is empty", index+1, sentenceIndex+1)
		}
		if sentence.Duration <= 0 {
			return fmt.Errorf("track %d sentence %d duration must be greater than zero", index+1, sentenceIndex+1)
		}
		if sentence.Duration > time.Duration(1<<63-1)-total {
			return fmt.Errorf("track %d duration overflows", index+1)
		}
		total += sentence.Duration
	}
	if track.Duration != total {
		return fmt.Errorf("track %d duration %s does not match sentence audio total %s", index+1, track.Duration, total)
	}
	return nil
}

func reportFailure(view View, index, total int, playbackErr error) error {
	if renderErr := view.Failed(index, total, playbackErr); renderErr != nil {
		return errors.Join(playbackErr, fmt.Errorf("render playback failure: %w", renderErr))
	}
	return playbackErr
}
