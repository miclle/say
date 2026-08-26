package player

import "fmt"

func (p *streamPlayer) navigationPosition() navigationTarget {
	if p.pending != nil {
		return *p.pending
	}
	sentence := p.currentSentence
	if p.spoken {
		sentence = len(p.tracks[p.current].Sentences) - 1
	}
	return navigationTarget{chapter: p.current, sentence: max(0, sentence)}
}

func (p *streamPlayer) requestSentence(direction int) (bool, error) {
	from := p.navigationPosition()
	target := from
	target.sentence += direction
	target = p.normalizeTarget(target)
	if target == from && p.pending == nil {
		return false, nil
	}
	p.pending = &target
	p.waiting = false
	return p.resolveNavigation()
}

func (p *streamPlayer) requestChapter(direction int) (bool, error) {
	from := p.navigationPosition()
	chapter := max(0, min(p.total-1, from.chapter+direction))
	if chapter == from.chapter {
		return false, nil
	}
	p.pending = &navigationTarget{chapter: chapter}
	p.waiting = false
	return p.resolveNavigation()
}

// All chapter boundaries are known from the source text before synthesis.
// Mixed sentence/chapter keys must not depend on which audio is ready yet.
func (p *streamPlayer) normalizeTarget(target navigationTarget) navigationTarget {
	for target.sentence < 0 && target.chapter > 0 {
		target.chapter--
		target.sentence += p.sentenceCounts[target.chapter]
	}
	if target.chapter == 0 && target.sentence < 0 {
		target.sentence = 0
	}
	for target.sentence >= p.sentenceCounts[target.chapter] {
		count := p.sentenceCounts[target.chapter]
		if target.chapter == p.total-1 {
			target.sentence = count - 1
			break
		}
		target.sentence -= count
		target.chapter++
	}
	return target
}

func (p *streamPlayer) resolveNavigation() (bool, error) {
	target := p.normalizeTarget(*p.pending)
	p.pending = &target
	if target.chapter >= len(p.tracks) || target.sentence < 0 || target.sentence >= len(p.tracks[target.chapter].Sentences) {
		// Give immediate feedback, before a native pause can block the loop.
		if err := p.view.Buffering(target.chapter, p.total); err != nil {
			return false, fmt.Errorf("render navigation buffering state: %w", err)
		}
		if p.active {
			p.transport.Pause()
			p.active = false
		}
		return false, nil
	}
	p.pending = nil
	track := p.tracks[target.chapter]
	position := p.offsets[target.chapter]
	for _, sentence := range track.Sentences[:target.sentence] {
		position += sentence.Duration
	}
	// Selection is cheap to render; do not make keyboard feedback wait for
	// native file preparation or stopping the old audio.
	if err := p.view.Seeked(target.chapter, p.total, track.Text, target.sentence, p.playing, 0, position, p.knownDuration(), p.complete()); err != nil {
		return false, fmt.Errorf("render navigation state: %w", err)
	}
	p.current = target.chapter
	p.currentSentence = target.sentence
	p.spoken = false
	p.active = false
	if err := p.transport.Load(track.Sentences[target.sentence].Path); err != nil {
		return false, fmt.Errorf("load sentence %d of track %d of %d: %w", target.sentence+1, target.chapter+1, p.total, err)
	}
	p.active = true
	if p.playing {
		if err := p.transport.Play(); err != nil {
			return false, fmt.Errorf("play track %d of %d after navigation: %w", target.chapter+1, p.total, err)
		}
	}
	return false, nil
}
