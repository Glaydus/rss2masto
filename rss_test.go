package rss2masto

import (
	"fmt"
	"regexp"
	"strings"
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
	// Load dictionary used by hashDict inside makeHashtags
	ReloadHashDict([]byte("krakow=Kraków\nhokej-na-lodzie=HokejNaLodzie\ntelewizja-i-vod=Telewizja #Vod\n"))

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
			item:     &gofeed.Item{Link: "https://www.telepolis.pl/fintech/cashless/przerwa-paribas"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`telepolis\.pl\/(?:artykuly|rozrywka|.*?tech)\/(.+?)\/`),
			expected: "#Cashless",
		},
		{
			name:     "no categories, with regex match and prefix",
			item:     &gofeed.Item{Link: "https://tvn24.pl/polska/abc"},
			feed:     &Feed{Prefix: "tvn"},
			regex:    regexp.MustCompile(`[\/\.]tvn24\.pl\/(.+?)\/`),
			expected: "#Polska #tvnPolska",
		},
		{
			name:     "no categories, no regex match",
			item:     &gofeed.Item{Link: "https://example.com/posts/"},
			feed:     &Feed{},
			expected: "",
		},
		{
			name:     "with HashTag only",
			item:     &gofeed.Item{},
			feed:     &Feed{HashTag: "golang"},
			expected: "#golang",
		},
		{
			name:     "with HashTag and categories",
			item:     &gofeed.Item{Categories: []string{"Tech"}},
			feed:     &Feed{HashTag: "golang"},
			expected: "#golang #Tech",
		},
		{
			name:     "with HashTag and prefix",
			item:     &gofeed.Item{Categories: []string{"News"}},
			feed:     &Feed{HashTag: "golang", Prefix: "Go"},
			expected: "#golang #News #GoGolang #GoNews",
		},
		{
			name:     "hashlink with dict translation — diacritics",
			item:     &gofeed.Item{Link: "https://example.com/sport/krakow/article"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/sport/([^/]+)/`),
			expected: "#Kraków",
		},
		{
			name:     "hashlink with dict translation — hyphenated slug",
			item:     &gofeed.Item{Link: "https://example.com/sport/hokej-na-lodzie/article"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/sport/([^/]+)/`),
			expected: "#HokejNaLodzie", // dict maps hyphenated slug to valid hashtag
		},
		{
			name:     "hashlink with dict translation — 2 hashes",
			item:     &gofeed.Item{Link: "https://www.telepolis.pl/rozrywka/telewizja-i-vod/nowy-kryminal"},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`telepolis\.pl\/(?:artykuly|rozrywka|.*?tech)\/(.+?)\/`),
			expected: "#Telewizja #Vod",
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

func TestHashDict(t *testing.T) {
	// Load a known dictionary inline — no file dependency
	ReloadHashDict([]byte(`
# test dictionary
krakow=Kraków
hokej-na-lodzie=HokejNaLodzie
zuzel=Żużel
`))

	tests := []struct {
		input    string
		expected string
	}{
		{"krakow", "Kraków"},
		{"hokej-na-lodzie", "HokejNaLodzie"},
		{"zuzel", "Żużel"},
		{"unknown", "unknown"}, // not in dict — returned as-is
		{"", ""},               // empty string — returned as-is
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hashDict(tt.input)
			if got != tt.expected {
				t.Errorf("hashDict(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestReloadHashDict(t *testing.T) {
	t.Run("loads from bytes", func(t *testing.T) {
		ReloadHashDict([]byte("foo=Bar\nbaz=Qux\n"))
		if got := hashDict("foo"); got != "Bar" {
			t.Errorf("hashDict(%q) = %q, want %q", "foo", got, "Bar")
		}
		if got := hashDict("baz"); got != "Qux" {
			t.Errorf("hashDict(%q) = %q, want %q", "baz", got, "Qux")
		}
	})

	t.Run("ignores comments and blank lines", func(t *testing.T) {
		ReloadHashDict([]byte("# comment\n\nkey=Val\n"))
		if got := hashDict("key"); got != "Val" {
			t.Errorf("hashDict(%q) = %q, want %q", "key", got, "Val")
		}
	})

	t.Run("ignores lines without separator", func(t *testing.T) {
		ReloadHashDict([]byte("valid=OK\nnoequalssign\n"))
		if got := hashDict("noequalssign"); got != "noequalssign" {
			t.Errorf("expected passthrough for malformed line, got %q", got)
		}
	})

	t.Run("empty data does not replace existing dict", func(t *testing.T) {
		ReloadHashDict([]byte("preserved=Yes\n"))
		ReloadHashDict([]byte("")) // empty — should not replace
		if got := hashDict("preserved"); got != "Yes" {
			t.Errorf("dict was replaced by empty data, hashDict(%q) = %q", "preserved", got)
		}
	})
}

func TestViewHashDict(t *testing.T) {
	ReloadHashDict([]byte("b=Beta\na=Alpha\n"))
	out := string(ViewHashDict())
	// output must be sorted by key
	if out != "a=Alpha\nb=Beta\n" {
		t.Errorf("ViewHashDict() = %q, want sorted output", out)
	}
}

func TestReplaceLink(t *testing.T) {
	tests := []struct {
		name        string
		link        string
		replaceLink string
		expected    string
	}{
		{
			name:        "removes tracking query param",
			link:        "https://example.com/article/123?utm_medium=feed",
			replaceLink: `\?utm_medium=[^&]+`,
			expected:    "https://example.com/article/123",
		},
		{
			name:        "removes path suffix",
			link:        "https://example.com/news/story.html/amp",
			replaceLink: `/amp$`,
			expected:    "https://example.com/news/story.html",
		},
		{
			name:        "removes part of the path",
			link:        "https://www.example.com/news/story.html",
			replaceLink: `(www\.)`,
			expected:    "https://example.com/news/story.html",
		},
		{
			name:        "no match leaves link unchanged",
			link:        "https://example.com/article/123",
			replaceLink: `\?utm_source=[^&]+`,
			expected:    "https://example.com/article/123",
		},
		{
			name:        "source=rss stripped before replace_link",
			link:        "https://example.com/post?source=rss&ref=feed",
			replaceLink: `&ref=[^&]+`,
			expected:    "https://example.com/post",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link := tt.link
			// replicate the logic from GetFeed
			link, _, _ = strings.Cut(link, "?source=rss")
			if tt.replaceLink != "" {
				re := regexp.MustCompile(tt.replaceLink)
				link = re.ReplaceAllString(link, "")
			}
			if link != tt.expected {
				t.Errorf("link = %q, want %q", link, tt.expected)
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
		{Name: "Test", URLs: FeedURLs{""}, Token: ""},
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
		feed.SetETag([]byte(`"abc123"`))

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
		feed.SetETag(original)

		p.FetchAndParse(feed)

		// slice header should point to the same backing array (not replaced)
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
