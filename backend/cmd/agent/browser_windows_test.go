//go:build windows

package main

import (
	"context"
	"fmt"
	"github.com/Microsoft/go-winio"
	"os"
	"testing"
	"time"
)

func TestBrowserPipeIsolation(t *testing.T) {
	a := tabPipeName("S-1-5-21-111", 1)
	if a == tabPipeName("S-1-5-21-111", 2) || a == tabPipeName("S-1-5-21-222", 1) {
		t.Fatal("browser pipe crosses user/session boundaries")
	}
}

func TestNativePipeSessionAndFraming(t *testing.T) {
	sid, session, err := tabIdentity()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%s-test-%d-%d", tabPipeName(sid, session), os.Getpid(), time.Now().UnixNano())
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if !pipeSameSession(conn, session, true) || pipeSameSession(conn, session+1, true) {
			done <- fmt.Errorf("invalid client session check")
			return
		}
		msg, err := readNative(conn)
		if err != nil {
			done <- err
			return
		}
		if _, err := parseTab(msg); err != nil {
			done <- err
			return
		}
		done <- writeNative(conn, []byte(`{"ok":true}`))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if !pipeSameSession(conn, session, false) || pipeSameSession(conn, session+1, false) {
		t.Fatal("invalid server session check")
	}
	if err := writeNative(conn, []byte(`{"title":"Synthetic","host":"example.com"}`)); err != nil {
		t.Fatal(err)
	}
	data, err := readNative(conn)
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("pipe response %s: %v", data, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
