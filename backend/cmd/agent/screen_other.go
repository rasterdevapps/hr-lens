//go:build !linux && !windows

package main

import (
	"context"
	"errors"
	"time"
)

// Screen capture is built for Linux (X11) only; macOS comes later.

const canCapture = false

type window struct{ app, appName, title string }

type screen struct{}

func newScreen() (*screen, error) {
	return nil, errors.New("the HR Lens agent supports Linux (X11) only for now")
}

func (s *screen) locked() bool                                       { return false }
func (s *screen) activeWindow() (window, error)                      { return window{}, errors.ErrUnsupported }
func (s *screen) screenshot(context.Context, string, int, int) error { return errors.ErrUnsupported }
func (s *screen) record(context.Context, string, time.Duration, func() bool) error {
	return errors.ErrUnsupported
}
func (s *screen) watchInput(context.Context, func(), func()) error { return errors.ErrUnsupported }
func (s *screen) sessionStart() (time.Time, bool)                  { return time.Time{}, false }
func singleInstance() (func(), error)                              { return func() {}, nil }
