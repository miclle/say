package player

import (
	"context"
	"fmt"
	"strings"

	"github.com/miclle/say/internal/textchunk"
)

// Target identifies a sentence using source-text boundaries, not audio time.
type Target struct{ Chapter, Sentence int }

// AudioResult can arrive in any document order.
type AudioResult struct {
	Target Target
	Audio  SentenceTrack
	Err    error
}

// AudioSource accepts the latest playback intent without blocking input.
type AudioSource interface {
	Request(Target)
	Suspend()
	Results() <-chan AudioResult
}

// PrepareSentence synthesizes one sentence and returns its audio metadata.
// Implementations must honor cancellation and clean up partial output.
type PrepareSentence func(context.Context, Target, string) (SentenceTrack, error)

// Preparation owns one cancellable worker and a session-local audio cache.
// Close must complete before the caller removes the worker's audio directory.
type Preparation struct {
	requests chan *Target
	results  chan AudioResult
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewPreparation starts an idle scheduler; Request starts preparation.
func NewPreparation(ctx context.Context, chapters []string, prepare PrepareSentence) *Preparation {
	ctx, cancel := context.WithCancel(ctx)
	p := &Preparation{requests: make(chan *Target, 1), results: make(chan AudioResult), cancel: cancel, done: make(chan struct{})}
	texts := make([][]string, len(chapters))
	for i, chapter := range chapters {
		texts[i] = textchunk.Sentences(chapter)
	}
	go p.run(ctx, texts, prepare)
	return p
}

// Results returns indexed audio and demanded failures until Close.
func (p *Preparation) Results() <-chan AudioResult { return p.results }

// Request prioritizes a target and replaces the lookahead window.
func (p *Preparation) Request(target Target) { p.replace(&target) }

// Suspend cancels in-flight work and stops preparation until the next Request.
func (p *Preparation) Suspend() { p.replace(nil) }

// Close cancels and joins the worker. It is safe to call more than once.
func (p *Preparation) Close() { p.cancel(); <-p.done }

// The player is the single request writer. Keep only its latest intent.
func (p *Preparation) replace(target *Target) {
	select {
	case <-p.done:
		return
	default:
	}
	select {
	case <-p.requests:
	default:
	}
	select {
	case p.requests <- target:
	case <-p.done:
	}
}

func validTarget(texts [][]string, target Target) bool {
	return target.Chapter >= 0 && target.Chapter < len(texts) && target.Sentence >= 0 && target.Sentence < len(texts[target.Chapter])
}

func following(texts [][]string, target Target) (Target, bool) {
	target.Sentence++
	if target.Sentence >= len(texts[target.Chapter]) {
		target.Chapter++
		target.Sentence = 0
	}
	return target, validTarget(texts, target)
}

func preparationWindow(texts [][]string, target Target) []Target {
	var targets []Target
	for range 4 { // Current sentence plus three following sentences.
		if !validTarget(texts, target) {
			break
		}
		targets = append(targets, target)
		target, _ = following(texts, target)
	}
	return targets
}

type preparedJob struct {
	AudioResult
	canceled bool
}

func (p *Preparation) run(ctx context.Context, texts [][]string, prepare PrepareSentence) {
	defer close(p.done)
	defer close(p.results)
	cache := make(map[Target]AudioResult)
	completed := make(chan preparedJob, 1)
	var wanted *Target
	var window []Target
	var output []AudioResult
	var running *Target
	var cancelJob context.CancelFunc
	defer func() {
		if cancelJob != nil {
			cancelJob()
			<-completed
		}
	}()

	apply := func(target *Target) {
		wanted, window, output = target, nil, nil
		if target != nil {
			window = preparationWindow(texts, *target)
			if len(window) == 0 {
				output = append(output, AudioResult{Target: *target, Err: fmt.Errorf("invalid audio target: %+v", *target)})
			}
			for _, key := range window {
				if result, ok := cache[key]; ok && (result.Err == nil || key == *target) {
					output = append(output, result)
				}
			}
		}
		if running != nil {
			keep := false
			for _, key := range window {
				if key == *running {
					keep = true
				}
			}
			if target != nil {
				if _, ready := cache[*target]; !ready && *running != *target {
					keep = false
				}
			}
			if !keep {
				cancelJob()
			}
		}
	}
	for {
		// Observe queued selection changes before starting another prefetch job.
		select {
		case target := <-p.requests:
			apply(target)
		default:
		}
		if ctx.Err() != nil {
			return
		}
		if running == nil {
			for _, target := range window {
				if _, ok := cache[target]; ok {
					continue
				}
				jobCtx, cancel := context.WithCancel(ctx)
				cancelJob, running = cancel, &target
				go func() {
					audio, err := prepare(jobCtx, target, texts[target.Chapter][target.Sentence])
					if err == nil && (strings.TrimSpace(audio.Path) == "" || audio.Duration <= 0) {
						err = fmt.Errorf("invalid sentence audio at chapter %d sentence %d", target.Chapter+1, target.Sentence+1)
					}
					completed <- preparedJob{AudioResult: AudioResult{Target: target, Audio: audio, Err: err}, canceled: jobCtx.Err() != nil}
				}()
				break
			}
		}
		var send chan AudioResult
		var next AudioResult
		if len(output) > 0 {
			send, next = p.results, output[0]
		}
		select {
		case <-ctx.Done():
			return
		case target := <-p.requests:
			apply(target)
		case result := <-completed:
			cancelJob()
			cancelJob, running = nil, nil
			if result.canceled {
				continue
			}
			cache[result.Target] = result.AudioResult
			for _, key := range window {
				if key == result.Target && (result.Err == nil || wanted != nil && key == *wanted) {
					output = append(output, result.AudioResult)
					break
				}
			}
		case send <- next:
			output = output[1:]
		}
	}
}
