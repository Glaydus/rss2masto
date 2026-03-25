package rss2masto

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	jsoniter "github.com/json-iterator/go"
	"github.com/mmcdole/gofeed"
	"github.com/valyala/fasthttp"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

// FeedsMonitor holds the configuration and state for monitoring multiple RSS feeds
type FeedsMonitor struct {
	// Instance holds the Mastodon instance configuration and list of feeds to monitor
	// The struct includes fields for:
	// - URL: Mastodon instance URL
	// - Lang: default language for posts
	// - Limit: maximum characters per post
	// - TimeZone: timezone for date formatting
	// - Save: whether to save state to disk
	// - Monit: last monitoring run timestamp
	// - Feeds: list of feeds to monitor
	Instance struct {
		URL      string  `yaml:"url"`
		Lang     string  `yaml:"lang"`
		Limit    int     `yaml:"limit"`
		TimeZone string  `yaml:"timezone"`
		Save     bool    `yaml:"save,omitempty"`
		Monit    int64   `yaml:"last_monit,omitempty"`
		Feeds    []*Feed `yaml:"feed"`
	} `yaml:"instance"`

	Parser     *Parser
	hostClient httpClient
	isStarted  atomic.Bool
	lastCheck  atomic.Int64
	lastMonit  atomic.Int64
	location   *time.Location
}

// Feed holds the configuration and state for a single RSS feed
// The struct includes fields for:
// - Name: feed identifier/name
// - URL: RSS feed endpoint
// - Token: Mastodon API access token
// - Prefix: optional text to prepend to posts
// - Visibility: post visibility level (public, unlisted, private)
// - HashLink: whether to add hash link to posts
// - ReplaceFrom/ReplaceTo: text replacement rules
// - Interval: check interval in minutes
// - LastRun: Unix timestamp of last check
// - Count: number of items posted
// - Id: Mastodon account ID
// - SendTime: time when last post was sent
// - Followers: concurrent follower count
// - shedCounter: scheduled counter for posting
// - etag: HTTP ETag for conditional requests
type Feed struct {
	Name        string                 `yaml:"name"`
	URL         string                 `yaml:"url"`
	Token       string                 `yaml:"token"`
	Prefix      string                 `yaml:"prefix,omitempty"`
	Visibility  string                 `yaml:"visibility,omitempty"`
	HashLink    string                 `yaml:"hashlink,omitempty"`
	ReplaceFrom string                 `yaml:"replace_from,omitempty"`
	ReplaceTo   string                 `yaml:"replace_to,omitempty"`
	Interval    int64                  `yaml:"interval,omitempty"`
	LastRun     int64                  `yaml:"last_run,omitempty"`
	Count       int64                  `yaml:"-"`
	Id          int64                  `yaml:"-"`
	SendTime    time.Time              `yaml:"-"`
	Followers   atomic.Int64           `yaml:"-"`
	shedCounter atomic.Int64           `yaml:"-"`
	etag        atomic.Pointer[[]byte] `yaml:"-"`
}

// MastodonPost holds the data needed to post to Mastodon
// This struct is used to marshal the request body for posting to Mastodon API
type MastodonPost struct {
	Status     string `json:"status"`
	Visibility string `json:"visibility"`
	Language   string `json:"language,omitempty"`
}

const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
const DefaultCharacterLimit = 500 // default mastodon max character limit
const DefaultCheckInterval = 10   // default check feed interval in minutes

var (
	configFile      = "./feed.yaml"
	visibilityTypes = map[string]bool{
		"public":   true,
		"unlisted": true,
		"private":  true,
	}
	// use this to convert strings to title case (instead of deprecated strings.Title())
	casesTitle cases.Caser
)

type httpClient interface {
	Do(req *fasthttp.Request, resp *fasthttp.Response) error
}

// Parser wraps gofeed.Parser with an HTTP client and sync.Pool for efficient reuse
type Parser struct {
	Client     httpClient
	parserPool sync.Pool
}

