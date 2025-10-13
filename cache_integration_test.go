package rss2masto

import (
	"context"
	"testing"
	"time"

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

	cache := &CacheClient{
		client: client,
		ctx:    context.Background(),
	}

	t.Run("Set and Get", func(t *testing.T) {
		key := "test:integration:key"
		value := "test_value"

		err := cache.Set(key, value, time.Minute)
		if err != nil {
			t.Fatalf("Set failed: %v", err)
		}

		result, err := cache.Get(key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if result != value {
			t.Errorf("Expected %s, got %s", value, result)
		}
	})

	t.Run("Exists", func(t *testing.T) {
		key := "test:integration:exists"
		cache.Set(key, "value", time.Minute)

		if !cache.Exists(key) {
			t.Error("Key should exist")
		}

		if cache.Exists("nonexistent:key") {
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

		err := cache.ZAdd(key, members)
		if err != nil {
			t.Fatalf("ZAdd failed: %v", err)
		}

		result, err := cache.ZRevRange(key, 0, -1)
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
		// Set up test data
		cache.Set("test:pattern:1", "value1", time.Minute)
		cache.Set("test:pattern:2", "value2", time.Minute)
		cache.Set("other:key", "value3", time.Minute)

		keys, err := cache.GetKeys("test:pattern:*")
		if err != nil {
			t.Fatalf("GetKeys failed: %v", err)
		}

		if len(keys) < 2 {
			t.Errorf("Expected at least 2 keys matching pattern, got %d", len(keys))
		}
	})

	t.Run("MGet", func(t *testing.T) {
		keys := []string{"test:mget:1", "test:mget:2", "test:mget:nonexistent"}
		cache.Set(keys[0], "value1", time.Minute)
		cache.Set(keys[1], "value2", time.Minute)

		results, err := cache.MGet(keys)
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

	t.Run("Clear", func(t *testing.T) {
		cache.Set("test:clear:key", "value", time.Minute)

		err := cache.Clear()
		if err != nil {
			t.Fatalf("Clear failed: %v", err)
		}

		if cache.Exists("test:clear:key") {
			t.Error("Key should not exist after clear")
		}
	})
}
