package rss2masto

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	jsoniter "github.com/json-iterator/go"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

type FeedsMonitor struct {
	Instance struct {
		URL      string  `yaml:"url"`
		Lang     string  `yaml:"lang"`
		Limit    int     `yaml:"limit"`
		TimeZone string  `yaml:"timezone"`
		Save     bool    `yaml:"save,omitempty"`
		Monit    int64   `yaml:"last_monit,omitempty"`
		Feeds    []*Feed `yaml:"feed"`
	} `yaml:"instance"`

	ctxTimeout time.Duration
	lastCheck  atomic.Int64
	lastMonit  atomic.Int64
	location   *time.Location
}

type Feed struct {
	Name        string       `yaml:"name"`
	FeedUrl     string       `yaml:"url"`
	Token       string       `yaml:"token"`
	Prefix      string       `yaml:"prefix,omitempty"`
	Visibility  string       `yaml:"visibility,omitempty"`
	HashLink    string       `yaml:"hashlink,omitempty"`
	ReplaceFrom string       `yaml:"replace_from,omitempty"`
	ReplaceTo   string       `yaml:"replace_to,omitempty"`
	Interval    int64        `yaml:"interval,omitempty"`
	LastRun     int64        `yaml:"last_run,omitempty"`
	Count       int64        `yaml:"-"`
	Id          int64        `yaml:"-"`
	Followers   atomic.Int64 `yaml:"-"`
	Progress    atomic.Int64 `yaml:"-"`
	SendTime    time.Time    `yaml:"-"`
}

const DefaultCharacterLimit = 500 // default mastodon max character limit
const DefaultCheckInterval = 10   // default check feed interval in minutes

var (
	configFile      = "./feed.yml"
	visibilityTypes = map[string]struct{}{
		"public":   {},
		"unlisted": {},
		"private":  {},
	}
	// use this to convert strings to title case (instead of deprecated strings.Title())
	casesTitle cases.Caser
)

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
	fm.lastMonit.Store(fm.Instance.Monit)

	// Set LastMonit to now -55 min if not set or older than 1 hour
	if fm.Instance.Monit == 0 || time.Now().UTC().Sub(time.Unix(fm.Instance.Monit, 0)).Hours() > 1 {
		t := time.Now().UTC().Truncate(time.Minute).Add(time.Minute * time.Duration(-55))
		fm.lastMonit.Store(t.Unix())
	}

	// load location for time formatting
	fm.location, err = time.LoadLocation(fm.Instance.TimeZone)
	if err != nil {
		fmt.Println(err)
		fm.location = time.UTC
	}

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

	// Set instance characters limit if not set
	if fm.Instance.Limit == 0 {
		fm.Instance.Limit = fm.getInstanceLimit()
	}

	// Set user ID on feed
	err = fm.setFeedsId()
	if err != nil {
		fmt.Println(err)
	}

	// Set default values for feeds
	fm.setDefaultValues()

	// other initializations
	fm.ctxTimeout = time.Duration(60/(len(fm.Instance.Feeds)+1)) * time.Second

	return &fm, nil
}

func (fm *FeedsMonitor) setDefaultValues() {

	feedNameReplacer := strings.NewReplacer("\n", "\\n", "\r", "\\r")

	for _, feed := range fm.Instance.Feeds {
		if feed.LastRun == 0 {
			feed.LastRun = fm.LastMonit()
		}
		if feed.Interval == 0 {
			feed.Interval = DefaultCheckInterval
		}
		feed.LastRun += 60 * feed.Interval

		if _, ok := visibilityTypes[feed.Visibility]; !ok {
			feed.Visibility = "private"
		}
		if feed.Name == "" {
			u, err := url.Parse(feed.FeedUrl)
			if err == nil {
				feed.Name = u.Hostname()
			}
		}
		// Sanitize feed.Name
		feed.Name = feedNameReplacer.Replace(feed.Name)
	}
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
	if fm.Instance.URL == "" {
		return
	}

	var wg sync.WaitGroup
	for _, feed := range fm.Instance.Feeds {
		if feed.Id > 0 {
			wg.Add(1)
			go func(feed *Feed) {
				defer wg.Done()
				err := fm.getFollowers(feed)
				if err != nil {
					fmt.Println(feed.Name, err)
				}
			}(feed)
		}
	}
	wg.Wait()
}

func (fm *FeedsMonitor) getFollowers(feed *Feed) error {
	urlAccount := fmt.Sprintf("%s/api/v1/accounts/%d", fm.Instance.URL, feed.Id)
	if err := fm.validateURL(urlAccount); err != nil {
		return fmt.Errorf("invalid instance URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlAccount, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s Received non-OK HTTP status: %d", feed.Name, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	followersCount := jsoniter.Get(body, "followers_count")
	if followersCount.ValueType() != jsoniter.NumberValue {
		return fmt.Errorf("%s JSON not having number value", feed.Name)
	}
	feed.Followers.Store(followersCount.ToInt64())
	return nil
}

// Get instance characters limit
func (fm *FeedsMonitor) getInstanceLimit() (limit int) {
	limit = DefaultCharacterLimit

	if fm.Instance.URL == "" {
		return
	}

	instanceURL := fm.Instance.URL + "/api/v1/instance"
	if err := fm.validateURL(instanceURL); err != nil {
		fmt.Println("Invalid instance URL:", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instanceURL, nil)
	if err != nil {
		fmt.Println("Error creating request:", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("Error getting instance data from", fm.Instance.URL)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Received non-OK HTTP status: %d\n", resp.StatusCode)
		return
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return
	}

	i := jsoniter.Get(body, "configuration", "statuses", "max_characters").ToInt()
	if i > 0 {
		limit = i
	}
	return
}

func (fm *FeedsMonitor) setFeedsId() error {
	if fm.Instance.URL == "" {
		return fmt.Errorf("instance URL is empty")
	}

	for _, feed := range fm.Instance.Feeds {
		if err := fm.updateFeedData(feed); err != nil {
			fmt.Println(err)
			continue
		}
	}

	return nil
}

func (fm *FeedsMonitor) updateFeedData(feed *Feed) error {
	if feed.Token == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	credentialsURL := fm.Instance.URL + "/api/v1/accounts/verify_credentials"
	if err := fm.validateURL(credentialsURL); err != nil {
		return fmt.Errorf("%s Invalid credentials URL: %w", feed.Name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, credentialsURL, nil)
	if err != nil {
		return fmt.Errorf("%s Unable to create new request: %w", feed.Name, err)
	}
	req.Header.Set("Authorization", "Bearer "+feed.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s Unable to execute request: %w", feed.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s Received non-200 status code: %d", feed.Name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s Unable to read response body: %w", feed.Name, err)
	}

	feed.Id = jsoniter.Get(body, "id").ToInt64()
	feed.Followers.Store(jsoniter.Get(body, "followers_count").ToInt64())

	return nil
}

func (fm *FeedsMonitor) validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}

	if u.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs allowed")
	}

	// Validate path doesn't contain traversal
	if strings.Contains(u.Path, "..") {
		return fmt.Errorf("path traversal not allowed")
	}

	// Block private/internal IP ranges
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsPrivate() || ip.IsLoopback() {
			return fmt.Errorf("private/internal IPs not allowed")
		}
	}
	return nil
}
