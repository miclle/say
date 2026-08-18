//go:build !darwin

package terminal

import (
	"fmt"
	"os"
)

// BeginRawInput reports that interactive playback is currently macOS-only.
func BeginRawInput(*os.File) (func() error, error) {
	return nil, fmt.Errorf("raw playback controls are supported only on macOS")
}
