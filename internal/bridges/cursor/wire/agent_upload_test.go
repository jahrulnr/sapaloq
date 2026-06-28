package wire

import (
	"io"
	"testing"
)

func TestAgentUploadBodyStreamsInitialThenQueued(t *testing.T) {
	body := newAgentUploadBody([]byte("hello"))
	buf := make([]byte, 3)
	n, err := body.Read(buf)
	if err != nil || n != 3 || string(buf) != "hel" {
		t.Fatalf("first read = %d %q err=%v", n, buf[:n], err)
	}
	if err := body.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	rest := make([]byte, 10)
	n, err = body.Read(rest)
	if err != nil || n != 2 || string(rest[:n]) != "lo" {
		t.Fatalf("second read = %d %q err=%v", n, rest[:n], err)
	}
	n, err = body.Read(buf)
	if err != nil || n != 3 || string(buf[:n]) != "wor" {
		t.Fatalf("third read = %d %q err=%v", n, buf[:n], err)
	}
	n, err = body.Read(rest)
	if err != nil || n != 2 || string(rest[:n]) != "ld" {
		t.Fatalf("fourth read = %d %q err=%v", n, rest[:n], err)
	}
	_ = body.Close()
	_, err = body.Read(buf)
	if err != io.EOF {
		t.Fatalf("want EOF after close, got %v", err)
	}
}
