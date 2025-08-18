package rss2masto

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNewFeedsMonitor(t *testing.T) {
	// Create a temporary config file
	configContent := `instance:
  url: "https://mastodon.social"
  lang: "en"
  limit: 500
  timezone: "UTC"
  save: true
  last_monit: 0
  feed:
    - name: "Test Feed"
      url: "https://example.com/feed.xml"
      token: "test-token"
      visibility: "public"
      interval: 15
`

	tmpFile, err := os.CreateTemp("", "feed_*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	// Backup original config file path
	originalConfigFile := configFile
	configFile = tmpFile.Name()
	defer func() { configFile = originalConfigFile }()

	fm, err := NewFeedsMonitor()
	if err != nil {
		t.Fatalf("NewFeedsMonitor() error = %v", err)
	}

	if fm.Instance.URL != "https://mastodon.social" {
		t.Errorf("Expected URL 'https://mastodon.social', got '%s'", fm.Instance.URL)
	}

	if len(fm.Instance.Feeds) != 1 {
		t.Errorf("Expected 1 feed, got %d", len(fm.Instance.Feeds))
	}

	feed := fm.Instance.Feeds[0]
	if feed.Name != "Test Feed" {
		t.Errorf("Expected feed name 'Test Feed', got '%s'", feed.Name)
	}

	if feed.Interval != 15 {
		t.Errorf("Expected interval 15, got %d", feed.Interval)
	}
}

func TestValidateURL(t *testing.T) {
	fm := &FeedsMonitor{}

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid HTTPS URL", "https://example.com/api", false},
		{"HTTP URL should fail", "http://example.com/api", true},
		{"path traversal should fail", "https://example.com/../api", true},
		{"private IP should fail", "https://192.168.1.1/api", true},
		{"loopback IP should fail", "https://127.0.0.1/api", true},
		{"invalid URL", "not-a-url", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fm.validateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFeedIndex(t *testing.T) {
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
			Feeds: []*Feed{
				{Name: "Feed One"},
				{Name: "Feed Two"},
				{Name: "Another Feed"},
			},
		},
	}

	tests := []struct {
		name     string
		feedName string
		want     int
	}{
		{"exact match", "Feed One", 0},
		{"prefix match", "Feed", 0},
		{"second feed", "Feed Two", 1},
		{"no match", "Nonexistent", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fm.FeedIndex(tt.feedName)
			if got != tt.want {
				t.Errorf("FeedIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLastCheckStr(t *testing.T) {
	fm := &FeedsMonitor{location: time.UTC}

	// Test with no last check
	if got := fm.LastCheckStr(); got != "" {
		t.Errorf("LastCheckStr() with no check = %v, want empty string", got)
	}

	// Test with a timestamp
	testTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	fm.lastCheck.Store(testTime.Unix())

	got := fm.LastCheckStr()
	expected := "2023-01-01 12:00:00"
	if got != expected {
		t.Errorf("LastCheckStr() = %v, want %v", got, expected)
	}
}

func TestGetInstanceLimit(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/instance" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"configuration":{"statuses":{"max_characters":1000}}}`))
		}
	}))
	defer server.Close()

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
			URL: server.URL,
		},
		ctxTimeout: 5 * time.Second,
	}

	limit := fm.getInstanceLimit()
	if limit != 500 {
		t.Errorf("getInstanceLimit() = %v, want 500", limit)
	}
}

func TestGetInstanceLimitDefault(t *testing.T) {
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
			URL: "", // Empty URL should return default
		},
	}

	limit := fm.getInstanceLimit()
	if limit != DefaultCharacterLimit {
		t.Errorf("getInstanceLimit() with empty URL = %v, want %v", limit, DefaultCharacterLimit)
	}
}

func TestLocation(t *testing.T) {
	fm := &FeedsMonitor{location: time.UTC}

	if got := fm.Location(); got != time.UTC {
		t.Errorf("Location() = %v, want %v", got, time.UTC)
	}
}

func TestLastMonit(t *testing.T) {
	fm := &FeedsMonitor{}
	testTime := int64(1640995200) // 2022-01-01 00:00:00 UTC
	fm.lastMonit.Store(testTime)

	if got := fm.LastMonit(); got != testTime {
		t.Errorf("LastMonit() = %v, want %v", got, testTime)
	}
}

func TestLastCheck(t *testing.T) {
	fm := &FeedsMonitor{}
	testTime := int64(1640995200)
	fm.lastCheck.Store(testTime)

	if got := fm.LastCheck(); got != testTime {
		t.Errorf("LastCheck() = %v, want %v", got, testTime)
	}
}
