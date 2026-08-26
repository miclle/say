package player

func (p *streamPlayer) handleCommand(command Command) (bool, error) {
	if command == Toggle {
		p.playing = !p.playing
		if p.active {
			if p.playing {
				if err := p.transport.Play(); err != nil {
					return false, err
				}
			} else {
				p.transport.Pause()
			}
		}
		if !p.started {
			return false, nil
		}
		if p.selecting {
			return false, p.view.Selected(p.target.Chapter, len(p.chapters), p.chapters[p.target.Chapter], p.target.Sentence)
		}
		if p.playing {
			return false, p.view.Resumed(p.target.Chapter, len(p.chapters))
		}
		return false, p.view.Paused(p.target.Chapter, len(p.chapters))
	}
	target := p.target
	switch command {
	case Backward:
		if target.Sentence > 0 {
			target.Sentence--
		} else if target.Chapter > 0 {
			target.Chapter--
			target.Sentence = len(p.texts[target.Chapter]) - 1
		}
	case Forward:
		if next, ok := following(p.texts, target); ok {
			target = next
		}
	case PreviousChapter:
		if target.Chapter > 0 {
			target.Chapter--
			target.Sentence = 0
		}
	case NextChapter:
		if target.Chapter+1 < len(p.texts) {
			target.Chapter++
			target.Sentence = 0
		}
	default:
		return false, nil
	}
	if target == p.target && !p.selecting {
		return false, nil
	}
	p.target = target
	p.selecting, p.navigating, p.awaiting = true, true, false
	if err := p.start(); err != nil {
		return false, err
	}
	if err := p.view.Selected(target.Chapter, len(p.chapters), p.chapters[target.Chapter], target.Sentence); err != nil {
		return false, err
	}
	if p.active {
		p.transport.Pause()
		p.active = false
	}
	p.source.Suspend()
	return true, nil
}
