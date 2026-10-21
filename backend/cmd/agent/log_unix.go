//go:build !windows

package main

import (
	"io"
	"os"
)

// logOutput is where the agent's log goes: the desktop session keeps stderr.
func logOutput() io.Writer { return os.Stderr }
