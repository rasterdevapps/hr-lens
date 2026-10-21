//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	setWindowsHook                  = user32.NewProc("SetWindowsHookExW")
	unhookWindowsHook               = user32.NewProc("UnhookWindowsHookEx")
	callNextHook                    = user32.NewProc("CallNextHookEx")
	peekMessage                     = user32.NewProc("PeekMessageW")
	postThreadMessage               = user32.NewProc("PostThreadMessageW")
	currentThreadID                 = kernel32.NewProc("GetCurrentThreadId")
	inputCallbacksOnce              sync.Once
	keyboardCallback, mouseCallback uintptr
	currentInputCounts              atomic.Pointer[hookCounts]
)

// Native callbacks inspect only injection flags and message kind. Key codes,
// scan codes, text, cursor coordinates and mouse data are never copied or stored.
type keyboardFlags struct {
	_     [8]byte
	Flags uint32
}
type mouseFlags struct {
	_     [12]byte
	Flags uint32
}
type hookCounts struct {
	allowed      atomic.Bool
	keys, clicks atomic.Int64
}

func keyCountable(msg uintptr, flags uint32) bool {
	return (msg == 0x0100 || msg == 0x0104) && flags&(0x10|0x02) == 0
}
func clickCountable(msg uintptr, flags uint32) bool {
	return (msg == 0x0201 || msg == 0x0204 || msg == 0x0207 || msg == 0x020b) && flags&3 == 0
}
func keyboardCountProc(code int32, msg, data uintptr) uintptr {
	if code == 0 && data != 0 {
		if c := currentInputCounts.Load(); c != nil && c.allowed.Load() && keyCountable(msg, (*keyboardFlags)(unsafe.Pointer(data)).Flags) {
			c.keys.Add(1)
		}
	}
	next, _, _ := callNextHook.Call(0, uintptr(code), msg, data)
	return next
}
func mouseCountProc(code int32, msg, data uintptr) uintptr {
	if code == 0 && data != 0 {
		if c := currentInputCounts.Load(); c != nil && c.allowed.Load() && clickCountable(msg, (*mouseFlags)(unsafe.Pointer(data)).Flags) {
			c.clicks.Add(1)
		}
	}
	next, _, _ := callNextHook.Call(0, uintptr(code), msg, data)
	return next
}

func (s *screen) watchInputReady(ctx context.Context, count func(int, int), ready func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	counts := &hookCounts{}
	if !currentInputCounts.CompareAndSwap(nil, counts) {
		return errors.New("input counter already running")
	}
	defer currentInputCounts.Store(nil)
	inputCallbacksOnce.Do(func() {
		keyboardCallback = syscall.NewCallback(keyboardCountProc)
		mouseCallback = syscall.NewCallback(mouseCountProc)
	})
	// Prime this thread's queue before cancellation can post WM_QUIT.
	var msg winMsg
	peekMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0)
	tid, _, _ := currentThreadID.Call()
	module, _, _ := procGetModuleHandleW.Call(0)
	keyboard, _, err := setWindowsHook.Call(13, keyboardCallback, module, 0)
	if keyboard == 0 {
		return fmt.Errorf("install aggregate keyboard counter: %w", err)
	}
	defer unhookWindowsHook.Call(keyboard)
	mouse, _, err := setWindowsHook.Call(14, mouseCallback, module, 0)
	if mouse == 0 {
		return fmt.Errorf("install aggregate mouse counter: %w", err)
	}
	defer unhookWindowsHook.Call(mouse)
	finished := make(chan struct{})
	controllerDone := make(chan struct{})
	defer func() { counts.allowed.Store(false); close(finished); <-controllerDone }()
	go func() {
		defer close(controllerDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-finished:
				return
			case <-ctx.Done():
				counts.allowed.Store(false)
				// A full message queue can reject a post temporarily; retry without blocking callbacks.
				for {
					if ok, _, _ := postThreadMessage.Call(tid, 0x0012, 0, 0); ok != 0 {
						return
					}
					select {
					case <-finished:
						return
					case <-ticker.C:
					}
				}
			case <-ticker.C:
				allowed := ctx.Err() == nil && !s.captureSuspended()
				counts.allowed.Store(allowed)
				keys, clicks := counts.keys.Swap(0), counts.clicks.Swap(0)
				if allowed && (keys > 0 || clicks > 0) {
					count(int(keys), int(clicks))
				}
			}
		}
	}()
	if ready != nil {
		ready()
	}
	for {
		result, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("input message pump: %w", err)
		}
		if result == 0 {
			return ctx.Err()
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
