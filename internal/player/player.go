package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/miclle/say/internal/textchunk"
)

const (
	pollInterval    = 25 * time.Millisecond
	navigationDelay = 200 * time.Millisecond
)

// Command describes an interactive playback action.
type Command uint8

const (
	// Toggle preserves the playhead while changing playback intent.
	Toggle Command = iota + 1
	// Backward selects the previous sentence.
	Backward
	// Forward selects the next sentence.
	Forward
	// PreviousChapter selects the start of the previous chapter.
	PreviousChapter
	// NextChapter selects the start of the next chapter.
	NextChapter
)

// SentenceTrack binds a source sentence to its synthesized audio.
type SentenceTrack struct {
	Path     string
	Duration time.Duration
}

// Transport controls one active audio file.
type Transport interface {
	Load(string) error
	Play() error
	Pause()
	Seek(time.Duration) error
	Position() time.Duration
	IsPlaying() bool
}

// View separates a text-only selection preview from actual audio activation.
type View interface {
	Prepared(prepared, total int) error
	Start(total int) error
	Speaking(index, total int, text string) error
	Progress(index, total, sentence int) error
	Spoken(index, total int) error
	Paused(index, total int) error
	Resumed(index, total int) error
	Buffering(index, total int) error
	Selected(index, total int, text string, sentence int) error
	Seeked(index, total int, text string, sentence int, playing bool, delta, position, duration time.Duration, complete bool) error
	Failed(index, total int, err error) error
	Finish(total int) error
}

type streamPlayer struct {
	chapters   []string
	texts      [][]string
	source     AudioSource
	transport  Transport
	view       View
	cache      map[Target]SentenceTrack
	target     Target // Latest text selection or automatic playback destination.
	loaded     Target // Last audio loaded; may differ from the preview.
	playing    bool   // User intent, independent of buffering or selection.
	active     bool
	awaiting   bool
	selecting  bool
	navigating bool
	started    bool
	hasLoaded  bool
}

// Play selects by source text and consumes demand-driven sentence audio.
// The caller owns source shutdown; Play always stops the transport on exit.
func Play(ctx context.Context, chapters []string, source AudioSource, transport Transport, commands <-chan Command, view View) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(chapters) == 0 {
		return fmt.Errorf("at least one chapter is required")
	}
	if source == nil || source.Results() == nil {
		return fmt.Errorf("audio result source is required")
	}
	if transport == nil || view == nil {
		return fmt.Errorf("audio transport and view are required")
	}
	texts := make([][]string, len(chapters))
	for i, chapter := range chapters {
		texts[i] = textchunk.Sentences(chapter)
		if len(texts[i]) == 0 {
			return fmt.Errorf("chapter %d has no sentences", i+1)
		}
	}
	p := &streamPlayer{
		chapters: chapters, texts: texts, source: source, transport: transport, view: view,
		cache: make(map[Target]SentenceTrack), playing: true, awaiting: true,
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(navigationDelay)
	timer.Stop()
	defer timer.Stop()
	defer transport.Pause()
	var settled <-chan time.Time
	source.Request(Target{})
	results := source.Results()
	handle := func(command Command) error {
		changed, err := p.handleCommand(command)
		if changed {
			timer.Reset(navigationDelay)
			settled = timer.C
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return p.fail(ctx.Err())
		case result, ok := <-results:
			if !ok {
				if err := ctx.Err(); err != nil {
					return p.fail(err)
				}
				return p.fail(fmt.Errorf("audio source closed before playback finished"))
			}
			if err := p.accept(result); err != nil {
				return p.fail(err)
			}
		case command, ok := <-commands:
			if !ok {
				commands = nil
				continue
			}
			if err := handle(command); err != nil {
				return p.fail(err)
			}
		case <-settled:
			// A queued arrow at the deadline wins over committing the old selection.
			changed := false
		drain:
			for range 32 {
				select {
				case command, ok := <-commands:
					if !ok {
						commands = nil
						break drain
					}
					moved, err := p.handleCommand(command)
					if err != nil {
						return p.fail(err)
					}
					changed = changed || moved
				default:
					break drain
				}
			}
			if changed {
				timer.Reset(navigationDelay)
				continue
			}
			settled = nil
			p.selecting = false
			if err := p.demand(); err != nil {
				return p.fail(err)
			}
		case <-ticker.C:
			finished, err := p.tick()
			if err != nil {
				return p.fail(err)
			}
			if finished {
				return view.Finish(len(chapters))
			}
		}
	}
}

