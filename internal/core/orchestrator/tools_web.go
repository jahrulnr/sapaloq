package orchestrator

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultFetchBytes = 32 * 1024
	maxFetchBytes     = 256 * 1024
	webTimeout        = 20 * time.Second
)

var (
	htmlTagRE     = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`)
	anyTagRE      = regexp.MustCompile(`(?s)<[^>]+>`)
	wsCollapseRE  = regexp.MustCompile(`[ \t]+`)
	blankLinesRE  = regexp.MustCompile(`\n{3,}`)
	ddgResultRE   = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgRedirectRE = regexp.MustCompile(`uddg=([^&]+)`)
)

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
	req.Header.Set("User-Agent", "SapaLOQ/1.0 (+companion)")
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

// toolWebSearch uses DuckDuckGo's keyless HTML endpoint and returns the top
// results as title + URL lines. Best-effort: returns an honest message if the
// endpoint is unreachable.
func toolWebSearch(ctx context.Context, args toolArgs) string {
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return "Error: query is required."
	}
	reqCtx, cancel := context.WithTimeout(ctx, webTimeout)
	defer cancel()
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(q)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "Error: " + err.Error()
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SapaLOQ/1.0)")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "Web search unavailable: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Web search unavailable: HTTP %d.", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	matches := ddgResultRE.FindAllStringSubmatch(string(body), 8)
	if len(matches) == 0 {
		return "No results found."
	}
	var b strings.Builder
	for i, m := range matches {
		title := strings.TrimSpace(htmlToText(m[2]))
		link := decodeDDGURL(m[1])
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, title, link))
	}
	return strings.TrimSpace(b.String())
}

func decodeDDGURL(href string) string {
	if m := ddgRedirectRE.FindStringSubmatch(href); len(m) == 2 {
		if dec, err := url.QueryUnescape(m[1]); err == nil {
			return dec
		}
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}
