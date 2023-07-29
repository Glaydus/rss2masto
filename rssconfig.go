package rss2masto

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	jsoniter "github.com/json-iterator/go"
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
	wg         sync.WaitGroup
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
)

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

	// Set LastMonit to now -50 min if not set or older than 1 hour
	if fm.Instance.Monit == 0 || time.Now().Sub(time.Unix(fm.Instance.Monit, 0)).Hours() > 1 {
		fm.lastMonit.Store(time.Now().UTC().Add(time.Minute * time.Duration(-50)).Unix()) // Now() -50 min
	}

	// load location for time formatting
	fm.location, err = time.LoadLocation(fm.Instance.TimeZone)
	if err != nil {
		fmt.Println(err)
		fm.location = time.UTC
	}

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
	for _, feed := range fm.Instance.Feeds {
		if feed.LastRun == 0 {
			feed.LastRun = fm.LastMonit()
		}
		if feed.Interval == 0 {
			feed.Interval = DefaultCheckInterval
		}
		if _, ok := visibilityTypes[feed.Visibility]; !ok {
			feed.Visibility = "private"
		}
		if feed.Name == "" {
			u, err := url.Parse(feed.FeedUrl)
			if err == nil {
				feed.Name = u.Hostname()
			}
		}
	}
	return &fm, nil
}

func (fm *FeedsMonitor) LastCheck() int64 {
	return fm.lastCheck.Load()
}

func (fm *FeedsMonitor) LastCheckStr() string {
	sec := fm.lastCheck.Load()
	if sec == 0 {
		return ""
	}
	return time.Unix(sec, 0).In(fm.Location()).Format(time.DateTime)
}

func (fm *FeedsMonitor) LastMonit() int64 {
	return fm.lastMonit.Load()
}

func (fm *FeedsMonitor) FeedIndex(name string) int {
	for i, feed := range fm.Instance.Feeds {
		if strings.HasPrefix(feed.Name, name) {
			return i
		}
	}
	return -1
}

func (fm *FeedsMonitor) Location() *time.Location {
	return fm.location
}

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
					fmt.Println(err)
				}
			}(feed)
		}
	}
	wg.Wait()
}

func (fm *FeedsMonitor) getFollowers(feed *Feed) error {
	urlAccount := fmt.Sprintf("%s/api/v1/accounts/%d", fm.Instance.URL, feed.Id)
	resp, err := http.Get(urlAccount)
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

	resp, err := http.Get(fm.Instance.URL + "/api/v1/instance")
	if err != nil {
		fmt.Println("Error getting instance data from", fm.Instance.URL)
		return
	}
	defer resp.Body.Close()

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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fm.Instance.URL+"/api/v1/accounts/verify_credentials", nil)
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
