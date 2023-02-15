package rss2masto

import (
	"context"
	"crypto/md5"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
)

func (fm *FeedsMonitor) Start() {

	var maxTime int64

	for _, feed := range fm.Instance.Feeds {

		if !fm.checkFeed(feed) {
			continue
		}

		fm.getFeed(feed)

		if feed.LastRun > maxTime {
			maxTime = feed.LastRun
		}
	}
	fm.Instance.LastMonit = maxTime

	if fm.Instance.Save {
		err := fm.SaveFeedsData()
		if err != nil {
			log.Println("Error saving config file: ", err)
		}
	}
}

func (fm *FeedsMonitor) checkFeed(feed *Feed) bool {
	if feed.FeedUrl == "" || feed.Token == "" {
		return false
	}
	if feed.LastRun == 0 {
		feed.LastRun = fm.Instance.LastMonit
	}
	if _, ok := visibilityTypes[feed.Visibility]; !ok {
		feed.Visibility = "private"
	}
	return true
}

func (fm *FeedsMonitor) getFeed(f *Feed) {

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fp := gofeed.NewParser()
	feed, err := fp.ParseURLWithContext(f.FeedUrl, ctx)
	if err != nil {
		log.Println("Error parsing feed: ", err)
		return
	}

	// Sort by date descending
	sort.Slice(feed.Items, func(i, j int) bool {
		return feed.Items[i].PublishedParsed.Unix() > feed.Items[j].PublishedParsed.Unix()
	})

	var reReplace *regexp.Regexp

	if f.ReplaceFrom != "" {
		reReplace, _ = regexp.Compile(f.ReplaceFrom)
	}

	pol := bluemonday.StrictPolicy()

	for i := len(feed.Items) - 1; i >= 0; i-- {
		item := feed.Items[i]

		pubUnixTime := item.PublishedParsed.Unix()
		if pubUnixTime <= f.LastRun {
			continue
		}
		f.LastRun = pubUnixTime

		description := pol.Sanitize(item.Description)
		if reReplace != nil {
			description = reReplace.ReplaceAllString(description, f.ReplaceTo)
		}
		description = html.UnescapeString(strings.TrimSpace(description))
		title := html.UnescapeString(item.Title)
		hashtags := makeHasztags(item, f)

		// Check if the post is too long
		l := len(title) + len(hashtags) + len(item.Link)
		if l+len(description) > fm.Instance.Limit {
			n := fm.Instance.Limit - l - 12
			description = description[:n] + " [...]"
		}

		sb := strings.Builder{}
		sb.WriteString(title)
		sb.WriteString("\n\n")

		if description != "" {
			sb.WriteString(description)
			sb.WriteString("\n\n")
		}
		if hashtags != "" {
			sb.WriteString(hashtags)
			sb.WriteString("\n")
		}
		sb.WriteString(item.Link)
		msg := sb.String()

		idempotencyKey := fmt.Sprintf("%x", md5.Sum([]byte(item.GUID)))

		data := url.Values{}
		data.Set("status", msg)
		data.Set("visibility", f.Visibility)
		data.Set("language", feed.Language)

		req, _ := http.NewRequest("POST", fm.Instance.URL+"/api/v1/statuses", strings.NewReader(data.Encode()))
		req.Header.Set("Authorization", "Bearer "+f.Token)
		req.Header.Set("Idempotency-Key", idempotencyKey)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.Println("Error posting to Mastodon", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			f.Count++
		}

	}
}

func makeHasztags(item *gofeed.Item, f *Feed) (hashtags string) {
	var aTags []string

	if item.Categories != nil {
		for _, tag := range item.Categories {
			tag = strings.TrimSpace(tag)
			tag = strings.ReplaceAll(tag, " - ", " ")
			tag = strings.Title(tag)
			tag = strings.ReplaceAll(tag, " ", "")
			a := strings.FieldsFunc(tag, splitter)
			for _, s := range a {
				if !strings.ContainsAny(s, `-\/`) {
					aTags = append(aTags, s)
				}
			}
		}
	} else {
		reTag, _ := regexp.Compile(f.HashLink)
		if reTag != nil {
			res := reTag.FindAllStringSubmatch(item.Link, 1)
			if len(res) != 0 {
				tag := res[0][1]
				if !strings.Contains(tag, "-") {
					aTags = append(aTags, tag)
				}
			}
		}
	}

	l := len(aTags)
	if l != 0 {
		if f.Prefix != "" {
			for i := 0; i < l; i++ {
				if !strings.Contains(aTags[i], f.Prefix) {
					aTags = append(aTags, f.Prefix+strings.Title(aTags[i]))
				}
			}
		}
		hashtags = "#" + strings.Join(aTags, " #")
	}
	return
}

func splitter(r rune) bool {
	return r == '.' || r == ':'
}
