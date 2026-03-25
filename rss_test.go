package rss2masto

import (
	"fmt"
	"regexp"
	"sync"
	"testing"

	"github.com/mmcdole/gofeed"
	"github.com/valyala/fasthttp"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func TestHashString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"test", "11441948532827618368"},
		{"", "3244421341483603138"},
		{"hello world", "15296390279056496779"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hashString(tt.input)
			if got != tt.want {
				t.Errorf("hashString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMakeHashtags(t *testing.T) {
	casesTitle = cases.Title(language.English, cases.NoLower)

	tests := []struct {
		name     string
		item     *gofeed.Item
		feed     *Feed
		regex    *regexp.Regexp
		expected string
	}{
		{
			name:     "with categories",
			item:     &gofeed.Item{Categories: []string{"Technology", "Programming"}},
			feed:     &Feed{},
			expected: "#Technology #Programming",
		},
		{
			name:     "with prefix",
			item:     &gofeed.Item{Categories: []string{"Tech"}},
			feed:     &Feed{Prefix: "Go"},
			expected: "#Tech #GoTech",
		},
		{
			name:     "no categories, with regex match",
			item:     &gofeed.Item{Link: "https://example.com/posts/golang"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/posts/([^/]+)`),
			expected: "#golang",
		},
		{
			name:     "no categories, no regex match",
			item:     &gofeed.Item{Link: "https://example.com/posts/"},
			feed:     &Feed{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeHashtags(tt.item, tt.feed, tt.regex)
			if got != tt.expected {
				t.Errorf("makeHashtags() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStart_EmptyFeeds(t *testing.T) {
	fm := &FeedsMonitor{}
	fm.Instance.Feeds = []*Feed{}
	// Should return immediately with no feeds
	fm.Start()
}

func TestStart_FeedsWithoutURLOrToken(t *testing.T) {
	fm := &FeedsMonitor{}
	fm.Instance.Feeds = []*Feed{
		{Name: "Test", URL: "", Token: ""},
	}
	fm.Start() // Should complete without error
}

func TestFetchAndParse(t *testing.T) {
	const validRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Test Feed</title>
<item><title>Item 1</title><link>https://example.com/1</link><guid>guid1</guid>
<pubDate>Mon, 01 Jan 2024 12:00:00 +0000</pubDate></item>
</channel></rss>`

	newParser := func(handler func(*fasthttp.Request, *fasthttp.Response) error) *Parser {
		return &Parser{
			Client:     &mockHostClient{handler: handler},
			parserPool: sync.Pool{New: func() any { return gofeed.NewParser() }},
		}
	}

	t.Run("200 OK parses feed", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			resp.SetStatusCode(fasthttp.StatusOK)
			resp.SetBodyString(validRSS)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")

		result := p.FetchAndParse(feed)

		if result == nil {
			t.Fatal("expected parsed feed, got nil")
		}
		if result.Title != "Test Feed" {
			t.Errorf("Title = %q, want %q", result.Title, "Test Feed")
		}
		if len(result.Items) != 1 {
			t.Errorf("Items count = %d, want 1", len(result.Items))
		}
	})

	t.Run("200 OK with ETag stores etag in feed", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			resp.SetStatusCode(fasthttp.StatusOK)
			resp.Header.Set("ETag", `"abc123"`)
			resp.SetBodyString(validRSS)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")

		p.FetchAndParse(feed)

		if string(feed.ETag()) != `"abc123"` {
			t.Errorf("ETag = %q, want %q", feed.ETag(), `"abc123"`)
		}
	})

	t.Run("If-None-Match sent when etag present", func(t *testing.T) {
		var receivedIfNoneMatch string
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			receivedIfNoneMatch = string(req.Header.Peek("If-None-Match"))
			resp.SetStatusCode(fasthttp.StatusNotModified)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")
		etag := []byte(`"abc123"`)
		feed.etag.Store(&etag)

		result := p.FetchAndParse(feed)

		if result != nil {
			t.Error("expected nil for 304 Not Modified")
		}
		if receivedIfNoneMatch != `"abc123"` {
			t.Errorf("If-None-Match = %q, want %q", receivedIfNoneMatch, `"abc123"`)
		}
	})

	t.Run("304 Not Modified returns nil", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			resp.SetStatusCode(fasthttp.StatusNotModified)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")

		if result := p.FetchAndParse(feed); result != nil {
			t.Error("expected nil for 304 Not Modified")
		}
	})

	t.Run("same ETag in response does not overwrite", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			resp.SetStatusCode(fasthttp.StatusOK)
			resp.Header.Set("ETag", `"abc123"`)
			resp.SetBodyString(validRSS)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")
		original := []byte(`"abc123"`)
		feed.etag.Store(&original)

		p.FetchAndParse(feed)

		// pointer should be the same original slice (not replaced)
		if &feed.ETag()[0] != &original[0] {
			t.Error("etag pointer changed despite same value")
		}
	})

	t.Run("non-200/304 status returns nil", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			resp.SetStatusCode(fasthttp.StatusInternalServerError)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")

		if result := p.FetchAndParse(feed); result != nil {
			t.Errorf("expected nil for 500, got %v", result)
		}
	})

	t.Run("connection error returns nil", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			return fmt.Errorf("connection refused")
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")

		if result := p.FetchAndParse(feed); result != nil {
			t.Error("expected nil on connection error")
		}
	})

	t.Run("invalid XML returns nil", func(t *testing.T) {
		p := newParser(func(req *fasthttp.Request, resp *fasthttp.Response) error {
			resp.SetStatusCode(fasthttp.StatusOK)
			resp.SetBodyString(`not valid xml at all`)
			return nil
		})
		feed := NewTestFeed("te", "https://example.com/feed.xml")

		if result := p.FetchAndParse(feed); result != nil {
			t.Error("expected nil for invalid XML")
		}
	})
}

