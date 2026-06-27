//go:build !linux

package ipc

import (
	"net"
	"os"
)

// Non-Linux platforms still rely on the private 0700 parent and 0600 socket.
// Platform-specific peer credentials can be added without weakening Linux.
func socketOwnedByCurrentUser(os.FileInfo) bool { return true }
func peerIsCurrentUser(net.Conn) bool           { return true }
