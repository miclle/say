package player

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/miclle/say/internal/tts"
)

// View receives playback lifecycle events in display order.
type View interface {
	Start(total int) error
	Speaking(index, total int, text string) error
	Spoken(index, total int) error
	Failed(index, total int, err error) error
	Finish(total int) error
}

// Play renders and speaks each chunk sequentially.
func Play(ctx context.Context, chunks []string, speaker tts.Speaker, view View) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if speaker == nil {
		return fmt.Errorf("speaker is required")
	}
	if view == nil {
		return fmt.Errorf("view is required")
	}
	if len(chunks) == 0 {
		return fmt.Errorf("at least one speech chunk is required")
	}
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			return fmt.Errorf("chunk %d is empty", i+1)
		}
	}

	if err := view.Start(len(chunks)); err != nil {
		return fmt.Errorf("render playback header: %w", err)
	}
	for i, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			if renderErr := view.Failed(i, len(chunks), err); renderErr != nil {
				return errors.Join(err, fmt.Errorf("render playback failure: %w", renderErr))
			}
			return err
		}

		if err := view.Speaking(i, len(chunks), chunk); err != nil {
			return fmt.Errorf("render chunk %d of %d before speech: %w", i+1, len(chunks), err)
		}
		if err := speaker.Speak(ctx, chunk); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
			playbackErr := fmt.Errorf("speak chunk %d of %d: %w", i+1, len(chunks), err)
			if renderErr := view.Failed(i, len(chunks), err); renderErr != nil {
				return errors.Join(playbackErr, fmt.Errorf("render playback failure: %w", renderErr))
			}
			return playbackErr
		}
		if err := view.Spoken(i, len(chunks)); err != nil {
			return fmt.Errorf("render completion for chunk %d of %d: %w", i+1, len(chunks), err)
		}
	}
	if err := view.Finish(len(chunks)); err != nil {
		return fmt.Errorf("render playback summary: %w", err)
	}
	return nil
}
