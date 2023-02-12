package rss2masto

import (
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/valyala/fastjson"
	"gopkg.in/yaml.v3"
)

const _feedFileName = "./feed.yml"

var visibilityTypes = map[string]struct{}{
	"public":   {},
	"unlisted": {},
	"private":  {},
}

type FeedsMonitor struct {
	Monitor struct {
		Last      int64   `yaml:"last"`
		Instance  string  `yaml:"instance"`
		Limit     int     `yaml:"limit"`
		Save      bool    `yaml:"save,omitempty"`
		LastMonit int64   `yaml:"-"`
		Feeds     []*Feed `yaml:"feed"`
	} `yaml:"monitor"`
}

type Feed struct {
	Name       string `yaml:"name"`
	FeedUrl    string `yaml:"url"`
	Token      string `yaml:"token"`
	Language   string `yaml:"language,omitempty"`
	Prefix     string `yaml:"prefix,omitempty"`
	Visibility string `yaml:"visibility,omitempty"`
	Delete     string `yaml:"delete,omitempty"`
	HashLink   string `yaml:"hashlink,omitempty"`
	LastRun    int64  `yaml:"-"`
	Count      int    `yaml:"-"` // Number of posts
}

func NewFeedsMonitor() (*FeedsMonitor, error) {
	var fMonitor FeedsMonitor

	file, err := os.ReadFile(_feedFileName)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(file, &fMonitor)
	if err != nil {
		return nil, err
	}

	// Set last to now -50 min if not set or older than 1 hour
	if fMonitor.Monitor.Last == 0 || time.Now().Sub(time.Unix(fMonitor.Monitor.Last, 0)).Hours() > 1 {
		fMonitor.Monitor.Last = time.Now().UTC().Add(time.Minute * time.Duration(-50)).Unix() // Now() -50 min
	}
	fMonitor.Monitor.LastMonit = fMonitor.Monitor.Last

	// Set instance characters limit if not set
	if fMonitor.Monitor.Limit == 0 {
		fMonitor.Monitor.Limit = getInstanceLimit(&fMonitor)
	}

	return &fMonitor, nil
}

func (f *FeedsMonitor) SaveFeedsData() error {
	out, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	err = os.WriteFile(_feedFileName, out, 0644)
	if err != nil {
		return err
	}
	return nil
}

// Get instance characters limit
func getInstanceLimit(fm *FeedsMonitor) (limit int) {
	limit = 500 // default mastodon limit

	if fm.Monitor.Instance != "" {
		resp, _ := http.Get(fm.Monitor.Instance + "/api/v1/instance")
		if resp == nil {
			log.Println("Error getting instance data")
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		var p fastjson.Parser
		if v, err := p.ParseBytes(body); err == nil {
			if i, err := v.Get("configuration", "statuses", "max_characters").Int(); err == nil {
				limit = i
			}
		}
	}
	return
}
