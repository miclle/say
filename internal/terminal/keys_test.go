package terminal

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/miclle/say/internal/player"
)

func TestReadCommandsDecodesSpaceAndArrowKeys(t *testing.T) {
	input := &oneByteReader{reader: bytes.NewBufferString("x \x1b[D?\x1b[C \x1bOD\x1bOC\x1b[A\x1b[B\x1bOA\x1bOB")}
	var got []player.Command
	for command := range ReadCommands(context.Background(), input) {
		got = append(got, command)
	}
	want := []player.Command{
		player.Toggle,
		player.Backward,
		player.Forward,
		player.Toggle,
		player.Backward,
		player.Forward,
		player.PreviousChapter,
		player.NextChapter,
		player.PreviousChapter,
		player.NextChapter,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

func TestReadCommandsIgnoresIncompleteEscapeSequenceAtEOF(t *testing.T) {
	var got []player.Command
	for command := range ReadCommands(context.Background(), bytes.NewBufferString(" \x1b[")) {
		got = append(got, command)
	}
	if want := []player.Command{player.Toggle}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

type oneByteReader struct{ reader *bytes.Buffer }

func (r *oneByteReader) Read(p []byte) (int, error) {
	return r.reader.Read(p[:1])
}
