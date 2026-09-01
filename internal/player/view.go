package player

import "time"

// CombineViews returns one view that forwards callbacks in order.
// Forwarding stops at the first error so later views never observe a state
// that the primary view failed to render.
func CombineViews(views ...View) View {
	combined := make(combinedView, 0, len(views))
	for _, view := range views {
		if view != nil {
			combined = append(combined, view)
		}
	}
	return combined
}

type combinedView []View

func (views combinedView) each(call func(View) error) error {
	for _, view := range views {
		if err := call(view); err != nil {
			return err
		}
	}
	return nil
}

func (views combinedView) Prepared(prepared, total int) error {
	return views.each(func(view View) error { return view.Prepared(prepared, total) })
}

func (views combinedView) Start(total int) error {
	return views.each(func(view View) error { return view.Start(total) })
}

func (views combinedView) Speaking(index, total int, text string) error {
	return views.each(func(view View) error { return view.Speaking(index, total, text) })
}

func (views combinedView) Progress(index, total, sentence int) error {
	return views.each(func(view View) error { return view.Progress(index, total, sentence) })
}

func (views combinedView) Spoken(index, total int) error {
	return views.each(func(view View) error { return view.Spoken(index, total) })
}

func (views combinedView) Paused(index, total int) error {
	return views.each(func(view View) error { return view.Paused(index, total) })
}

func (views combinedView) Resumed(index, total int) error {
	return views.each(func(view View) error { return view.Resumed(index, total) })
}

func (views combinedView) Buffering(index, total int) error {
	return views.each(func(view View) error { return view.Buffering(index, total) })
}

func (views combinedView) Selected(index, total int, text string, sentence int) error {
	return views.each(func(view View) error { return view.Selected(index, total, text, sentence) })
}

func (views combinedView) Seeked(index, total int, text string, sentence int, playing bool, delta, position, duration time.Duration, complete bool) error {
	return views.each(func(view View) error {
		return view.Seeked(index, total, text, sentence, playing, delta, position, duration, complete)
	})
}

func (views combinedView) Failed(index, total int, err error) error {
	return views.each(func(view View) error { return view.Failed(index, total, err) })
}

func (views combinedView) Finish(total int) error {
	return views.each(func(view View) error { return view.Finish(total) })
}

func (views combinedView) Track(index, total, sentence int, text string, playing bool, position, duration time.Duration) error {
	return views.each(func(view View) error {
		details, ok := view.(TrackView)
		if !ok {
			return nil
		}
		return details.Track(index, total, sentence, text, playing, position, duration)
	})
}
