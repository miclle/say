package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/miclle/say/internal/textchunk"
)

const (
	pollInterval = 25 * time.Millisecond
	seekStep     = 5 * time.Second
)

// Command describes an interactive playback action.
type Command uint8

const (
	// Toggle switches between playing and paused states.
	Toggle Command = iota + 1
	// Backward seeks five seconds toward the start of the document.
	Backward
	// Forward seeks five seconds toward the end of the document.
	Forward
)

// Track binds one displayed text unit to its synthesized audio file.
type Track struct {
	Text     string
	Path     string
	Duration time.Duration
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
func Play(ctx context.Context, total int, results <-chan TrackResult, transport Transport, commands <-chan Command, view View) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	return playStream(ctx, total, results, transport, commands, view, ticker.C)
}

type pendingSeek struct {
	target time.Duration
	delta  time.Duration
}

type streamPlayer struct {
	total           int
	results         <-chan TrackResult
	transport       Transport
	commands        <-chan Command
	view            View
	ticks           <-chan time.Time
	tracks          []Track
	sentences       [][]string
	offsets         []time.Duration
	current         int
	currentSentence int
	playing         bool
	active          bool
	spoken          bool
	waiting         bool
	streamDone      bool
	pending         *pendingSeek
}

func playStream(ctx context.Context, total int, results <-chan TrackResult, transport Transport, commands <-chan Command, view View, ticks <-chan time.Time) error {
	if err := validateStream(ctx, total, results, transport, view); err != nil {
		return err
	}
	p := &streamPlayer{
		total:           total,
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
				p.streamDone = true
				if len(p.tracks) != total {
					return p.fail(fmt.Errorf("track stream prepared %d of %d tracks", len(p.tracks), total))
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
			finished, err := p.handleCommand(command)
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
	index := len(p.tracks)
	if index >= p.total {
		return false, fmt.Errorf("track stream produced more than %d tracks", p.total)
	}
	if err := validateTrack(track, index); err != nil {
		return false, err
	}
	previous := p.offsets[len(p.offsets)-1]
	if track.Duration > time.Duration(1<<63-1)-previous {
		return false, fmt.Errorf("total audio duration overflows")
	}
	p.tracks = append(p.tracks, track)
	p.sentences = append(p.sentences, textchunk.Sentences(track.Text))
	p.offsets = append(p.offsets, previous+track.Duration)
	if err := p.view.Prepared(len(p.tracks), p.total); err != nil {
		return false, fmt.Errorf("render preparation progress: %w", err)
	}

	if p.current < 0 {
		if err := p.view.Start(p.total); err != nil {
			return false, fmt.Errorf("render playback header: %w", err)
		}
		p.current = 0
		if err := p.activate(0, 0); err != nil {
			return false, err
		}
		if !p.playing {
			if err := p.view.Paused(p.current, p.total); err != nil {
				return false, fmt.Errorf("render paused state: %w", err)
			}
		}
		return false, nil
	}
	return p.resolveWait()
}

func (p *streamPlayer) resolveWait() (bool, error) {
	if p.pending != nil {
		complete := p.complete()
		knownDuration := p.knownDuration()
		if p.pending.target < knownDuration || complete {
			target := p.pending.target
			delta := p.pending.delta
			p.pending = nil
			return p.performSeek(target, delta, complete)
		}
	}
	if p.waiting && p.current+1 < len(p.tracks) {
		p.current++
		p.waiting = false
		p.spoken = false
		if err := p.activate(p.current, 0); err != nil {
			return false, err
		}
	}
	if p.waiting && p.complete() && p.current == len(p.tracks)-1 {
		return true, nil
	}
	return false, nil
}

func (p *streamPlayer) handleCommand(command Command) (bool, error) {
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
		delta := -seekStep
		if command == Forward {
			delta = seekStep
		}
		return p.requestSeek(delta)
	}
	return false, nil
}

func (p *streamPlayer) requestSeek(delta time.Duration) (bool, error) {
	target := p.absolutePosition() + delta
	if p.pending != nil {
		target = p.pending.target + delta
	}
	if target < 0 {
		target = 0
	}
	complete := p.complete()
	if target < p.knownDuration() || complete {
		p.pending = nil
		return p.performSeek(target, delta, complete)
	}

	if p.active {
		p.transport.Pause()
		p.active = false
	}
	p.waiting = false
	p.pending = &pendingSeek{target: target, delta: delta}
	return false, nil
}

func (p *streamPlayer) performSeek(target, delta time.Duration, complete bool) (bool, error) {
	knownDuration := p.knownDuration()
	if target >= knownDuration {
		if !complete {
			return false, fmt.Errorf("seek target exceeds prepared audio")
		}
		target = knownDuration
		last := len(p.tracks) - 1
		if p.active {
			p.transport.Pause()
		}
		if p.current != last || !p.active {
			p.current = last
			if err := p.activateAt(p.current, p.tracks[last].Duration, false, false); err != nil {
				return false, err
			}
		} else if err := p.transport.Seek(p.tracks[last].Duration); err != nil {
			return false, fmt.Errorf("seek to document end: %w", err)
		}
		p.currentSentence = estimatedSentence(p.sentences[last], p.tracks[last].Duration, p.tracks[last].Duration)
		if err := p.view.Seeked(last, p.total, p.tracks[last].Text, p.currentSentence, p.playing, delta, target, knownDuration, true); err != nil {
			return false, fmt.Errorf("render seek state: %w", err)
		}
		return true, nil
	}

	index := 0
	for index+1 < len(p.offsets) && p.offsets[index+1] <= target {
		index++
	}
	local := target - p.offsets[index]
	activateTarget := index != p.current || !p.active
	if activateTarget {
		p.current = index
		if err := p.activateAt(index, local, false, false); err != nil {
			return false, err
		}
	} else if err := p.transport.Seek(local); err != nil {
		return false, fmt.Errorf("seek track %d of %d: %w", index+1, p.total, err)
	}
	p.active = true
	p.waiting = false
	p.spoken = false
	sentence := estimatedSentence(p.sentences[index], local, p.tracks[index].Duration)
	p.currentSentence = sentence
	if err := p.view.Seeked(index, p.total, p.tracks[index].Text, sentence, p.playing, delta, target, knownDuration, complete); err != nil {
		return false, fmt.Errorf("render seek state: %w", err)
	}
	if activateTarget && p.playing {
		if err := p.transport.Play(); err != nil {
			return false, fmt.Errorf("play track %d of %d after seek: %w", index+1, p.total, err)
		}
	}
	return false, nil
}

func (p *streamPlayer) handleTick() (bool, error) {
	if p.current < 0 || !p.playing || !p.active {
		return false, nil
	}
	if p.transport.IsPlaying() {
		return false, p.reportProgress()
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
		if err := p.activate(p.current, 0); err != nil {
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

func (p *streamPlayer) activate(index int, position time.Duration) error {
	return p.activateAt(index, position, p.playing, true)
}

func (p *streamPlayer) activateAt(index int, position time.Duration, shouldPlay, render bool) error {
	track := p.tracks[index]
	if err := p.transport.Load(track.Path); err != nil {
		return fmt.Errorf("load track %d of %d: %w", index+1, p.total, err)
	}
	if render {
		if err := p.view.Speaking(index, p.total, track.Text); err != nil {
			return fmt.Errorf("render track %d of %d before playback: %w", index+1, p.total, err)
		}
	}
	if position > 0 {
		if err := p.transport.Seek(position); err != nil {
			return fmt.Errorf("seek track %d of %d: %w", index+1, p.total, err)
		}
	}
	p.currentSentence = estimatedSentence(p.sentences[index], position, track.Duration)
	if shouldPlay {
		if err := p.transport.Play(); err != nil {
			return fmt.Errorf("play track %d of %d: %w", index+1, p.total, err)
		}
	}
	p.active = true
	return nil
}

func (p *streamPlayer) reportProgress() error {
	track := p.tracks[p.current]
	sentence := estimatedSentence(p.sentences[p.current], p.transport.Position(), track.Duration)
	if sentence == p.currentSentence {
		return nil
	}
	p.currentSentence = sentence
	if err := p.view.Progress(p.current, p.total, sentence); err != nil {
		return fmt.Errorf("render sentence progress for track %d of %d: %w", p.current+1, p.total, err)
	}
	return nil
}

func estimatedSentence(sentences []string, position, duration time.Duration) int {
	if len(sentences) <= 1 || duration <= 0 || position <= 0 {
		return 0
	}
	if position >= duration {
		return len(sentences) - 1
	}

	total := 0
	for _, sentence := range sentences {
		total += max(1, utf8.RuneCountInString(sentence))
	}
	progress := float64(position) / float64(duration)
	cumulative := 0
	for index, sentence := range sentences {
		cumulative += max(1, utf8.RuneCountInString(sentence))
		if progress < float64(cumulative)/float64(total) {
			return index
		}
	}
	return len(sentences) - 1
}

func (p *streamPlayer) absolutePosition() time.Duration {
	if p.current < 0 {
		return 0
	}
	position := p.transport.Position()
	if position < 0 {
		position = 0
	}
	if position > p.tracks[p.current].Duration {
		position = p.tracks[p.current].Duration
	}
	return p.offsets[p.current] + position
}

func (p *streamPlayer) knownDuration() time.Duration {
	return p.offsets[len(p.offsets)-1]
}

func (p *streamPlayer) complete() bool {
	return p.streamDone || len(p.tracks) == p.total
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

func validateTrack(track Track, index int) error {
	if strings.TrimSpace(track.Text) == "" {
		return fmt.Errorf("track %d text is empty", index+1)
	}
	if strings.TrimSpace(track.Path) == "" {
		return fmt.Errorf("track %d path is empty", index+1)
	}
	if track.Duration <= 0 {
		return fmt.Errorf("track %d duration must be greater than zero", index+1)
	}
	return nil
}

func reportFailure(view View, index, total int, playbackErr error) error {
	if renderErr := view.Failed(index, total, playbackErr); renderErr != nil {
		return errors.Join(playbackErr, fmt.Errorf("render playback failure: %w", renderErr))
	}
	return playbackErr
}
