//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func tabPipeName(sid string, session uint32) string {
	return fmt.Sprintf(`\\.\pipe\HRLens-%s-%d-tabs`, sid, session)
}
func tabIdentity() (string, uint32, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", 0, err
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return "", 0, err
	}
	return user.User.Sid.String(), session, nil
}
func pipeSameSession(conn net.Conn, want uint32, client bool) bool {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	var pid, session uint32
	var err error
	if client {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(fd.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(fd.Fd()), &pid)
	}
	return err == nil && windows.ProcessIdToSessionId(pid, &session) == nil && session == want
}

// Per-user ACL, per-session address, local clients only (go-winio) and OS-verified
// peer session prevent browser reports crossing simultaneous Windows sessions.
func (a *agent) serveTabs(ctx context.Context) {
	sid, session, err := tabIdentity()
	if err != nil {
		a.log.Warn("browser identity unavailable", "error", err)
		return
	}
	ln, err := winio.ListenPipe(tabPipeName(sid, session), &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)", InputBufferSize: maxTabMessage, OutputBufferSize: maxTabMessage})
	if err != nil {
		a.log.Warn("browser link unavailable", "error", err)
		return
	}
	defer ln.Close()
	go func() { <-ctx.Done(); ln.Close() }()
	slots := make(chan struct{}, 8)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if !pipeSameSession(conn, session, true) {
			conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		go func() {
			defer func() { conn.Close(); <-slots }()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			data, err := readNative(conn)
			if err != nil {
				return
			}
			report, err := parseTab(data)
			if err != nil {
				return
			}
			a.tabs.put(report.Title, report.Host)
			_ = writeNative(conn, []byte(`{"ok":true}`))
		}()
	}
}

// Chrome/Edge launch the installed GUI binary with the allowed extension origin
// as argv[1]. Inherited stdin/stdout carry only framed JSON, never log messages.
func runNativeHost(args []string) (bool, error) {
	if len(args) == 0 || args[0] != extensionOrigin {
		return false, nil
	}
	sid, session, err := tabIdentity()
	if err != nil {
		return true, err
	}
	for {
		data, err := readNative(os.Stdin)
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if err != nil {
			return true, err
		}
		if _, err := parseTab(data); err != nil {
			return true, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := winio.DialPipeContext(ctx, tabPipeName(sid, session))
		cancel()
		if err != nil {
			return true, err
		}
		if !pipeSameSession(conn, session, false) {
			conn.Close()
			return true, errors.New("browser agent session mismatch")
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		err = writeNative(conn, data)
		var response []byte
		if err == nil {
			response, err = readNative(conn)
		}
		conn.Close()
		if err != nil {
			return true, err
		}
		if err := writeNative(os.Stdout, response); err != nil {
			return true, err
		}
	}
}

var nativeRegistryPaths = []string{`Software\Google\Chrome\NativeMessagingHosts\` + nativeHostName, `Software\Microsoft\Edge\NativeMessagingHosts\` + nativeHostName}

func setupBrowserHost(binary string) error {
	manifest := filepath.Join(filepath.Dir(binary), "native-host.json")
	data, err := json.MarshalIndent(map[string]any{"name": nativeHostName, "description": "HR Lens browser domain bridge", "path": binary, "type": "stdio", "allowed_origins": []string{extensionOrigin}}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		return err
	}
	root := registry.CURRENT_USER
	if isAdmin() {
		root = registry.LOCAL_MACHINE
	}
	for _, path := range nativeRegistryPaths {
		k, _, err := registry.CreateKey(root, path, registry.SET_VALUE)
		if err != nil {
			return err
		}
		err = k.SetStringValue("", manifest)
		k.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func removeBrowserHost() error {
	root := registry.CURRENT_USER
	if isAdmin() {
		root = registry.LOCAL_MACHINE
	}
	for _, path := range nativeRegistryPaths {
		if err := registry.DeleteKey(root, path); err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return err
		}
	}
	return nil
}
