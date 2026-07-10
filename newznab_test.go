package newznab

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// errRoundTripper returns a fixed error for every request.
type errRoundTripper struct{ err error }

func (rt errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, rt.err
}

// errReadCloser fails on Read, exercising the io.ReadAll error branch.
type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("boom read") }
func (errReadCloser) Close() error             { return nil }

// bodyErrRoundTripper returns a 200 response whose body errors on Read.
type bodyErrRoundTripper struct{}

func (bodyErrRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       errReadCloser{},
		Header:     make(http.Header),
	}, nil
}

const searchXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/" version="2.0">
  <channel>
    <newznab:response offset="10" total="4200"/>
    <item>
      <title>Example.Release.1080p</title>
      <guid>abc123</guid>
      <link>https://idx.example/getnzb/link1.nzb</link>
      <category>Movies &gt; HD</category>
      <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
      <enclosure url="https://idx.example/getnzb/enc1.nzb" length="1048576" type="application/x-nzb"/>
      <newznab:attr name="size" value="2097152"/>
      <newznab:attr name="group" value="alt.binaries.movies"/>
      <newznab:attr name="poster" value="nobody@example.com"/>
      <newznab:attr name="grabs" value="7"/>
      <newznab:attr name="category" value="2040"/>
    </item>
    <item>
      <title>Minimal.Release</title>
      <guid>def456</guid>
      <link>https://idx.example/getnzb/link2.nzb</link>
      <pubDate>not-a-real-date</pubDate>
      <enclosure length="512"/>
      <newznab:attr name="category" value="5000"/>
    </item>
  </channel>
</rss>`

// searchNoTotalXML omits the total attribute and has an item with no size
// source at all, plus an invalid grabs value.
const searchNoTotalXML = `<rss xmlns:newznab="http://x/">
  <channel>
    <newznab:response offset="0"/>
    <item>
      <title>NoSize</title>
      <guid>g1</guid>
      <link>https://idx.example/n1.nzb</link>
      <category>TV</category>
      <pubDate>Tue, 03 Feb 2009 04:05:06 +0000</pubDate>
      <newznab:attr name="grabs" value="notanumber"/>
    </item>
  </channel>
</rss>`

// searchBadTotalXML has a non-numeric total, so Total falls back to len(Items).
const searchBadTotalXML = `<rss xmlns:newznab="http://x/">
  <channel>
    <newznab:response total="lots"/>
    <item><title>One</title><guid>x</guid><link>l</link></item>
  </channel>
</rss>`

const capsXML = `<?xml version="1.0" encoding="UTF-8"?>
<caps>
  <categories>
    <category id="2000" name="Movies">
      <subcat id="2040" name="HD"/>
      <subcat id="2030" name="SD"/>
    </category>
    <category id="5000" name="TV"/>
  </categories>
