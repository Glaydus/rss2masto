package rss2masto

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"github.com/valyala/fasthttp"
	"github.com/zeebo/xxh3"
)

const (
	earlierDuration = -time.Hour * 12
)

var strictPolicy = bluemonday.StrictPolicy()

// Start processes all feeds in parallel using goroutines
// For each feed with valid URL and token:
// - Increments sheduler counter
// - When shedCounter reaches interval, resets counter and processes feed
// - Updates last check timestamp
// - Saves feed data if configured
func (fm *FeedsMonitor) Start() {

	if len(fm.Instance.Feeds) == 0 {
		return
	}

	if fm.isStarted.Swap(true) {
		return
	}
	defer fm.isStarted.Store(false)

	var wg sync.WaitGroup
	for _, feed := range fm.Instance.Feeds {
		if feed.URL == "" || feed.Token == "" {
			continue
		}
		if feed.shedCounter.Add(1) >= feed.Interval {
			feed.shedCounter.Store(0)
			fm.lastCheck.Store(time.Now().Unix())
			wg.Go(func() {
				fm.GetFeed(feed)
			})
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

// GetFeed retrieves and processes items from a feed
// For each item in the feed:
// - Checks if item is within time limits
// - Generates idempotency key based on item GUID
// - Skips if item already processed
// - Sanitizes title and description
// - Applies replacement rules if configured
// - Constructs message with title, description, hashtags and link
// - Sends post to mastodon instance
// - Updates counters and timestamps
func (fm *FeedsMonitor) GetFeed(f *Feed) {

	feed := fm.Parser.FetchAndParse(f)
	if feed == nil {
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

		if Cache.ValueExists(idempotencyKey) {
			continue
		}

		hashtags := makeHashtags(item, f, reTag)
		title, description := fm.sanitizeMessage(item, len(hashtags))

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

		if !debugMode {
			func() {

				req := fasthttp.AcquireRequest()
				defer fasthttp.ReleaseRequest(req)

				// Prepare post data
				post := MastodonPost{
					Status:     msg,
					Visibility: f.Visibility,
				}
				if len(lang) == 2 {
					post.Language = lang
				}

				// Writing directly to BodyWriter() saves one []byte allocation
				err := jsoniter.ConfigDefault.NewEncoder(req.BodyWriter()).Encode(post)
				if err != nil {
					fmt.Printf("Jsoniter error: %v\n", err)
					return
				}

				req.Header.SetContentType("application/json")
				req.Header.Set("Authorization", "Bearer "+f.Token)
				req.Header.Set("Idempotency-Key", idempotencyKey)

				err = fm.PostToInstance(req)
				if err != nil {
					fmt.Printf("[%s] Mastodon post error: %v\n", f.Name, err)
					return
				}

				f.Count++
				f.SendTime = time.Now().In(fm.Location())

				err = Cache.Store(idempotencyKey, "1")
				if err != nil {
					fmt.Printf("[%s] Cache store error: %v\n", f.Name, err)
				}

				if f.LastRun < pubUnixTime {
					f.LastRun = pubUnixTime
				}
				if f.LastRun > fm.LastMonit() {
					fm.lastMonit.Store(f.LastRun)
				}
			}()
		} else {
			//os.WriteFile(idempotencyKey, []byte(msg), 0600)
		}
	}
}

// GetFromInstance performs a GET request to the specified endpoint on the Mastodon instance.
func (fm *FeedsMonitor) GetFromInstance(endpoint string, token ...string) ([]byte, error) {
	target := fm.Instance.URL + endpoint

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	url := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(url)

	err := url.Parse(nil, s2b(target))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	req.SetURI(url)
	req.Header.SetMethod(fasthttp.MethodGet)
	req.Header.Set("Accept", "application/json")
	if len(token) > 0 {
		req.Header.Set("Authorization", "Bearer "+token[0])
	}

	if err := fm.hostClient.Do(req, resp); err != nil {
		return nil, err
	}

	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, fmt.Errorf("Received non-OK HTTP status: %d", resp.StatusCode())
	}
	return resp.Body(), nil
}

// PostToInstance performs a POST request to the Mastodon instance's API endpoint for creating statuses.
func (fm *FeedsMonitor) PostToInstance(req *fasthttp.Request) error {
	target := fm.Instance.URL + "/api/v1/statuses"

	url := fasthttp.AcquireURI()
	defer fasthttp.ReleaseURI(url)

	err := url.Parse(nil, s2b(target))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	req.SetURI(url)
	req.Header.SetMethod(fasthttp.MethodPost)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	if err := fm.hostClient.Do(req, resp); err != nil {
		return err
	}
	statusCode := resp.StatusCode()

	if statusCode == fasthttp.StatusTooManyRequests {
		retryAfter := resp.Header.Peek("Retry-After")
		return fmt.Errorf("rate limited, retry after: %s seconds", retryAfter)
	}

	if statusCode != fasthttp.StatusOK {
		if statusCode < fasthttp.StatusInternalServerError {
			return fmt.Errorf("Post returned status: %d [%s]", statusCode, resp.Body())
		}
		return fmt.Errorf("Post returned status: %d", statusCode)
	}
	return nil
}

// FetchAndParse fetches and parses a feed from the given URL.
// It returns a parsed feed or nil if an error occurs.
func (p *Parser) FetchAndParse(f *Feed) *gofeed.Feed {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(f.URL)
	req.Header.SetMethod(fasthttp.MethodGet)
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Accept", "application/xml, text/xml, */*")

	currentEtag := *f.etag.Load()
	if len(currentEtag) > 0 {
		req.Header.SetBytesV("If-None-Match", currentEtag)
	}

	if err := p.Client.Do(req, resp); err != nil {
		fmt.Printf("[%s] Error fetching: %v\n", f.Name, err)
		return nil
	}

	if resp.StatusCode() == fasthttp.StatusNotModified {
		return nil
	}

	if resp.StatusCode() == fasthttp.StatusOK {
		newEtag := resp.Header.Peek("ETag")
		if len(newEtag) > 0 && !bytes.Equal(currentEtag, newEtag) {
			etagCopy := append([]byte(nil), newEtag...)
			f.etag.Store(&etagCopy)
		}

		fp := p.parserPool.Get().(*gofeed.Parser)
		defer p.parserPool.Put(fp)

		result, err := fp.Parse(bytes.NewReader(resp.Body()))
		if err != nil {
			fmt.Printf("[%s] Error parsing: %v\n", f.Name, err)
			return nil
		}
		return result
	}
	fmt.Printf("[%s] Failed to fetch, status code: %d\n", f.Name, resp.StatusCode())
	return nil
}

// sanitizeMessage cleans up the message content and title
func (fm *FeedsMonitor) sanitizeMessage(item *gofeed.Item, tagsLen int) (title, description string) {
	description = item.Description
	if item.Content != "" {
		description = item.Content
	}
	description = strictPolicy.Sanitize(description)
	description = html.UnescapeString(strings.TrimSpace(description))
	title = html.UnescapeString(item.Title)

	// Check if the post is too long
	l := len(title) + tagsLen + len(item.Link)
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
	return
}

var replacer = strings.NewReplacer(" - ", " ", " i ", ": ")

// makeHashtags constructs hashtags from item categories or link matching
// If categories exist, they are processed to create hashtags
// If HashLink regex is provided, it's used to extract hashtags from the item link
// Prefix is added to hashtags if specified
func makeHashtags(item *gofeed.Item, f *Feed, re *regexp.Regexp) (hashtags string) {
	var aTags []string

	if item.Categories != nil {
		for _, tag := range item.Categories {
			tag = strings.TrimSpace(tag)
			tag = replacer.Replace(tag)
			// tag = strings.Title(tag) // deprecated
			tag = casesTitle.String(tag)

			tag = strings.ReplaceAll(tag, " ", "")
			a := strings.SplitSeq(tag, ":")
			for s := range a {
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
			for i := range l {
				if !strings.Contains(aTags[i], f.Prefix) {
					aTags = append(aTags, f.Prefix+casesTitle.String(aTags[i]))
				}
			}
		}
		hashtags = "#" + strings.Join(aTags, " #")
	}
	return
}

// hashString returns a hash of the given string
// Uses xxh3 hash algorithm for hashing
// The hash is returned as a string representation of the uint64 hash value
func hashString(s string) string {
	return strconv.FormatUint(xxh3.HashString(s), 10)
}