func (p *streamPlayer) accept(result AudioResult) error {
	if !validTarget(p.texts, result.Target) {
		return fmt.Errorf("invalid audio target: %+v", result.Target)
	}
	if result.Err != nil {
		if !p.selecting && p.awaiting && result.Target == p.target {
			return result.Err
		}
		return nil
	}
	if strings.TrimSpace(result.Audio.Path) == "" || result.Audio.Duration <= 0 {
		return fmt.Errorf("invalid sentence audio at chapter %d sentence %d", result.Target.Chapter+1, result.Target.Sentence+1)
	}
	p.cache[result.Target] = result.Audio
	if !p.selecting && p.awaiting && result.Target == p.target {
		return p.activate()
	}
	return nil
}

func (p *streamPlayer) start() error {
	if p.started {
		return nil
	}
	if err := p.view.Start(len(p.chapters)); err != nil {
		return err
	}
	p.started = true
	return nil
}

func (p *streamPlayer) demand() error {
	p.awaiting = true
	p.source.Request(p.target)
	if _, ok := p.cache[p.target]; ok {
		return p.activate()
	}
	return p.view.Buffering(p.target.Chapter, len(p.chapters))
}

func (p *streamPlayer) activate() error {
	audio := p.cache[p.target]
	index, total := p.target.Chapter, len(p.chapters)
	if !p.started {
		if err := p.view.Prepared(1, total); err != nil {
			return err
		}
		if err := p.start(); err != nil {
			return err
		}
	}
	if p.navigating {
		// Navigation is indexed by source text. Unknown earlier audio durations
		// must never be fabricated or required to select a later sentence.
		if err := p.view.Seeked(index, total, p.chapters[index], p.target.Sentence, p.playing, 0, 0, 0, false); err != nil {
			return err
		}
	} else if !p.hasLoaded || p.loaded.Chapter != index {
		if err := p.view.Speaking(index, total, p.chapters[index]); err != nil {
			return err
		}
	} else {
		if err := p.view.Progress(index, total, p.target.Sentence); err != nil {
			return err
		}
	}
	if err := p.transport.Load(audio.Path); err != nil {
		return fmt.Errorf("load chapter %d sentence %d: %w", index+1, p.target.Sentence+1, err)
	}
	p.loaded, p.hasLoaded, p.active, p.awaiting, p.navigating = p.target, true, true, false, false
	if p.playing {
		if err := p.transport.Play(); err != nil {
			return fmt.Errorf("play chapter %d sentence %d: %w", index+1, p.target.Sentence+1, err)
		}
	} else if err := p.view.Paused(index, total); err != nil {
		return err
	}
	return nil
}

func (p *streamPlayer) tick() (bool, error) {
	if p.selecting || !p.playing || !p.active || p.transport.IsPlaying() {
		return false, nil
	}
	p.active = false
	if p.target.Sentence == len(p.texts[p.target.Chapter])-1 {
		if err := p.view.Spoken(p.target.Chapter, len(p.chapters)); err != nil {
			return false, err
		}
	}
	next, ok := following(p.texts, p.target)
	if !ok {
		return true, nil
	}
	p.target = next
	return false, p.demand()
}

func (p *streamPlayer) fail(err error) error {
	if !p.started {
		return err
	}
	if renderErr := p.view.Failed(p.target.Chapter, len(p.chapters), err); renderErr != nil {
		return errors.Join(err, renderErr)
	}
	return err
}
