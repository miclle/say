package terminal

import (
	"bufio"
	"context"
	"io"

	"github.com/miclle/say/internal/player"
)

// ReadCommands decodes supported key sequences until input ends or context is canceled.
func ReadCommands(ctx context.Context, reader io.Reader) <-chan player.Command {
	commands := make(chan player.Command)
	go func() {
		defer close(commands)
		input := bufio.NewReader(reader)
		state := byte(0)
		for {
			key, err := input.ReadByte()
			if err != nil {
				return
			}
			var command player.Command
			var emit bool
			switch state {
			case 0:
				switch key {
				case ' ':
					command, emit = player.Toggle, true
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
				switch key {
				case 'D':
					command, emit = player.Backward, true
				case 'C':
					command, emit = player.Forward, true
				}
				state = 0
			}
			if emit {
				select {
				case commands <- command:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return commands
}