// NewParser creates a new RSS parser with optional custom HTTP client
// If no client is provided, a default fasthttp.Client is created with:
// - 1MB max response size
// - 15s read/write timeouts
// - 4096 concurrency limit
// - 1-hour DNS cache duration
func NewParser(c httpClient) *Parser {
	if c == nil {
		// Default production configuration
		c = &fasthttp.Client{
			MaxResponseBodySize:      1024 * 1024, // 1MB limit
			ReadBufferSize:           4096 * 2,    // 2 * default
			MaxConnsPerHost:          10,
			ReadTimeout:              15 * time.Second,
			WriteTimeout:             15 * time.Second,
			NoDefaultUserAgentHeader: true,
			// increase DNS cache time to an hour instead of default minute
			Dial: (&fasthttp.TCPDialer{
				Concurrency:      4096,
				DNSCacheDuration: time.Hour,
			}).Dial,
		}
	}
	return &Parser{
		Client: c,
		parserPool: sync.Pool{
			New: func() any {
				return gofeed.NewParser()
			},
		},
	}
}

// NewFeedsMonitor creates and initializes a new FeedsMonitor instance by:
// - Loading and parsing the feed configuration from YAML file
// - Setting up monitoring timestamps and intervals
// - Configuring timezone and language settings
// - Setting character limits and feed IDs
// - Initializing default values for all feeds
func NewFeedsMonitor() (*FeedsMonitor, error) {
	var fm FeedsMonitor

	file, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(file, &fm)
	if err != nil {
		return nil, err
	}
	instanceHost, err := fm.parseURLHost(fm.Instance.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid instance URL: %w", err)
	}

	fm.hostClient = &fasthttp.HostClient{
		IsTLS:                  true,
		Addr:                   instanceHost + ":443",
		Name:                   "rss2masto",
		ReadTimeout:            15 * time.Second,
		WriteTimeout:           15 * time.Second,
		DisablePathNormalizing: true,
		Dial: (&fasthttp.TCPDialer{
			DNSCacheDuration: time.Hour,
		}).Dial,
	}
	fm.Parser = NewParser(nil)

	// Set LastMonit to 6 hours ago if not set or older than 6 hours
	if fm.Instance.Monit == 0 || time.Now().UTC().Sub(time.Unix(fm.Instance.Monit, 0)).Hours() > 6 {
		t := time.Now().UTC().Truncate(time.Minute).Add(-6 * time.Hour)
		fm.Instance.Monit = t.Unix()
	}
	fm.lastMonit.Store(fm.Instance.Monit)

	// set language tag for case conversion
	langTag := language.English
	if fm.Instance.Lang != "" {
		langTag, err = language.Parse(fm.Instance.Lang)
		if err != nil {
			langTag = language.English
			fmt.Println(err, "using default language")
		}
	}
	casesTitle = cases.Title(langTag, cases.NoLower)

	// Set default values for feeds and get their IDs
	go fm.setDefaults()

	return &fm, nil
}

// NewTestFeed creates a new feed with default values for testing purposes
// This function is intended for testing and development purposes only
func NewTestFeed(name, url string) *Feed {
	feed := &Feed{
		Name: name,
		URL:  url,
	}
	empty := make([]byte, 0)
	feed.etag.Store(&empty)
	return feed
}

// ETag returns the ETag for the feed
// It returns a copy of the ETag byte slice to prevent external modification
func (f *Feed) ETag() []byte {
	return *f.etag.Load()
}

// LastCheck returns the Unix timestamp of the last check
func (fm *FeedsMonitor) LastCheck() int64 {
	return fm.lastCheck.Load()
}

// LastCheckStr returns the formatted date/time string of the last check
func (fm *FeedsMonitor) LastCheckStr() string {
	sec := fm.lastCheck.Load()
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).In(fm.Location()).Format(time.DateTime)
}

// LastMonit returns the Unix timestamp of the last monitoring run
func (fm *FeedsMonitor) LastMonit() int64 {
	return fm.lastMonit.Load()
}

// FeedIndex returns the index of the feed with the given name prefix, or -1 if not found
func (fm *FeedsMonitor) FeedIndex(name string) int {
	for i, feed := range fm.Instance.Feeds {
		if strings.HasPrefix(feed.Name, name) {
			return i
		}
	}
	return -1
}

// Location returns the timezone location used for time formatting
func (fm *FeedsMonitor) Location() *time.Location {
	if fm.location == nil {
		var err error
		fm.location, err = time.LoadLocation(fm.Instance.TimeZone)
		if err != nil {
			fmt.Println(err)
			fm.location = time.UTC
		}
	}
	return fm.location
}

// SaveFeedsData saves the current feed monitoring state to the config file
func (fm *FeedsMonitor) SaveFeedsData() error {
	fm.Instance.Monit = fm.LastMonit()
	out, err := yaml.Marshal(fm)
	if err != nil {
		return err
	}
	err = os.WriteFile(configFile, out, 0600)
	if err != nil {
		return err
	}
	return nil
}

