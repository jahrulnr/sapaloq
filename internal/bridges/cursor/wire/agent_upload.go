package wire

import (
	"io"
	"sync"
)

// agentUploadBody is a request body that stays open for the whole api5 turn.
// io.Pipe can interact poorly with net/http http2 request upload lifecycle; this
// reader blocks after the initial RunRequest until more exec/KV frames are
// queued (matches Node http2 req.write without req.end()).
type agentUploadBody struct {
	mu     sync.Mutex
	chunks [][]byte
	notify chan struct{}
	closed bool
	read   int
}

func newAgentUploadBody(initial []byte) *agentUploadBody {
	u := &agentUploadBody{notify: make(chan struct{}, 1)}
	if len(initial) > 0 {
		u.chunks = append(u.chunks, append([]byte(nil), initial...))
	}
	return u
}

func (u *agentUploadBody) Read(p []byte) (int, error) {
	for {
		u.mu.Lock()
		if u.closed && len(u.chunks) == 0 {
			u.mu.Unlock()
			return 0, io.EOF
		}
		if len(u.chunks) > 0 {
			n := copy(p, u.chunks[0])
			u.chunks[0] = u.chunks[0][n:]
			if len(u.chunks[0]) == 0 {
				u.chunks = u.chunks[1:]
			}
			u.read += n
			u.mu.Unlock()
			return n, nil
		}
		u.mu.Unlock()
		<-u.notify
	}
}

func (u *agentUploadBody) Write(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return io.ErrClosedPipe
	}
	u.chunks = append(u.chunks, append([]byte(nil), chunk...))
	u.mu.Unlock()
	select {
	case u.notify <- struct{}{}:
	default:
	}
	return nil
}

func (u *agentUploadBody) Close() error {
	u.mu.Lock()
	u.closed = true
	u.mu.Unlock()
	select {
	case u.notify <- struct{}{}:
	default:
	}
	return nil
}

func (u *agentUploadBody) CloseWithError(err error) error {
	_ = err
	return u.Close()
}
