//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// echoOff stops the terminal showing what is typed (for the password) and returns the undo,
// or nil when stdin is not a terminal.
func echoOff() func() {
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if off.Run() != nil {
		return nil
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = os.Stdin
		on.Run()
	}
}
