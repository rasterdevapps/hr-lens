package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const nativeHostName = "com.hrlens.agent"
const extensionOrigin = "chrome-extension://jobmeopcpnhamneamceglchglicfhdlk/"
const maxTabMessage = 8192

type tabReport struct {
	Title string `json:"title"`
	Host  string `json:"host"`
}

func readNative(r io.Reader) ([]byte, error) {
	var size uint32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return nil, err
	}
	if size == 0 || size > maxTabMessage {
		return nil, errors.New("invalid native message length")
	}
	data := make([]byte, int(size))
	_, err := io.ReadFull(r, data)
	return data, err
}
func writeNative(w io.Writer, data []byte) error {
	if len(data) == 0 || len(data) > maxTabMessage {
		return errors.New("invalid native message length")
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(data))); err != nil {
		return err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
func parseTab(data []byte) (tabReport, error) {
	var r tabReport
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	r.Title = strings.TrimSpace(r.Title)
	r.Host = strings.ToLower(strings.TrimSpace(r.Host))
	if r.Title == "" || len(r.Title) > 4096 || len(r.Host) > 253 || strings.ContainsAny(r.Host, "/:?@#\\ \t\n\r") {
		return r, errors.New("invalid tab report")
	}
	return r, nil
}
