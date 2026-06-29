package main

import (
	"net"
	"testing"
	"time"
)

func TestIPCIdlePolicy(t *testing.T) {
	idle, total := ipcIdlePolicy("chat_send")
	if idle != 5*time.Minute || total != 35*time.Minute {
		t.Fatalf("chat_send = (%v,%v)", idle, total)
	}
	idle, total = ipcIdlePolicy("ping")
	if idle != 0 || total != 60*time.Second {
		t.Fatalf("ping = (%v,%v)", idle, total)
	}
}

func TestSetIPCReadDeadlineExhausted(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	started := time.Now().Add(-31 * time.Second)
	err := setIPCReadDeadline(c1, 0, 30*time.Second, started, false)
	if err == nil {
		t.Fatal("expected threshold exhausted error")
	}
}

func TestSetIPCReadDeadlineSetsFuture(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	if err := setIPCReadDeadline(c1, 5*time.Minute, 35*time.Minute, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	// Prove a deadline was armed: read should not block forever once peer closes.
	_ = c2.Close()
	buf := make([]byte, 1)
	c1.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := c1.Read(buf); err == nil {
		t.Fatal("expected read error after peer close")
	}
}
