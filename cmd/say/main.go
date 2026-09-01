package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/miclle/say/internal/cli"
	"github.com/miclle/say/internal/desktop"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	args := os.Args[1:]
	code := 0
	if cli.WantsMenuBar(args) {
		code = desktop.Run(ctx, func(controls desktop.Controls) int {
			if controls == nil {
				return cli.Run(ctx, args, os.Stdout, os.Stderr)
			}
			return cli.RunWithControls(ctx, args, os.Stdout, os.Stderr, controls)
		})
	} else {
		code = cli.Run(ctx, args, os.Stdout, os.Stderr)
	}
	stop()
	os.Exit(code)
}
