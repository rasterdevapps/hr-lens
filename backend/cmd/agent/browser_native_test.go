package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNativeFramingAndBounds(t *testing.T) {
	data := []byte(`{"title":"Synthetic tab","host":"example.com"}`)
	var buf bytes.Buffer
	if err := writeNative(&buf, data); err != nil {
		t.Fatal(err)
	}
	got, err := readNative(&buf)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("roundtrip: %s %v", got, err)
	}
	if _, err := parseTab(got); err != nil {
		t.Fatal(err)
	}
	for _, size := range []uint32{0, maxTabMessage + 1, 0xffffffff} {
		buf.Reset()
		binary.Write(&buf, binary.LittleEndian, size)
		if _, err := readNative(&buf); err == nil {
			t.Fatalf("accepted length %d", size)
		}
	}
	if _, err := parseTab([]byte(`{"title":"Tab","host":"https://example.com/private?token=secret"}`)); err == nil {
		t.Fatal("accepted full URL")
	}
}