// UpdateFollowers concurrently updates the follower counts for all feeds
func (fm *FeedsMonitor) UpdateFollowers() {
	var wg sync.WaitGroup
	for _, feed := range fm.Instance.Feeds {
		if feed.Id > 0 {
			wg.Go(func() {
				err := fm.getFollowers(feed)
				if err != nil {
					fmt.Printf("[%s] Error getting followers: %v\n", feed.Name, err)
				}
			})
		}
	}
	wg.Wait()
}

// getFollowers gets the followers count for a feed from the Mastodon API
func (fm *FeedsMonitor) getFollowers(feed *Feed) error {
	b, err := fm.GetFromInstance(fmt.Sprintf("/api/v1/accounts/%d", feed.Id))
	if err != nil {
		return err
	}
	feed.Followers.Store(jsoniter.Get(b, "followers_count").ToInt64())
	return nil
}

// setDefaults sets default values for feeds that don't have them set
func (fm *FeedsMonitor) setDefaults() {

	// Set instance characters limit if not set
	if fm.Instance.Limit == 0 {
		fm.Instance.Limit = fm.getInstanceLimit()
	}

	feedNameReplacer := strings.NewReplacer("\n", "\\n", "\r", "\\r")

	for _, feed := range fm.Instance.Feeds {
		if feed.LastRun == 0 {
			feed.LastRun = fm.LastMonit()
		}
		if feed.Interval == 0 {
			feed.Interval = DefaultCheckInterval
		}

		if !visibilityTypes[feed.Visibility] {
			feed.Visibility = "private"
		}

		if feed.Name == "" {
			url := fasthttp.AcquireURI()
			defer fasthttp.ReleaseURI(url)

			err := url.Parse(nil, s2b(feed.URL))
			if err == nil {
				feed.Name = string(url.Host())
			}
		}
		// Sanitize feed.Name
		feed.Name = feedNameReplacer.Replace(feed.Name)

		// Set empty etag
		empty := make([]byte, 0)
		feed.etag.Store(&empty)

		// Update feed data including ID and followers count
		if err := fm.updateFeedData(feed); err != nil {
			fmt.Println(err)
		}
	}
}

// Get instance characters limit
// If the instance returns a valid limit, it's used; otherwise, the default limit is returned
func (fm *FeedsMonitor) getInstanceLimit() (limit int) {
	limit = DefaultCharacterLimit

	b, err := fm.GetFromInstance("/api/v1/instance")
	if err != nil {
		fmt.Println("Error getting instance data from", fm.Instance.URL, ":", err)
		return
	}
	i := jsoniter.Get(b, "configuration", "statuses", "max_characters").ToInt()
	if i > 0 {
		limit = i
	}
	return
}

// updateFeedData gets the Mastodon account ID and followers count for a feed
// The function verifies the token and retrieves the account ID and followers count
func (fm *FeedsMonitor) updateFeedData(feed *Feed) error {
	if feed.Token == "" {
		return fmt.Errorf("[%s] Missing token", feed.Name)
	}

	b, err := fm.GetFromInstance("/api/v1/accounts/verify_credentials", feed.Token)
	if err != nil {
		return fmt.Errorf("[%s] Unable to get credentials: %w", feed.Name, err)
	}
	id := jsoniter.Get(b, "id").ToInt64()
	if id == 0 {
		return fmt.Errorf("[%s] Invalid token", feed.Name)
	}
	feed.Id = id
	feed.Followers.Store(jsoniter.Get(b, "followers_count").ToInt64())

	return nil
}

// parseURLHost parses a URL and returns the host portion
// It validates that the URL uses HTTPS scheme and has no path traversal attempts
func (fm *FeedsMonitor) parseURLHost(rawURL string) (string, error) {
	url := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(url)

	err := url.Parse(nil, s2b(rawURL))
	if err != nil {
		return "", err
	}

	if b2s(url.Scheme()) != "https" {
		return "", fmt.Errorf("invalid URL scheme: %s", url.Scheme())
	}

	// Validate path doesn't contain traversal
	if strings.Contains(b2s(url.Path()), "../") {
		return "", fmt.Errorf("path traversal not allowed")
	}
	return string(url.Host()), nil
}

func b2s(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func s2b(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
