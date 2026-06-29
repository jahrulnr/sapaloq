package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jahrulnr/searchwire"
)

const (
	defaultFetchBytes = 32 * 1024
	maxFetchBytes     = 256 * 1024
	webTimeout        = 20 * time.Second
)

var (
	htmlTagRE    = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`)
	anyTagRE     = regexp.MustCompile(`(?s)<[^>]+>`)
	wsCollapseRE = regexp.MustCompile(`[ \t]+`)
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
)

type webSearchClient interface {
	Search(context.Context, string) (*searchwire.Response, error)
}

func httpClient() *http.Client {
	return &http.Client{Timeout: webTimeout}
}

func toolWebFetch(ctx context.Context, args toolArgs) string {
	raw := strings.TrimSpace(args.URL)
	if raw == "" {
		return "Error: url is required."
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "Error: url must be a valid http(s) URL."
	}
	limit := args.MaxBytes
	if limit <= 0 {
		limit = defaultFetchBytes
	}
	if limit > maxFetchBytes {
		limit = maxFetchBytes
	}
	reqCtx, cancel := context.WithTimeout(ctx, webTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, raw, nil)
	if err != nil {
		return "Error: " + err.Error()
	}
	req.Header.Set("User-Agent", "SapaLOQ/1.0 (+https://github.com/jahrulnr/sapaloq)")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "Error: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Error: HTTP %d from %s.", resp.StatusCode, raw)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, int64(limit)))
	text := htmlToText(string(body))
	if strings.TrimSpace(text) == "" {
		return "(no readable text extracted)"
	}
	return text
}

// htmlToText strips scripts/styles/tags and collapses whitespace into a rough
// plain-text rendering suitable for the model to read.
func htmlToText(html string) string {
	s := htmlTagRE.ReplaceAllString(html, " ")
	s = anyTagRE.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	s = wsCollapseRE.ReplaceAllString(s, " ")
	s = blankLinesRE.ReplaceAllString(s, "\n\n")
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'", "&nbsp;", " ",
	)
	return r.Replace(s)
}

// webSearch delegates to searchwire's concurrent metasearch backend and keeps
// the existing SapaLOQ tool contract: query in, plain text out.
func (o *Orchestrator) webSearch(ctx context.Context, args toolArgs) string {
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return "Error: query is required."
	}
	o.mu.RLock()
	searcher := o.webSearcher
	o.mu.RUnlock()
	if searcher == nil {
		return "Web search unavailable: search backend is not initialized"
	}
	resp, err := searcher.Search(ctx, q)
	if err != nil {
		return formatWebSearchError(err)
	}
	return formatWebSearchResponse(resp)
}

func formatWebSearchResponse(resp *searchwire.Response) string {
	if resp == nil {
		return "No results found."
	}
	results := make([]string, 0, len(resp.Results))
	for i, result := range resp.Results {
		var b strings.Builder
		fmt.Fprintf(&b, "%d. %s\n   %s", i+1, strings.TrimSpace(result.Title), strings.TrimSpace(result.URL))
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			snippet = strings.ReplaceAll(snippet, "\r\n", "\n")
			snippet = strings.ReplaceAll(snippet, "\r", "\n")
			snippet = strings.ReplaceAll(snippet, "\n", "\n   ")
			b.WriteString("\n   ")
			b.WriteString(snippet)
		}
		results = append(results, b.String())
	}
	body := strings.Join(results, "\n\n")
	if body == "" {
		body = "No results found."
	}
	if failures := formatSourceErrors(resp.Errors); failures != "" {
		body += "\n\n(sources failed: " + failures + ")"
	}
	return body
}

func formatWebSearchError(err error) string {
	if errors.Is(err, searchwire.ErrEmptyQuery) {
		return "Error: query is required."
	}
	var searchErr *searchwire.SearchError
	if errors.As(err, &searchErr) {
		if failures := formatSourceErrors(searchErr.Failures); failures != "" {
			return "Web search unavailable: " + failures
		}
	}
	return "Web search unavailable: " + err.Error()
}

func formatSourceErrors(failures []searchwire.SourceError) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		source := strings.TrimSpace(failure.Source)
		detail := strings.TrimSpace(failure.Error)
		if source == "" && detail == "" {
			continue
		}
		if source == "" {
			parts = append(parts, detail)
			continue
		}
		if detail == "" {
			parts = append(parts, source)
			continue
		}
		parts = append(parts, source+": "+detail)
	}
	return strings.Join(parts, "; ")
}
