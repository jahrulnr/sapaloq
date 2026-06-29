package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/jahrulnr/searchwire"
)

type stubWebSearchClient struct {
	search func(context.Context, string) (*searchwire.Response, error)
}

func (s stubWebSearchClient) Search(ctx context.Context, query string) (*searchwire.Response, error) {
	return s.search(ctx, query)
}

func TestFormatWebSearchResponse(t *testing.T) {
	resp := &searchwire.Response{
		Results: []searchwire.Result{
			{Title: " First result ", URL: " https://example.com/one ", Snippet: "First line\nsecond line"},
			{Title: "Second result", URL: "https://example.com/two"},
		},
		Errors: []searchwire.SourceError{
			{Source: "brave", Error: "HTTP 403"},
			{Source: "github", Error: "rate limited"},
		},
	}
	want := "1. First result\n   https://example.com/one\n   First line\n   second line\n\n" +
		"2. Second result\n   https://example.com/two\n\n" +
		"(sources failed: brave: HTTP 403; github: rate limited)"
	if got := formatWebSearchResponse(resp); got != want {
		t.Fatalf("formatWebSearchResponse() =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatWebSearchResponseNoResults(t *testing.T) {
	cases := []struct {
		name string
		resp *searchwire.Response
		want string
	}{
		{name: "nil response", want: "No results found."},
		{name: "empty response", resp: &searchwire.Response{}, want: "No results found."},
		{
			name: "empty with partial failure",
			resp: &searchwire.Response{Errors: []searchwire.SourceError{{Source: "wikipedia", Error: "timeout"}}},
			want: "No results found.\n\n(sources failed: wikipedia: timeout)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatWebSearchResponse(tc.resp); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatWebSearchError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "empty query", err: searchwire.ErrEmptyQuery, want: "Error: query is required."},
		{
			name: "all sources failed",
			err: &searchwire.SearchError{Failures: []searchwire.SourceError{
				{Source: "brave", Error: "HTTP 403"},
				{Source: "startpage", Error: "timeout"},
			}},
			want: "Web search unavailable: brave: HTTP 403; startpage: timeout",
		},
		{name: "context cancellation", err: context.Canceled, want: "Web search unavailable: context canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatWebSearchError(tc.err); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWebSearchEmptyQuery(t *testing.T) {
	called := false
	o := &Orchestrator{webSearcher: stubWebSearchClient{search: func(context.Context, string) (*searchwire.Response, error) {
		called = true
		return nil, errors.New("must not be called")
	}}}
	if got := o.webSearch(context.Background(), toolArgs{Query: " \t\n"}); got != "Error: query is required." {
		t.Fatalf("got %q", got)
	}
	if called {
		t.Fatal("search backend was called for empty input")
	}
}

func TestWebSearchUsesSearcher(t *testing.T) {
	var capturedQuery string
	o := &Orchestrator{webSearcher: stubWebSearchClient{search: func(ctx context.Context, query string) (*searchwire.Response, error) {
		capturedQuery = query
		return &searchwire.Response{Results: []searchwire.Result{{Title: "Result", URL: "https://example.com", Snippet: "Summary"}}}, nil
	}}}

	want := "1. Result\n   https://example.com\n   Summary"
	if got := o.webSearch(context.Background(), toolArgs{Query: "  golang context  "}); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if capturedQuery != "golang context" {
		t.Fatalf("backend query = %q", capturedQuery)
	}
}

func TestWebSearchPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := &Orchestrator{webSearcher: stubWebSearchClient{search: func(ctx context.Context, _ string) (*searchwire.Response, error) {
		return nil, ctx.Err()
	}}}
	if got := o.webSearch(ctx, toolArgs{Query: "query"}); got != "Web search unavailable: context canceled" {
		t.Fatalf("got %q", got)
	}
}

func TestWebSearchWithoutBackend(t *testing.T) {
	if got := (&Orchestrator{}).webSearch(context.Background(), toolArgs{Query: "query"}); got != "Web search unavailable: search backend is not initialized" {
		t.Fatalf("got %q", got)
	}
}
