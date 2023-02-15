package rss2masto

import (
	"io"
	"log"
	"net/http"
	"os"
	"time"

	jsoniter "github.com/json-iterator/go"
	"gopkg.in/yaml.v3"
)

var ConfigFile = "./feed.yml"

var visibilityTypes = map[string]struct{}{
	"public":   {},
	"unlisted": {},
	"private":  {},
}

type FeedsMonitor struct {
	Instance struct {
		URL       string  `yaml:"url"`
		Limit     int     `yaml:"limit"`
		Save      bool    `yaml:"save,omitempty"`
		LastMonit int64   `yaml:"last_monit,omitempty"`
		Feeds     []*Feed `yaml:"feed"`
	} `yaml:"instance"`
}

type Feed struct {
	Name        string `yaml:"name"`
	FeedUrl     string `yaml:"url"`
	Token       string `yaml:"token"`
	Prefix      string `yaml:"prefix,omitempty"`
	Visibility  string `yaml:"visibility,omitempty"`
	HashLink    string `yaml:"hashlink,omitempty"`
	ReplaceFrom string `yaml:"replace_from,omitempty"`
	ReplaceTo   string `yaml:"replace_to,omitempty"`
	LastRun     int64  `yaml:"last_run,omitempty"`
	Count       int    `yaml:"count,omitempty"`
}

func NewFeedsMonitor() (*FeedsMonitor, error) {
	var fm FeedsMonitor

	file, err := os.ReadFile(ConfigFile)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(file, &fm)
	if err != nil {
		return nil, err
	}

	// Set LastMonit to now -50 min if not set or older than 1 hour
	if fm.Instance.LastMonit == 0 || time.Now().Sub(time.Unix(fm.Instance.LastMonit, 0)).Hours() > 1 {
		fm.Instance.LastMonit = time.Now().UTC().Add(time.Minute * time.Duration(-50)).Unix() // Now() -50 min
	}

	// Set instance characters limit if not set
	if fm.Instance.Limit == 0 {
		fm.Instance.Limit = getInstanceLimit(&fm)
	}

	return &fm, nil
}

func (f *FeedsMonitor) SaveFeedsData() error {
	out, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	err = os.WriteFile(ConfigFile, out, 0644)
	if err != nil {
		return err
	}
	return nil
}

// Get instance characters limit
func getInstanceLimit(fm *FeedsMonitor) (limit int) {
	limit = 500 // default mastodon limit

	if fm.Instance.URL != "" {
		resp, _ := http.Get(fm.Instance.URL + "/api/v1/instance")
		if resp == nil {
			log.Println("Error getting instance data")
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		i := jsoniter.Get(body, "configuration", "statuses", "max_characters").ToInt()
		if i > 0 {
			limit = i
		}
	}
	return
}
