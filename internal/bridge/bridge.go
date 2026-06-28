package bridge

import (
	"context"
	"fmt"
	"sync"

	"github.com/jahrulnr/sapaloq/internal/parse"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Image is an inline image attachment (vision input). DataURI is a
// `data:image/<mime>;base64,<payload>` string for transport; the bridge layer
// can decode it before passing to the upstream RPC. Or the caller may set
// Data directly (already-decoded bytes) along with MimeType.
type Image struct {
	DataURI  string `json:"data_uri,omitempty"`
	Data     []byte `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type Request struct {
	SessionID     string    `json:"session_id,omitempty"`
	Messages      []Message `json:"messages"`
	Model         string    `json:"model,omitempty"`
	DeclaredTools []string  `json:"declared_tools,omitempty"`
	// Images carries inline image attachments for vision-capable bridges.
	// Empty for text-only requests.
	Images []Image `json:"images,omitempty"`
	// ToolExecutor lets a bridge with a native tool-callback protocol execute
	// declared SapaLOQ tools inside the provider's own turn loop.
	ToolExecutor func(context.Context, parse.ToolCall) (string, error) `json:"-"`
}

type Bridge interface {
	ID() string
	Caps() BridgeCaps
	Complete(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

type BridgeCaps struct {
	Thinking bool `json:"thinking"`
	Tools    bool `json:"tools"`
	LiveAPI  bool `json:"live_api"`
}

type Registry struct {
	mu      sync.RWMutex
	bridges map[string]Bridge
}

func NewRegistry() *Registry {
	return &Registry{bridges: map[string]Bridge{}}
}

func (r *Registry) Register(b Bridge) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bridges[b.ID()] = b
}

func (r *Registry) Get(id string) (Bridge, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.bridges[id]
	if !ok {
		return nil, fmt.Errorf("bridge %q not registered", id)
	}
	return b, nil
}