func TestMakeHashtagsEdgeCases(t *testing.T) {
	casesTitle = cases.Title(language.English, cases.NoLower)

	tests := []struct {
		name     string
		item     *gofeed.Item
		feed     *Feed
		regex    *regexp.Regexp
		expected string
	}{
		{
			name:     "categories with special characters filtered",
			item:     &gofeed.Item{Categories: []string{"Tech-News", "AI/ML", "Web.Dev"}},
			feed:     &Feed{},
			expected: "",
		},
		{
			name:     "replacer converts ' - ' to space",
			item:     &gofeed.Item{Categories: []string{"Tech - News"}},
			feed:     &Feed{},
			expected: "#TechNews",
		},
		{
			name:     "replacer converts ' i ' to ':'",
			item:     &gofeed.Item{Categories: []string{"Tech i News"}},
			feed:     &Feed{},
			expected: "#Tech #News",
		},
		{
			name:     "prefix added when not present",
			item:     &gofeed.Item{Categories: []string{"Polska"}},
			feed:     &Feed{Prefix: "PL"},
			expected: "#Polska #PLPolska",
		},
		{
			name:     "prefix not duplicated",
			item:     &gofeed.Item{Categories: []string{"PLPolska"}},
			feed:     &Feed{Prefix: "PL"},
			expected: "#PLPolska",
		},
		{
			name:     "colon splits tags",
			item:     &gofeed.Item{Categories: []string{"Tech:News:Update"}},
			feed:     &Feed{},
			expected: "#Tech #News #Update",
		},
		{
			name:     "regex extracts from link",
			item:     &gofeed.Item{Link: "https://example.com/category/golang"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/category/([^/]+)`),
			expected: "#golang",
		},
		{
			name:     "regex skips tags with hyphen",
			item:     &gofeed.Item{Link: "https://example.com/tag/go-lang"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/tag/([^/]+)`),
			expected: "",
		},
		{
			name:     "empty categories with no regex",
			item:     &gofeed.Item{},
			feed:     &Feed{},
			expected: "",
		},
		{
			name:     "whitespace trimmed",
			item:     &gofeed.Item{Categories: []string{"  Tech  ", "  News  "}},
			feed:     &Feed{},
			expected: "#Tech #News",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeHashtags(tt.item, tt.feed, tt.regex)
			if got != tt.expected {
				t.Errorf("makeHashtags() = %q, want %q", got, tt.expected)
			}
		})
	}
}
