package terminal

import (
	"context"
	"fmt"
	"io"
	"strings"
)

const selectionHint = "↑/↓ choose · Enter confirm"

// Select renders a single-line terminal chooser and returns the confirmed
// label index. The caller is responsible for enabling raw terminal input.
func Select(ctx context.Context, reader io.Reader, writer io.Writer, prompt string, labels []string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if reader == nil {
		return 0, fmt.Errorf("selection input is required")
	}
	if writer == nil {
		return 0, fmt.Errorf("selection output is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return 0, fmt.Errorf("selection prompt is required")
	}
	if len(labels) == 0 {
		return 0, fmt.Errorf("at least one selection option is required")
	}

	selected := 0
	if err := renderSelection(writer, prompt, labels[selected], false); err != nil {
		return 0, err
	}
	state := byte(0)
	for {
		key, err := readSelectionByte(ctx, reader)
		if err != nil {
			fmt.Fprintln(writer)
			return 0, err
		}
		switch state {
		case 0:
			switch key {
			case '\r', '\n':
				if _, err := fmt.Fprintln(writer); err != nil {
					return 0, fmt.Errorf("render selection confirmation: %w", err)
				}
				return selected, nil
			case 0x03:
				fmt.Fprintln(writer)
				return 0, context.Canceled
			case 0x1b:
				state = 1
			}
		case 1:
			if key == '[' || key == 'O' {
				state = 2
			} else {
				state = 0
			}
		case 2:
			previous := selected
			switch key {
			case 'A':
				selected = (selected - 1 + len(labels)) % len(labels)
			case 'B':
				selected = (selected + 1) % len(labels)
			}
			state = 0
			if selected != previous {
				if err := renderSelection(writer, prompt, labels[selected], true); err != nil {
					return 0, err
				}
			}
		}
	}
}

func renderSelection(writer io.Writer, prompt, label string, redraw bool) error {
	prefix := ""
	if redraw {
		prefix = "\r\x1b[2K"
	}
	if _, err := fmt.Fprintf(writer, "%s%s  › %s  (%s)", prefix, prompt, label, selectionHint); err != nil {
		return fmt.Errorf("render selection: %w", err)
	}
	return nil
}

func readSelectionByte(ctx context.Context, reader io.Reader) (byte, error) {
	type result struct {
		key byte
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		var buffer [1]byte
		_, err := io.ReadFull(reader, buffer[:])
		resultCh <- result{key: buffer[0], err: err}
	}()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case result := <-resultCh:
		return result.key, result.err
	}
}
