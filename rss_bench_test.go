package rss2masto

import (
	"sort"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

// makeBenchItems generates n feed items with timestamps spread 1 minute apart.
// Items are created in ascending order so each benchmark run starts from the
// same "worst-case for descending sort" state.
func makeBenchItems(n int) []*gofeed.Item {
	items := make([]*gofeed.Item, n)
	base := time.Now()
	for i := range n {
		t := base.Add(time.Duration(i) * time.Minute) // ascending
		items[i] = &gofeed.Item{PublishedParsed: &t}
	}
	return items
}

// cloneItems returns a shallow copy so each b.Loop() iteration starts from
// the same unsorted state.
func cloneItems(src []*gofeed.Item) []*gofeed.Item {
	dst := make([]*gofeed.Item, len(src))
	copy(dst, src)
	return dst
}

// BenchmarkSortUnix — current implementation: compare via Unix().
func BenchmarkSortUnix(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		items := makeBenchItems(n)
		b.Run(benchName(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				data := cloneItems(items)
				sort.Slice(data, func(i, j int) bool {
					return data[i].PublishedParsed.Unix() > data[j].PublishedParsed.Unix()
				})
			}
		})
	}
}

// BenchmarkSortAfter — proposed implementation: compare via time.Time.After().
func BenchmarkSortAfter(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		items := makeBenchItems(n)
		b.Run(benchName(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				data := cloneItems(items)
				sort.Slice(data, func(i, j int) bool {
					return data[i].PublishedParsed.After(*data[j].PublishedParsed)
				})
			}
		})
	}
}

func benchName(n int) string {
	switch n {
	case 10:
		return "n=10"
	case 100:
		return "n=100"
	case 1000:
		return "n=1000"
	}
	return "n=?"
}
