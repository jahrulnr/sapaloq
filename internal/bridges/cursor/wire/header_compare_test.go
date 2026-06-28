package wire

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
)

func TestBuildHeadersMatchNodeStaticFields(t *testing.T) {
	if os.Getenv("SAPALOQ_LIVE_E2E") == "" {
		t.Skip("live only")
	}
	creds, err := credentials.Load(credentials.Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	h := BuildHeaders(creds.AccessToken, creds.MachineID, creds.GhostMode)
	for _, k := range []string{
		"x-cursor-client-version", "x-cursor-client-type", "x-cursor-client-os",
		"x-cursor-client-arch", "x-cursor-client-device-type", "user-agent",
	} {
		t.Logf("%s=%s", k, h[k])
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if k == "authorization" {
			continue
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(h[k])
		b.WriteByte('\n')
	}
	_ = os.WriteFile("/tmp/go-headers.txt", []byte(b.String()), 0o600)
}
