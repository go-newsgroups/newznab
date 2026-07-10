// Package newznab is a dependency-free client for the Newznab indexer API.
//
// It targets the Newznab Usenet search API and also works against
// NZBHydra2, which exposes a Newznab-superset API. The client is
// CGO-free and uses only the Go standard library.
package newznab

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// defaultUserAgent is sent when no custom User-Agent is configured.
const defaultUserAgent = "go-newsgroups-newznab/0.1.0"

// Client is a Newznab / NZBHydra2 API client.
type Client struct {
	// BaseURL is the indexer root, e.g. https://api.nzbgeek.info.
	BaseURL string
	// APIKey is the indexer API key.
	APIKey string
	// HTTPClient is used for all requests. Defaults to http.DefaultClient.
	HTTPClient *http.Client
	// UserAgent is sent on every request.
	UserAgent string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets a custom *http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.HTTPClient = h }
}

// WithUserAgent sets a custom User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.UserAgent = ua }
}

// New creates a Client for the given indexer root and API key.
func New(baseURL, apiKey string, opts ...Option) *Client {
	c := &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: http.DefaultClient,
		UserAgent:  defaultUserAgent,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Item is a single search result.
type Item struct {
	Title       string
	GUID        string
	NZBURL      string    // the .nzb download link (item <link> or <enclosure url>)
	Category    string
	Size        int64     // bytes (from newznab:attr name="size" or <enclosure length>)
	PublishDate time.Time // <pubDate>, RFC1123Z
	Group       string    // newznab:attr name="group" (may be empty)
	Poster      string    // newznab:attr name="poster" (may be empty)
	Grabs       int       // newznab:attr name="grabs" (0 if absent)
}

// SearchResult is the outcome of a Search call.
type SearchResult struct {
	Items  []Item
	Total  int // from <newznab:response total=...> when present, else len(Items)
	Offset int
}

// SearchOptions parameterises a Search call.
type SearchOptions struct {
	Query      string
	Categories []int  // newznab category ids
	Group      string // limit to a newsgroup
	Limit      int
	Offset     int
}

// Category is a capability category, possibly with subcategories.
type Category struct {
	ID      string
	Name    string
	Subcats []Category
}

// Search performs a t=search query against the indexer.
func (c *Client) Search(ctx context.Context, opts SearchOptions) (*SearchResult, error) {
	params := url.Values{}
	params.Set("t", "search")
	if opts.Query != "" {
		params.Set("q", opts.Query)
	}
	if len(opts.Categories) > 0 {
		params.Set("cat", joinInts(opts.Categories))
	}
	if opts.Group != "" {
		params.Set("group", opts.Group)
	}
	if opts.Limit > 0 {
		params.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Offset > 0 {
		params.Set("offset", strconv.Itoa(opts.Offset))
	}

	body, err := c.do(ctx, params)
	if err != nil {
		return nil, err
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("newznab: decode search response: %w", err)
	}

	items := make([]Item, 0, len(feed.Channel.Items))
	for _, ri := range feed.Channel.Items {
		items = append(items, ri.toItem())
	}

	total := len(items)
	if feed.Channel.Response.Total != "" {
		if t, err := strconv.Atoi(feed.Channel.Response.Total); err == nil {
			total = t
		}
	}
	offset, _ := strconv.Atoi(feed.Channel.Response.Offset)

	return &SearchResult{Items: items, Total: total, Offset: offset}, nil
}

// Capabilities performs a t=caps query and returns the category tree.
func (c *Client) Capabilities(ctx context.Context) ([]Category, error) {
	params := url.Values{}
	params.Set("t", "caps")

	body, err := c.do(ctx, params)
	if err != nil {
		return nil, err
	}

	var caps capsResponse
	if err := xml.Unmarshal(body, &caps); err != nil {
		return nil, fmt.Errorf("newznab: decode caps response: %w", err)
	}

	out := make([]Category, 0, len(caps.Categories.Category))
	for _, cat := range caps.Categories.Category {
		out = append(out, cat.toCategory())
	}
	return out, nil
}

// do issues the request with apikey + o=xml set and returns the body,
// converting non-2xx statuses and Newznab error bodies into errors.
func (c *Client) do(ctx context.Context, params url.Values) ([]byte, error) {
	params.Set("apikey", c.APIKey)
	params.Set("o", "xml")

	u := c.BaseURL + "/api?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("newznab: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("newznab: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("newznab: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("newznab: unexpected status %d: %s", resp.StatusCode, resp.Status)
	}

	var ae apiError
	if xml.Unmarshal(body, &ae) == nil && ae.XMLName.Local == "error" {
		return nil, fmt.Errorf("newznab: api error %s: %s", ae.Code, ae.Description)
	}

	return body, nil
}

// joinInts renders ints as a comma-separated list.
func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// --- XML wire types ---

type apiError struct {
	XMLName     xml.Name `xml:"error"`
	Code        string   `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Response struct {
			Offset string `xml:"offset,attr"`
			Total  string `xml:"total,attr"`
		} `xml:"response"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Category  string `xml:"category"`
	PubDate   string `xml:"pubDate"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

// toItem maps the wire item onto the public Item type.
func (ri rssItem) toItem() Item {
	attrs := make(map[string]string, len(ri.Attrs))
	for _, a := range ri.Attrs {
		attrs[a.Name] = a.Value
	}

	item := Item{
		Title:    ri.Title,
		GUID:     ri.GUID,
		Category: ri.Category,
		Group:    attrs["group"],
		Poster:   attrs["poster"],
	}

	// NZBURL: prefer <enclosure url>, fall back to <link>.
	item.NZBURL = ri.Link
	if ri.Enclosure.URL != "" {
		item.NZBURL = ri.Enclosure.URL
	}

	// Category: fall back to the newznab:attr "category" when the core
	// element is empty.
	if item.Category == "" {
		item.Category = attrs["category"]
	}

	// Size: attr "size" first, else <enclosure length>.
	if v, ok := attrs["size"]; ok {
		item.Size, _ = strconv.ParseInt(v, 10, 64)
	} else if ri.Enclosure.Length != "" {
		item.Size, _ = strconv.ParseInt(ri.Enclosure.Length, 10, 64)
	}

	if v, ok := attrs["grabs"]; ok {
		item.Grabs, _ = strconv.Atoi(v)
	}

	if t, err := time.Parse(time.RFC1123Z, ri.PubDate); err == nil {
		item.PublishDate = t
	}

	return item
}

type capsResponse struct {
	XMLName    xml.Name `xml:"caps"`
	Categories struct {
		Category []capsCategory `xml:"category"`
	} `xml:"categories"`
}

type capsCategory struct {
	ID     string `xml:"id,attr"`
	Name   string `xml:"name,attr"`
	Subcat []struct {
		ID   string `xml:"id,attr"`
		Name string `xml:"name,attr"`
	} `xml:"subcat"`
}

// toCategory maps the wire category onto the public Category type.
func (cc capsCategory) toCategory() Category {
	cat := Category{ID: cc.ID, Name: cc.Name}
	for _, sc := range cc.Subcat {
		cat.Subcats = append(cat.Subcats, Category{ID: sc.ID, Name: sc.Name})
	}
	return cat
}
