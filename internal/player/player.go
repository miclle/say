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

// Track binds one displayed text unit to its synthesized audio file.
type Track struct {
	Text     string
	Path     string
	Duration time.Duration
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

// View receives playback lifecycle and control events.
type View interface {
	Start(total int) error
	Speaking(index, total int, text string) error
	Spoken(index, total int) error
	Paused(index, total int) error
	Resumed(index, total int) error
	Seeked(index, total int, delta, position, duration time.Duration) error
	Failed(index, total int, err error) error
	Finish(total int) error
}

// Play renders and controls synthesized audio tracks.
func Play(ctx context.Context, tracks []Track, transport Transport, commands <-chan Command, view View) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	return play(ctx, tracks, transport, commands, view, ticker.C)
}

func play(ctx context.Context, tracks []Track, transport Transport, commands <-chan Command, view View, ticks <-chan time.Time) error {
	if err := validate(ctx, tracks, transport, view); err != nil {
		return err
	}

	total := len(tracks)
	offsets := make([]time.Duration, total+1)
	for i, track := range tracks {
		if track.Duration > time.Duration(1<<63-1)-offsets[i] {
			return fmt.Errorf("total audio duration overflows")
		}
		offsets[i+1] = offsets[i] + track.Duration
	}

	if err := view.Start(total); err != nil {
		return fmt.Errorf("render playback header: %w", err)
	}
	current := 0
	playing := true
	if err := activateTrack(transport, view, tracks, current, 0, playing); err != nil {
		return reportFailure(view, current, total, err)
	}

	for {
		select {
		case <-ctx.Done():
			return reportFailure(view, current, total, ctx.Err())
		case command, ok := <-commands:
			if !ok {
				commands = nil
				continue
			}
			switch command {
			case Toggle:
				if playing {
					transport.Pause()
					playing = false
					if err := view.Paused(current, total); err != nil {
						return fmt.Errorf("render paused state: %w", err)
					}
				} else {
					if err := transport.Play(); err != nil {
						return reportFailure(view, current, total, fmt.Errorf("resume track %d of %d: %w", current+1, total, err))
					}
					playing = true
					if err := view.Resumed(current, total); err != nil {
						return fmt.Errorf("render resumed state: %w", err)
					}
				}
			case Backward, Forward:
				delta := -seekStep
				if command == Forward {
					delta = seekStep
				}
				next, finished, err := seek(tracks, offsets, transport, view, current, playing, delta)
				if err != nil {
					return reportFailure(view, current, total, err)
				}
				current = next
				if finished {
					if err := view.Spoken(current, total); err != nil {
						return fmt.Errorf("render completion for track %d of %d: %w", current+1, total, err)
					}
					if err := view.Finish(total); err != nil {
						return fmt.Errorf("render playback summary: %w", err)
					}
					return nil
				}
			}
		case <-ticks:
			if !playing || transport.IsPlaying() {
				continue
			}
			if err := view.Spoken(current, total); err != nil {
				return fmt.Errorf("render completion for track %d of %d: %w", current+1, total, err)
			}
			if current == total-1 {
				if err := view.Finish(total); err != nil {
					return fmt.Errorf("render playback summary: %w", err)
				}
				return nil
			}
			current++
			if err := activateTrack(transport, view, tracks, current, 0, true); err != nil {
				return reportFailure(view, current, total, err)
			}
		}
	}
}

func validate(ctx context.Context, tracks []Track, transport Transport, view View) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transport == nil {
		return fmt.Errorf("audio transport is required")
	}
	if view == nil {
		return fmt.Errorf("view is required")
	}
	if len(tracks) == 0 {
		return fmt.Errorf("at least one audio track is required")
	}
	for i, track := range tracks {
		if strings.TrimSpace(track.Text) == "" {
			return fmt.Errorf("track %d text is empty", i+1)
		}
		if strings.TrimSpace(track.Path) == "" {
			return fmt.Errorf("track %d path is empty", i+1)
		}
		if track.Duration <= 0 {
			return fmt.Errorf("track %d duration must be greater than zero", i+1)
		}
	}
	return nil
}

func activateTrack(transport Transport, view View, tracks []Track, index int, position time.Duration, playing bool) error {
	if err := transport.Load(tracks[index].Path); err != nil {
		return fmt.Errorf("load track %d of %d: %w", index+1, len(tracks), err)
	}
	if err := view.Speaking(index, len(tracks), tracks[index].Text); err != nil {
		return fmt.Errorf("render track %d of %d before playback: %w", index+1, len(tracks), err)
	}
	if position > 0 {
		if err := transport.Seek(position); err != nil {
			return fmt.Errorf("seek track %d of %d: %w", index+1, len(tracks), err)
		}
	}
	if playing {
		if err := transport.Play(); err != nil {
			return fmt.Errorf("play track %d of %d: %w", index+1, len(tracks), err)
		}
	}
	return nil
}

func seek(tracks []Track, offsets []time.Duration, transport Transport, view View, current int, playing bool, delta time.Duration) (int, bool, error) {
	position := transport.Position()
	if position < 0 {
		position = 0
	}
	if position > tracks[current].Duration {
		position = tracks[current].Duration
	}
	absolute := offsets[current] + position + delta
	if absolute < 0 {
		absolute = 0
	}
	totalDuration := offsets[len(tracks)]
	if absolute >= totalDuration {
		last := len(tracks) - 1
		if current != last {
			if err := activateTrack(transport, view, tracks, last, tracks[last].Duration, false); err != nil {
				return current, false, err
			}
		} else {
			transport.Pause()
			if err := transport.Seek(tracks[last].Duration); err != nil {
				return current, false, fmt.Errorf("seek to document end: %w", err)
			}
		}
		if err := view.Seeked(last, len(tracks), delta, totalDuration, totalDuration); err != nil {
			return current, false, fmt.Errorf("render seek state: %w", err)
		}
		return last, true, nil
	}

	target := 0
	for target+1 < len(offsets) && offsets[target+1] <= absolute {
		target++
	}
	local := absolute - offsets[target]
	if target != current {
		if err := activateTrack(transport, view, tracks, target, local, playing); err != nil {
			return current, false, err
		}
	} else if err := transport.Seek(local); err != nil {
		return current, false, fmt.Errorf("seek track %d of %d: %w", current+1, len(tracks), err)
	}
	if err := view.Seeked(target, len(tracks), delta, absolute, totalDuration); err != nil {
		return current, false, fmt.Errorf("render seek state: %w", err)
	}
	return target, false, nil
}

func reportFailure(view View, index, total int, playbackErr error) error {
	if renderErr := view.Failed(index, total, playbackErr); renderErr != nil {
		return errors.Join(playbackErr, fmt.Errorf("render playback failure: %w", renderErr))
	}
	return playbackErr
}
