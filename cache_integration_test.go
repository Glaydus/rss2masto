package rss2masto

import (
	"context"
	"testing"
	"time"

	rediscache "github.com/go-redis/cache/v9"
	"github.com/redis/go-redis/v9"
)

// TestCacheClient_Integration tests the cache client with a real Redis interface
// This test requires a running Redis instance or can be skipped
//
//gocyclo:ignore
func TestCacheClient_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a test Redis client
	client := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1, // Use test database
	})

	// Test connection
	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	// Clean up test database
	defer client.FlushDB(context.Background())

	cc := &CacheClient{
		client: client,
		ctx:    context.Background(),
		cache: rediscache.New(&rediscache.Options{
			Redis: client,
		}),
	}

	t.Run("Set and Get", func(t *testing.T) {
		key := "test:integration:key"
		value := "test_value"

		err := cc.Set(key, value, time.Minute)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		result, err := cc.Get(key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if result != value {
			t.Errorf("Expected %s, got %s", value, result)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		key := "test:integration:exists"
		cc.Set(key, "value", time.Minute)

		if !cc.Exists(key) {
			t.Error("Key should exist")
		}

		if cc.Exists("nonexistent:key") {
			t.Error("Nonexistent key should not exist")
		}
	})

	t.Run("ZAdd and ZRevRange", func(t *testing.T) {
		key := "test:integration:zset"
		members := []redis.Z{
			{Score: 1.0, Member: "first"},
			{Score: 2.0, Member: "second"},
			{Score: 3.0, Member: "third"},
		}

		err := cc.ZAdd(key, members)
		if err != nil {
			t.Fatalf("ZAdd failed: %v", err)
		}

		result, err := cc.ZRevRange(key, 0, -1)
		if err != nil {
			t.Fatalf("ZRevRange failed: %v", err)
		}

		expected := []string{"third", "second", "first"}
		if len(result) != len(expected) {
			t.Errorf("Expected %d members, got %d", len(expected), len(result))
		}

		for i, member := range result {
			if member != expected[i] {
				t.Errorf("Expected %s at position %d, got %s", expected[i], i, member)
			}
		}
	})

	t.Run("GetKeys", func(t *testing.T) {
		cc.Set("test:pattern:1", "value1", time.Minute)
		cc.Set("test:pattern:2", "value2", time.Minute)
		cc.Set("other:key", "value3", time.Minute)

		keys, err := cc.GetKeys("test:pattern:*")
		if err != nil {
			t.Fatalf("GetKeys failed: %v", err)
		}

		if len(keys) < 2 {
			t.Errorf("Expected at least 2 keys matching pattern, got %d", len(keys))
		}
	})

	t.Run("MGet", func(t *testing.T) {
		keys := []string{"test:mget:1", "test:mget:2", "test:mget:nonexistent"}
		cc.Set(keys[0], "value1", time.Minute)
		cc.Set(keys[1], "value2", time.Minute)

		results, err := cc.MGet(keys)
		if err != nil {
			t.Fatalf("MGet failed: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}

		if results[0] != "value1" || results[1] != "value2" || results[2] != nil {
			t.Errorf("Unexpected MGet results: %v", results)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		key := "test:integration:delete"
		cc.Set(key, "value", time.Minute)

		if !cc.Exists(key) {
			t.Error("Key should exist before deletion")
		}

		err := cc.Delete(key)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		if cc.Exists(key) {
			t.Error("Key should not exist after deletion")
		}
	})
}
