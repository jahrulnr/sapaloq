package wire

import (
	"bytes"
	"testing"

	"golang.org/x/net/http2/hpack"
)

func TestWriteHPACKHeadersPseudoFirst(t *testing.T) {
	headers := map[string]string{
		"authorization": "Bearer x",
		":path":         "/agent.v1.AgentService/Run",
		":method":       "POST",
		":scheme":       "https",
		":authority":    "agentn.global.api5.cursor.sh",
		"content-type":  "application/connect+proto",
	}
	var buf bytes.Buffer
	enc := hpack.NewEncoder(&buf)
	if err := writeHPACKHeaders(enc, headers); err != nil {
		t.Fatal(err)
	}
	dec := hpack.NewDecoder(4096, func(f hpack.HeaderField) {
		t.Logf("field %s=%s", f.Name, f.Value)
	})
	fields, err := dec.DecodeFull(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{":method", ":authority", ":scheme", ":path"} {
		if fields[i].Name != name {
			t.Fatalf("field[%d] = %q, want %q", i, fields[i].Name, name)
		}
	}
}
