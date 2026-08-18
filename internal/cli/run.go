package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/miclle/say/internal/document"
	"github.com/miclle/say/internal/player"
	"github.com/miclle/say/internal/terminal"
	"github.com/miclle/say/internal/textchunk"
	"github.com/miclle/say/internal/tts"
)

const defaultMaxChars = 500

type speakerFactory func(voice string, rate int) (tts.Speaker, error)
type colorDetector func(writer io.Writer) bool

// Run executes the say command and returns a process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, tts.NewSystem, isCharacterDevice)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, newSpeaker speakerFactory, supportsColor colorDetector) int {
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "say: interrupted: %v\n", err)
		return 130
	}

	flags := flag.NewFlagSet("say", flag.ContinueOnError)
	flags.SetOutput(stderr)
	voice := flags.String("voice", "", "system voice name (default: System Settings)")
	rate := flags.Int("rate", 0, "speech rate in words per minute (default: system rate)")
	maxChars := flags.Int("max-chars", defaultMaxChars, "maximum Unicode characters per TTS call")
	noColor := flags.Bool("no-color", false, "disable ANSI terminal colors")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: say [flags] <document>")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Read a UTF-8 text document, print each speech unit, and play it with system TTS.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Flags:")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *maxChars <= 0 {
		fmt.Fprintln(stderr, "say: max-chars must be greater than zero")
		return 2
	}
	if *rate < 0 {
		fmt.Fprintln(stderr, "say: rate must not be negative")
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "say: exactly one document path is required")
		flags.Usage()
		return 2
	}

	title, text, err := document.Read(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "say: %v\n", err)
		return 1
	}
	chunks, err := textchunk.Split(text, *maxChars)
	if err != nil {
		fmt.Fprintf(stderr, "say: split document: %v\n", err)
		return 1
	}
	speaker, err := newSpeaker(*voice, *rate)
	if err != nil {
		fmt.Fprintf(stderr, "say: initialize TTS: %v\n", err)
		return 1
	}

	view := terminal.New(stdout, !*noColor && supportsColor(stdout), title, speaker.Name())
	if err := player.Play(ctx, chunks, speaker, view); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "say: playback interrupted: %v\n", err)
			return 130
		}
		fmt.Fprintf(stderr, "say: playback failed: %v\n", err)
		return 1
	}
	return 0
}

func isCharacterDevice(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
