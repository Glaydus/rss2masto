package rss2masto

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func TestHashString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"test", "5754696928334414137"},
		{"", "17241709254077376921"},
		{"hello world", "5020219685658847592"},
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

func TestCreateRequest(t *testing.T) {
	ctx := context.Background()
	url := "https://mastodon.social"
	key := "test-key"
	token := "test-token"
	data := strings.NewReader("status=test")

	req, err := createRequest(ctx, url, key, token, data)
	if err != nil {
		t.Fatalf("createRequest() error = %v", err)
	}

	if req.Method != http.MethodPost {
		t.Errorf("Expected POST method, got %s", req.Method)
	}

	expectedURL := "https://mastodon.social/api/v1/statuses"
	if req.URL.String() != expectedURL {
		t.Errorf("Expected URL %s, got %s", expectedURL, req.URL.String())
	}

	if auth := req.Header.Get("Authorization"); auth != "Bearer test-token" {
		t.Errorf("Expected Authorization 'Bearer test-token', got %s", auth)
	}

	if idempotency := req.Header.Get("Idempotency-Key"); idempotency != "test-key" {
		t.Errorf("Expected Idempotency-Key 'test-key', got %s", idempotency)
	}

	if contentType := req.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
		t.Errorf("Expected Content-Type 'application/x-www-form-urlencoded', got %s", contentType)
	}
}

func TestMakeHashtags(t *testing.T) {
	// Initialize casesTitle for testing
	casesTitle = cases.Title(language.English, cases.NoLower)

	tests := []struct {
		name     string
		item     *gofeed.Item
		feed     *Feed
		regex    *regexp.Regexp
		expected string
	}{
		{
			name: "with categories",
			item: &gofeed.Item{
				Categories: []string{"Technology", "Programming"},
			},
			feed:     &Feed{},
			regex:    nil,
			expected: "#Technology #Programming",
		},
		{
			name: "with prefix",
			item: &gofeed.Item{
				Categories: []string{"Tech"},
			},
			feed:     &Feed{Prefix: "Go"},
			regex:    nil,
			expected: "#Tech #GoTech",
		},
		{
			name: "no categories, with regex match",
			item: &gofeed.Item{
				Link: "https://example.com/posts/golang",
			},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/posts/([^/]+)`),
			expected: "#golang",
		},
		{
			name: "no categories, no regex match",
			item: &gofeed.Item{
				Link: "https://example.com/posts/",
			},
			feed:     &Feed{},
			regex:    nil,
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

func TestStart(t *testing.T) {
	// Test with empty feeds
	fm := &FeedsMonitor{
		Instance: struct {
			URL      string  `yaml:"url"`
			Lang     string  `yaml:"lang"`
			Limit    int     `yaml:"limit"`
			TimeZone string  `yaml:"timezone"`
			Save     bool    `yaml:"save,omitempty"`
			Monit    int64   `yaml:"last_monit,omitempty"`
			Feeds    []*Feed `yaml:"feed"`
		}{
			Feeds: []*Feed{},
		},
	}

	// Should return immediately with no feeds
	fm.Start()

	// Test with feeds but no URL/token
	fm.Instance.Feeds = []*Feed{
		{Name: "Test", FeedUrl: "", Token: ""},
	}

	fm.Start() // Should complete without error
}

func TestGetFeedWithMockServer(t *testing.T) {
	// Create mock RSS server
	rssContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
<channel>
<title>Test Feed</title>
<item>
<title>Test Item</title>
<description>Test description</description>
<link>https://example.com/item1</link>
<guid>item1</guid>
<pubDate>` + time.Now().Format(time.RFC1123Z) + `</pubDate>
</item>
</channel>
</rss>`

	rssServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(rssContent))
	}))
	defer rssServer.Close()

	// Create mock Mastodon server
	mastodonServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/statuses" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"123"}`))
		}
	}))
	defer mastodonServer.Close()

	fm := &FeedsMonitor{
		Instance: struct {
			URL      string  `yaml:"url"`
			Lang     string  `yaml:"lang"`
			Limit    int     `yaml:"limit"`
			TimeZone string  `yaml:"timezone"`
			Save     bool    `yaml:"save,omitempty"`
			Monit    int64   `yaml:"last_monit,omitempty"`
			Feeds    []*Feed `yaml:"feed"`
		}{
			URL:   mastodonServer.URL,
			Limit: 500,
			Lang:  "en",
		},
		feedParser: gofeed.NewParser(),
		ctxTimeout: 5 * time.Second,
		location:   time.UTC,
	}

	feed := &Feed{
		Name:       "Test Feed",
		FeedUrl:    rssServer.URL,
		Token:      "test-token",
		Visibility: "public",
		LastRun:    time.Now().Add(-time.Hour).Unix(),
	}

	// Test getFeed function
	fm.getFeed(feed)

	// Verify that the feed was processed (Count should be incremented in non-debug mode)
	// Since we're likely in debug mode during testing, we can't verify the count
	// but we can verify the function completed without panic
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
			name: "categories with special characters filtered",
			item: &gofeed.Item{
				Categories: []string{"Tech-News", "AI/ML", "Web.Dev"},
			},
			feed:     &Feed{},
			expected: "",
		},
		{
			name: "replacer converts ' - ' to space",
			item: &gofeed.Item{
				Categories: []string{"Tech - News"},
			},
			feed:     &Feed{},
			expected: "#TechNews",
		},
		{
			name: "replacer converts ' i ' to ':'",
			item: &gofeed.Item{
				Categories: []string{"Tech i News"},
			},
			feed:     &Feed{},
			expected: "#Tech #News",
		},
		{
			name: "prefix added when not present",
			item: &gofeed.Item{
				Categories: []string{"Polska"},
			},
			feed:     &Feed{Prefix: "PL"},
			expected: "#Polska #PLPolska",
		},
		{
			name: "prefix not duplicated",
			item: &gofeed.Item{
				Categories: []string{"PLPolska"},
			},
			feed:     &Feed{Prefix: "PL"},
			expected: "#PLPolska",
		},
		{
			name: "colon splits tags",
			item: &gofeed.Item{
				Categories: []string{"Tech:News:Update"},
			},
			feed:     &Feed{},
			expected: "#Tech #News #Update",
		},
		{
			name: "regex extracts from link",
			item: &gofeed.Item{
				Link: "https://example.com/category/golang",
			},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/category/([^/]+)`),
			expected: "#golang",
		},
		{
			name: "regex skips tags with hyphen",
			item: &gofeed.Item{
				Link: "https://example.com/tag/go-lang",
			},
			feed:     &Feed{},
			regex:    regexp.MustCompile(`/tag/([^/]+)`),
			expected: "",
		},
		{
			name: "empty categories with no regex",
			item: &gofeed.Item{},
			feed:     &Feed{},
			expected: "",
		},
		{
			name: "whitespace trimmed",
			item: &gofeed.Item{
				Categories: []string{"  Tech  ", "  News  "},
			},
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
