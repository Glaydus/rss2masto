package rss2masto

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
)

const (
	earlierDuration = -time.Hour * 12
	storageDuration = time.Hour * 24 * 7
)

var strictPolicy = bluemonday.StrictPolicy()

// Start processes all feeds in parallel using goroutines
// For each feed with valid URL and token:
// - Increments progress counter
// - When progress reaches interval, resets counter and processes feed
// - Updates last check timestamp
// - Saves feed data if configured
func (fm *FeedsMonitor) Start() {

	if len(fm.Instance.Feeds) == 0 {
		return
	}

	var wg sync.WaitGroup
	wg.Add(len(fm.Instance.Feeds))

	for _, feed := range fm.Instance.Feeds {
		if feed.FeedUrl != "" && feed.Token != "" {
			go func(f *Feed) {
				defer wg.Done()
				f.Progress.Add(1)
				if f.Progress.Load() >= f.Interval {
					f.Progress.Store(0)
					fm.lastCheck.Store(time.Now().Unix())
					fm.getFeed(f)
				}
			}(feed)
		} else {
			wg.Done()
		}
	}
	wg.Wait()

	if fm.Instance.Save {
		err := fm.SaveFeedsData()
		if err != nil {
			fmt.Println("Error saving config file:", err)
		}
	}
}

//gocyclo:ignore
func (fm *FeedsMonitor) getFeed(f *Feed) {

	ctx, cancel := context.WithTimeout(context.Background(), fm.ctxTimeout)
	defer cancel()

	feed, err := fm.feedParser.ParseURLWithContext(f.FeedUrl, ctx)
	if err != nil {
		fmt.Println(f.Name, "Parsing error:", err)
		return
	}

	// Sort by date descending
	if feed.FeedType == "atom" {
		sort.Slice(feed.Items, func(i, j int) bool {
			return feed.Items[i].UpdatedParsed.Unix() > feed.Items[j].UpdatedParsed.Unix()
		})
	} else {
		sort.Slice(feed.Items, func(i, j int) bool {
			return feed.Items[i].PublishedParsed.Unix() > feed.Items[j].PublishedParsed.Unix()
		})
	}

	var reReplace, reTag *regexp.Regexp

	if f.ReplaceFrom != "" {
		reReplace, _ = regexp.Compile(f.ReplaceFrom)
	}
	if f.HashLink != "" {
		reTag, _ = regexp.Compile(f.HashLink)
	}

	now := time.Now().UTC()
	limitUnixTime := now.Add(earlierDuration).Unix()

	for i := len(feed.Items) - 1; i >= 0; i-- {
		item := feed.Items[i]
		pubUnixTime := item.PublishedParsed.Unix()
		if feed.FeedType == "atom" {
			pubUnixTime = item.UpdatedParsed.Unix()
		}

		if pubUnixTime < limitUnixTime {
			continue
		}
		if pubUnixTime > now.Unix() {
			pubUnixTime = now.Unix()
		}
		if pubUnixTime < f.LastRun {
			continue
		}

		idempotencyKey := f.Name[:2] + ":" + hashString(item.GUID)

		if Cache() != nil {
			if Cache().Exists(idempotencyKey) {
				continue
			}
		} else if pubUnixTime <= f.LastRun {
			continue
		}

		description := item.Description
		if item.Content != "" {
			description = item.Content
		}
		description = strictPolicy.Sanitize(description)
		description = html.UnescapeString(strings.TrimSpace(description))
		title := html.UnescapeString(item.Title)
		hashtags := makeHashtags(item, f, reTag)

		// Check if the post is too long
		l := len(title) + len(hashtags) + len(item.Link)
		if l+len(description) > fm.Instance.Limit {
			n := fm.Instance.Limit - l - 11
			description = description[:n]
			n = strings.LastIndexAny(description, " .,;!?")
			if n > 0 {
				description = description[:n+1]
				if description[n] == ' ' {
					description = description[:n]
				}
			}
			l = len(description)
			if description[l-2] == ' ' {
				description = description[:l-2]
			}
			description = description + " [...]"
		}
		if reReplace != nil {
			description = reReplace.ReplaceAllString(description, f.ReplaceTo)
			description = strings.TrimSpace(description)
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
			sb.WriteString("\n\n")
		}
		sb.WriteString(item.Link)
		msg := sb.String()

		lang := feed.Language

		if len(lang) != 2 {
			if len(lang) > 2 {
				lang = lang[:2]
			} else {
				lang = fm.Instance.Lang
			}
		}

		data := url.Values{}
		data.Set("status", msg)
		data.Set("visibility", f.Visibility)
		if len(lang) == 2 {
			data.Set("language", lang)
		}

		if !debugMode {
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), fm.ctxTimeout)
				defer cancel()

				req, err := createRequest(ctx, fm.Instance.URL, idempotencyKey, f.Token, strings.NewReader(data.Encode()))
				if err != nil {
					fmt.Println("Error creating request:", err)
					return
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					fmt.Println(f.Name, "Mastodon post error:", err)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode == http.StatusOK {
					f.Count++
					f.SendTime = time.Now().In(fm.Location())
					if Cache() != nil {
						_ = Cache().Set(idempotencyKey, "1", storageDuration)
					}
					if f.LastRun < pubUnixTime {
						f.LastRun = pubUnixTime
					}
					if f.LastRun > fm.LastMonit() {
						fm.lastMonit.Store(f.LastRun)
					}
				}
			}()
		}
	}
}

func createRequest(ctx context.Context, url, key, token string, data *strings.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/api/v1/statuses", data)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

var replacer = strings.NewReplacer(" - ", " ", " i ", ": ")

func makeHashtags(item *gofeed.Item, f *Feed, re *regexp.Regexp) (hashtags string) {
	var aTags []string

	if item.Categories != nil {
		for _, tag := range item.Categories {
			tag = strings.TrimSpace(tag)
			tag = replacer.Replace(tag)
			// tag = strings.Title(tag) // deprecated
			tag = casesTitle.String(tag)

			tag = strings.ReplaceAll(tag, " ", "")
			a := strings.Split(tag, ":")
			for _, s := range a {
				if !strings.ContainsAny(s, `-\/.`) {
					aTags = append(aTags, s)
				}
			}
		}
	} else {
		if re != nil {
			res := re.FindAllStringSubmatch(item.Link, 1)
			if (len(res) != 0) && (len(res[0]) == 2) {
				tag := res[0][1]
				if !strings.Contains(tag, "-") {
					aTags = append(aTags, tag)
				}
			}
		}
	}

	l := len(aTags)
	if l > 0 {
		if f.Prefix != "" {
			for i := 0; i < l; i++ {
				if !strings.Contains(aTags[i], f.Prefix) {
					aTags = append(aTags, f.Prefix+casesTitle.String(aTags[i]))
				}
			}
		}
		hashtags = "#" + strings.Join(aTags, " #")
	}
	return
}

func hashString(s string) string {
	return strconv.FormatUint(xxhash.Sum64String(s), 10)
}
