package rss2masto

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-redis/cache/v9"
	"github.com/redis/go-redis/v9"
)

// CacheClient is a wrapper around the redis client and the cache library
type CacheClient struct {
	client *redis.Client
	cache  *cache.Cache
	ctx    context.Context
}

// Cache is the global cache client
var Cache *CacheClient = newCache()

const (
	storageDuration = 7 * 24 * time.Hour
	twoYearDuration = 2 * 365.25 * 24 * time.Hour
)

func newCache() *CacheClient {
	var opt *redis.Options
	var err error

	if debugMode {
		opt = &redis.Options{
			Addr:     "localhost:6379",
			Password: "", // no password set
			DB:       0,  // use default DB
		}
	} else {
		host := os.Getenv("REDIS_HOST")
		if host == "" {
			panic("REDIS_HOST not set")
		}
		opt, err = redis.ParseURL("redis://" + host)
		if err != nil {
			panic(err)
		}
	}
	opt.ClientName = "rss2masto"
	opt.PoolSize = 20
	opt.MinIdleConns = 5
	opt.PoolTimeout = 4 * time.Second
	opt.DialTimeout = 5 * time.Second
	opt.ConnMaxIdleTime = 30 * time.Second
	opt.ConnMaxLifetime = 5 * time.Minute

	client := redis.NewClient(opt)

	_, err = client.Ping(context.Background()).Result()
	if err != nil {
		panic(err)
	}

	return &CacheClient{
		client: client,
		ctx:    context.Background(),
		cache: cache.New(&cache.Options{
			Redis:        client,
			LocalCache:   cache.NewTinyLFU(5000, storageDuration*2),
			StatsEnabled: true,
		}),
	}
}

// Close closes the redis connection
func (c *CacheClient) Close() {
	err := c.client.Close()
	if err != nil {
		fmt.Printf("Error closing Redis connection: %v", err)
	} else {
		fmt.Println("Redis connection Close()")
	}
}

// Set sets a key-value pair in redis
func (c *CacheClient) Set(key string, value any, expiration time.Duration) error {
	return c.client.Set(c.ctx, key, value, expiration).Err()
}

// Get gets a value from redis
func (c *CacheClient) Get(key string) (string, error) {
	return c.client.Get(c.ctx, key).Result()
}

// GetKeys gets all keys matching a pattern
func (c *CacheClient) GetKeys(keyPattern string, count ...int64) ([]string, error) {
	limit := int64(-1)
	if len(count) > 0 {
		limit = count[0]
	}
	keys, _, err := c.client.Scan(c.ctx, 0, keyPattern, limit).Result()
	return keys, err
}

// GetEx gets a value from redis with an expiration
func (c *CacheClient) GetEx(key string, expiration time.Duration) (string, error) {
	return c.client.GetEx(c.ctx, key, expiration).Result()
}

// GetBytes gets a value from redis as bytes
func (c *CacheClient) GetBytes(key string) ([]byte, error) {
	return c.client.Get(c.ctx, key).Bytes()
}

// MGet gets multiple values from redis
func (c *CacheClient) MGet(keys []string) ([]any, error) {
	return c.client.MGet(c.ctx, keys...).Result()
}

// Exists checks if a key exists in redis
func (c *CacheClient) Exists(key string) bool {
	return c.client.Exists(c.ctx, key).Val() > 0
}

// ZAdd adds members to a sorted set stored at key, creating the sorted set if it doesn't exist
func (c *CacheClient) ZAdd(key string, members []redis.Z) error {
	return c.client.ZAdd(c.ctx, key, members...).Err()
}

// ZRange returns the elements of the sorted set stored at key with a score between min and max (inclusive)
func (c *CacheClient) ZRange(key string, start, stop int64) ([]string, error) {
	return c.client.ZRange(c.ctx, key, start, stop).Result()
}

// ZRevRange returns the elements of the sorted set stored at key in reverse order with a score between start and stop (inclusive)
func (c *CacheClient) ZRevRange(key string, start, stop int64) ([]string, error) {
	return c.client.ZRangeArgs(c.ctx, redis.ZRangeArgs{
		Key:   key,
		Start: start,
		Stop:  stop,
		Rev:   true,
	}).Result()
	// old implementation:
	//return c.client.ZRevRange(c.ctx, key, start, stop).Result()
}

// Load retrieves a value from the cache for the given key and stores it in the value interface
func (c *CacheClient) Load(key string, value any) error {
	return c.cache.Get(c.ctx, key, value)
}

// Store saves a value in the cache with the given key and a 7 day TTL
func (c *CacheClient) Store(key string, value any) error {
	return c.cache.Set(&cache.Item{
		Ctx:   c.ctx,
		Key:   key,
		Value: value,
		TTL:   storageDuration,
	})
}

// Save saves a value in the cache with the given key and a 2 year TTL
func (c *CacheClient) Save(key string, value any) error {
	return c.cache.Set(&cache.Item{
		Ctx:   c.ctx,
		Key:   key,
		Value: value,
		TTL:   twoYearDuration,
	})
}

// ValueExists checks if a value exists in the cache for the given key
func (c *CacheClient) ValueExists(key string) bool {
	return c.cache.Exists(c.ctx, key)
}

// Delete deletes a value from the cache for the given key
func (c *CacheClient) Delete(key string) error {
	return c.cache.Delete(c.ctx, key)
}

// Stats returns the cache statistics
func (c *CacheClient) Stats() *cache.Stats {
	return c.cache.Stats()
}

// PoolStats returns the redis connection pool statistics
func (c *CacheClient) PoolStats() *redis.PoolStats {
	return c.client.PoolStats()
}