</caps>`

const errXML = `<error code="100" description="Incorrect user credentials"/>`

func newTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("apikey"); got != "KEY" {
			t.Errorf("apikey = %q, want KEY", got)
		}
		if got := r.URL.Query().Get("o"); got != "xml" {
			t.Errorf("o = %q, want xml", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("missing User-Agent header")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchFull(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, searchXML)
	}))
	defer srv.Close()

	c := New(srv.URL, "KEY")
	res, err := c.Search(context.Background(), SearchOptions{
		Query:      "example release",
		Categories: []int{2000, 2040},
		Group:      "alt.binaries.movies",
		Limit:      50,
		Offset:     10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if gotQuery.Get("t") != "search" {
		t.Errorf("t = %q", gotQuery.Get("t"))
	}
	if gotQuery.Get("q") != "example release" {
		t.Errorf("q = %q", gotQuery.Get("q"))
	}
	if gotQuery.Get("cat") != "2000,2040" {
		t.Errorf("cat = %q", gotQuery.Get("cat"))
	}
	if gotQuery.Get("group") != "alt.binaries.movies" {
		t.Errorf("group = %q", gotQuery.Get("group"))
	}
	if gotQuery.Get("limit") != "50" {
		t.Errorf("limit = %q", gotQuery.Get("limit"))
	}
	if gotQuery.Get("offset") != "10" {
		t.Errorf("offset = %q", gotQuery.Get("offset"))
	}

	if res.Total != 4200 {
		t.Errorf("Total = %d, want 4200", res.Total)
	}
	if res.Offset != 10 {
		t.Errorf("Offset = %d, want 10", res.Offset)
	}
	if len(res.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(res.Items))
	}

	// First item: full set of attrs, enclosure url preferred, size from attr.
	i0 := res.Items[0]
	if i0.Title != "Example.Release.1080p" {
		t.Errorf("Title = %q", i0.Title)
	}
	if i0.GUID != "abc123" {
		t.Errorf("GUID = %q", i0.GUID)
	}
	if i0.NZBURL != "https://idx.example/getnzb/enc1.nzb" {
		t.Errorf("NZBURL = %q (want enclosure url)", i0.NZBURL)
	}
	if i0.Category != "Movies > HD" {
		t.Errorf("Category = %q", i0.Category)
	}
	if i0.Size != 2097152 {
		t.Errorf("Size = %d, want 2097152 (attr)", i0.Size)
	}
	if i0.Group != "alt.binaries.movies" {
		t.Errorf("Group = %q", i0.Group)
	}
	if i0.Poster != "nobody@example.com" {
		t.Errorf("Poster = %q", i0.Poster)
	}
	if i0.Grabs != 7 {
		t.Errorf("Grabs = %d, want 7", i0.Grabs)
	}
	want := time.Date(2006, 1, 2, 15, 4, 5, 0, time.FixedZone("", -7*3600))
	if !i0.PublishDate.Equal(want) {
		t.Errorf("PublishDate = %v, want %v", i0.PublishDate, want)
	}

	// Second item: no enclosure url -> link; size from enclosure length;
	// category from attr; bad pubDate -> zero time; no group/poster/grabs.
	i1 := res.Items[1]
	if i1.NZBURL != "https://idx.example/getnzb/link2.nzb" {
		t.Errorf("i1.NZBURL = %q (want link)", i1.NZBURL)
	}
	if i1.Size != 512 {
		t.Errorf("i1.Size = %d, want 512 (enclosure length)", i1.Size)
	}
	if i1.Category != "5000" {
		t.Errorf("i1.Category = %q, want 5000 (attr)", i1.Category)
	}
	if !i1.PublishDate.IsZero() {
		t.Errorf("i1.PublishDate = %v, want zero", i1.PublishDate)
	}
	if i1.Group != "" || i1.Poster != "" || i1.Grabs != 0 {
		t.Errorf("i1 optional attrs not empty: %+v", i1)
	}
}

func TestSearchMinimalOptionsAndNoTotal(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, searchNoTotalXML)
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "KEY") // trailing slash trimmed
	res, err := c.Search(context.Background(), SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	for _, k := range []string{"q", "cat", "group", "limit", "offset"} {
		if gotQuery.Has(k) {
			t.Errorf("param %q should be absent, got %q", k, gotQuery.Get(k))
		}
	}

	if res.Total != 1 {
		t.Errorf("Total = %d, want 1 (fallback to len)", res.Total)
	}
	if res.Offset != 0 {
		t.Errorf("Offset = %d, want 0", res.Offset)
	}
	i := res.Items[0]
	if i.Size != 0 {
		t.Errorf("Size = %d, want 0 (no size source)", i.Size)
	}
	if i.Grabs != 0 {
		t.Errorf("Grabs = %d, want 0 (unparseable)", i.Grabs)
	}
	if i.Category != "TV" {
		t.Errorf("Category = %q, want TV (core element)", i.Category)
	}
}

func TestSearchBadTotalFallsBack(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, searchBadTotalXML)
	c := New(srv.URL, "KEY")
	res, err := c.Search(context.Background(), SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("Total = %d, want 1 (unparseable total falls back)", res.Total)
	}
}

func TestSearchAPIError(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, errXML)
	c := New(srv.URL, "KEY")
	_, err := c.Search(context.Background(), SearchOptions{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "Incorrect user credentials") {
		t.Fatalf("err = %v, want api error with description", err)
	}
}

func TestSearchNon2xx(t *testing.T) {
	srv := newTestServer(t, http.StatusInternalServerError, "boom")
	c := New(srv.URL, "KEY")
	_, err := c.Search(context.Background(), SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want 500 status error", err)
	}
}

func TestSearchDecodeError(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, "<rss><channel><item></nope>")
	c := New(srv.URL, "KEY")
	_, err := c.Search(context.Background(), SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "decode search response") {
		t.Fatalf("err = %v, want decode error", err)
	}
}

func TestSearchRequestError(t *testing.T) {
	// Custom transport that always fails -> exercises HTTPClient.Do error.
	c := New("http://example.invalid", "KEY",
		WithHTTPClient(&http.Client{Transport: errRoundTripper{err: errors.New("dial fail")}}))
	_, err := c.Search(context.Background(), SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "request") {
		t.Fatalf("err = %v, want request error", err)
	}
}

func TestSearchBuildRequestError(t *testing.T) {
	// Invalid escape in BaseURL -> http.NewRequestWithContext fails.
	c := New("http://%zz", "KEY")
	_, err := c.Search(context.Background(), SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("err = %v, want build request error", err)
	}
}

func TestSearchReadError(t *testing.T) {
	c := New("http://example.invalid", "KEY",
		WithHTTPClient(&http.Client{Transport: bodyErrRoundTripper{}}))
	_, err := c.Search(context.Background(), SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "read response") {
		t.Fatalf("err = %v, want read response error", err)
	}
}

func TestCapabilities(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, capsXML)
	}))
	defer srv.Close()

	c := New(srv.URL, "KEY", WithUserAgent("custom-ua/1.0"))
	cats, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if gotQuery.Get("t") != "caps" {
		t.Errorf("t = %q, want caps", gotQuery.Get("t"))
	}
	if len(cats) != 2 {
		t.Fatalf("len(cats) = %d, want 2", len(cats))
	}
	if cats[0].ID != "2000" || cats[0].Name != "Movies" {
		t.Errorf("cats[0] = %+v", cats[0])
	}
	if len(cats[0].Subcats) != 2 {
		t.Fatalf("len(subcats) = %d, want 2", len(cats[0].Subcats))
	}
	if cats[0].Subcats[0].ID != "2040" || cats[0].Subcats[0].Name != "HD" {
		t.Errorf("subcat = %+v", cats[0].Subcats[0])
	}
	if cats[1].ID != "5000" || len(cats[1].Subcats) != 0 {
		t.Errorf("cats[1] = %+v", cats[1])
	}
}

func TestCapabilitiesError(t *testing.T) {
	srv := newTestServer(t, http.StatusForbidden, "denied")
	c := New(srv.URL, "KEY")
	_, err := c.Capabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want 403 error", err)
	}
}

func TestCapabilitiesDecodeError(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, "<caps><categories><category")
	c := New(srv.URL, "KEY")
	_, err := c.Capabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode caps response") {
		t.Fatalf("err = %v, want decode caps error", err)
	}
}

func TestWithHTTPClient(t *testing.T) {
	hc := &http.Client{Timeout: 5 * time.Second}
	c := New("http://x", "k", WithHTTPClient(hc))
	if c.HTTPClient != hc {
		t.Error("WithHTTPClient not applied")
	}
}

func TestNewDefaults(t *testing.T) {
	c := New("http://x/", "k")
	if c.BaseURL != "http://x" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.BaseURL)
	}
	if c.HTTPClient != http.DefaultClient {
		t.Error("default HTTPClient not set")
	}
	if c.UserAgent != defaultUserAgent {
		t.Errorf("UserAgent = %q", c.UserAgent)
	}
}
