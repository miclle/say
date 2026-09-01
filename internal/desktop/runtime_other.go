//go:build !darwin || !cgo

package desktop

import "context"

// Run executes without desktop controls when AppKit is unavailable.
func Run(_ context.Context, work func(Controls) int) int {
	return work(nil)
}
