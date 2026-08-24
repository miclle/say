package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
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
	offsets         []time.Duration
	current         int
	currentSentence int
	playing         bool
	active          bool
	spoken          bool
	waiting         bool
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
	if len(p.tracks) > 0 && !p.tracks[len(p.tracks)-1].Complete {
		return p.updateTrack(track)
	}
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

func (p *streamPlayer) updateTrack(track Track) (bool, error) {
	index := len(p.tracks) - 1
	if err := validateTrack(track, index); err != nil {
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
		complete := p.complete()
		knownDuration := p.knownDuration()
		if p.pending.target < knownDuration || complete {
			target := p.pending.target
			delta := p.pending.delta
			p.pending = nil
			return p.performSeek(target, delta, complete)
		}
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
		lastSentence := len(p.tracks[last].Sentences) - 1
		lastPosition := p.tracks[last].Sentences[lastSentence].Duration
		if p.current != last || !p.active || p.currentSentence != lastSentence {
			p.current = last
			if err := p.activateAt(p.current, p.tracks[last].Duration, false, false); err != nil {
				return false, err
			}
		} else if err := p.transport.Seek(lastPosition); err != nil {
			return false, fmt.Errorf("seek to document end: %w", err)
		}
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
	sentence, sentenceLocal := sentencePosition(p.tracks[index], local)
	activateTarget := index != p.current || !p.active || sentence != p.currentSentence
	if activateTarget {
		p.current = index
		if err := p.activateAt(index, local, false, false); err != nil {
			return false, err
		}
	} else if err := p.transport.Seek(sentenceLocal); err != nil {
		return false, fmt.Errorf("seek track %d of %d: %w", index+1, p.total, err)
	}
	p.active = true
	p.waiting = false
	p.spoken = false
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

func (p *streamPlayer) activate(index int, position time.Duration) error {
	return p.activateAt(index, position, p.playing, true)
}

func (p *streamPlayer) activateAt(index int, position time.Duration, shouldPlay, render bool) error {
	track := p.tracks[index]
	sentenceIndex, sentencePosition := sentencePosition(track, position)
	sentence := track.Sentences[sentenceIndex]
	if err := p.transport.Load(sentence.Path); err != nil {
		return fmt.Errorf("load sentence %d of track %d of %d: %w", sentenceIndex+1, index+1, p.total, err)
	}
	if render {
		if err := p.view.Speaking(index, p.total, track.Text); err != nil {
			return fmt.Errorf("render track %d of %d before playback: %w", index+1, p.total, err)
		}
	}
	if sentencePosition > 0 {
		if err := p.transport.Seek(sentencePosition); err != nil {
			return fmt.Errorf("seek sentence %d of track %d of %d: %w", sentenceIndex+1, index+1, p.total, err)
		}
	}
	p.currentSentence = sentenceIndex
	if shouldPlay {
		if err := p.transport.Play(); err != nil {
			return fmt.Errorf("play sentence %d of track %d of %d: %w", sentenceIndex+1, index+1, p.total, err)
		}
	}
	p.active = true
	return nil
}
func (p *streamPlayer) absolutePosition() time.Duration {
	if p.current < 0 {
		return 0
	}
	if p.spoken {
		return p.offsets[p.current+1]
	}
	if p.currentSentence < 0 || p.currentSentence >= len(p.tracks[p.current].Sentences) {
		return p.offsets[p.current]
	}
	track := p.tracks[p.current]
	sentence := track.Sentences[p.currentSentence]
	position := p.transport.Position()
	if position < 0 {
		position = 0
	}
	if position > sentence.Duration {
		position = sentence.Duration
	}
	for i := 0; i < p.currentSentence; i++ {
		position += track.Sentences[i].Duration
	}
	return p.offsets[p.current] + position
}

func sentencePosition(track Track, position time.Duration) (int, time.Duration) {
	if position < 0 {
		position = 0
	}
	for index, sentence := range track.Sentences {
		if index == len(track.Sentences)-1 {
			if position > sentence.Duration {
				position = sentence.Duration
			}
			return index, position
		}
		if position < sentence.Duration {
			return index, position
		}
		position -= sentence.Duration
	}
	return 0, 0
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

func validateTrack(track Track, index int) error {
	if strings.TrimSpace(track.Text) == "" {
		return fmt.Errorf("track %d text is empty", index+1)
	}
	if len(track.Sentences) == 0 {
		return fmt.Errorf("track %d has no sentence audio", index+1)
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
