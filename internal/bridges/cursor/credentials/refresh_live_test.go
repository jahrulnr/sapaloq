package credentials

import (
	"context"
	"os"
	"testing"
)

func TestEnsureFreshDoesNotReplaceValidVSCDBToken(t *testing.T) {
	if os.Getenv("SAPALOQ_LIVE_E2E") == "" {
		t.Skip("live only")
	}
	creds, err := Load(Options{TokenEnv: "SAPALOQ_CURSOR_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	before := creds.AccessToken
	if err := EnsureFresh(context.Background(), &creds); err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != before {
		t.Fatalf("token changed len %d -> %d", len(before), len(creds.AccessToken))
	}
}
