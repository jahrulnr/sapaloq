package wire

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jahrulnr/sapaloq/internal/debug"

	"golang.org/x/net/http2"
)

const defaultChatPath = "/aiserver.v1.ChatService/StreamUnifiedChatWithTools"

type StreamOptions struct {
	Endpoint     string
	Token        string
	MachineID    string
	Model        string
	Messages     []ChatMessage
	GhostMode    bool
	Timeout      time.Duration
	InsecureTLS  bool // test/local: skip TLS certificate verification
}

type FrameHandler func(part ExtractedPart)

func StreamChat(ctx context.Context, opts StreamOptions, onFrame FrameHandler) error {
	if opts.Token == "" {
		return fmt.Errorf("cursor token is required for live stream")
	}
	endpoint := strings.TrimRight(opts.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api2.cursor.sh"
	}
	url := endpoint
	if !strings.Contains(endpoint, "StreamUnifiedChatWithTools") {
		url = endpoint + defaultChatPath
	}
	model := opts.Model
	if model == "" {
		model = "default"
	}
	body := BuildChatBody(opts.Messages, model)
	headers := BuildHeaders(opts.Token, opts.MachineID, opts.GhostMode)

	debug.Debugf("wire: POST %s model=%s messages=%d body_bytes=%d machine=%s ghost=%v",
		url, model, len(opts.Messages), len(body), debug.RedactSecret(opts.MachineID), opts.GhostMode)
	if debug.Verbose() {
		for i, msg := range opts.Messages {
			debug.Verbosef("wire: message[%d] role=%s bytes=%d preview=%q", i, msg.Role, len(msg.Content), preview(msg.Content, 80))
		}
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if debug.Verbose() {
		for k := range headers {
			if strings.EqualFold(k, "authorization") {
				debug.Verbosef("wire: header %s=%s", k, debug.RedactSecret(headers[k]))
				continue
			}
			debug.Verbosef("wire: header %s=%s", k, headers[k])
		}
	}
	req.Body = io.NopCloser(bytesReader(body))
	req.ContentLength = int64(len(body))

	transport := &http2.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: opts.InsecureTLS,
		},
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	debug.Debugf("wire: response status=%d", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		debug.Debugf("wire: error body=%q", strings.TrimSpace(string(b)))
		return fmt.Errorf("cursor api status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	var rawBytes, frameCount, emptyExtracts int
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			rawBytes += n
			buf = append(buf, tmp[:n]...)
			for {
				flags, payload, consumed, ok := ParseConnectFrame(buf)
				if !ok {
					break
				}
				buf = buf[consumed:]
				frameCount++
				if err := DispatchConnectPayload(flags, payload, onFrame); err != nil {
					return err
				}
				part := ExtractFromPayload(payload)
				hasContent := part.Text != "" || part.Thinking != "" || part.ToolCall != nil || part.DecodeErr != ""
				if debug.Verbose() {
					debug.Verbosef("wire: frame=%d flags=0x%02x payload=%d thinking=%d text=%d tool=%v hex=%s",
						frameCount, flags, len(payload), len(part.Thinking), len(part.Text), part.ToolCall != nil,
						hexPreview(payload, 48))
				}
				if !hasContent {
					emptyExtracts++
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	debug.Debugf("wire: stream closed raw_bytes=%d frames=%d empty_extracts=%d trailing_buf=%d",
		rawBytes, frameCount, emptyExtracts, len(buf))
	if frameCount == 0 {
		debug.Debugf("wire: no connect frames parsed — check protobuf encode or auth")
	}
	return nil
}

func preview(text string, max int) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func hexPreview(payload []byte, max int) string {
	if len(payload) == 0 {
		return ""
	}
	if len(payload) > max {
		payload = payload[:max]
	}
	return hex.EncodeToString(payload)
}

type byteReader struct {
	data []byte
	pos  int
}

func bytesReader(data []byte) *byteReader {
	return &byteReader{data: data}
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *byteReader) Close() error { return nil }
