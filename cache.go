package rss2masto

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type CacheClient struct {
	client *redis.Client
	ctx    context.Context
}

var (
	ccOnce *CacheClient
	once   sync.Once
)

// Cache returns a singleton instance of CacheClient
func Cache() *CacheClient {
	once.Do(func() {
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
				log.Println("REDIS_HOST not set")
				return
			}
			opt, err = redis.ParseURL("redis://" + host)
			if err != nil {
				log.Println(err)
				return
			}
		}
		opt.ClientName = "rss2masto"
		client := redis.NewClient(opt)

		_, err = client.Ping(context.Background()).Result()
		if err != nil {
			log.Println(err)
			return
		}

		ccOnce = &CacheClient{
			client: client,
			ctx:    context.Background(),
		}
	})
	return ccOnce
}

// Set sets a key-value pair in redis
func (c *CacheClient) Set(key string, value interface{}, expiration time.Duration) error {
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
func (c *CacheClient) MGet(keys []string) ([]interface{}, error) {
	return c.client.MGet(c.ctx, keys...).Result()
}

// Exists checks if a key exists in redis
func (c *CacheClient) Exists(key string) bool {
	return c.client.Exists(c.ctx, key).Val() > 0
}

// Clear clears all keys in redis
func (c *CacheClient) Clear() error {
	return c.client.FlushDB(c.ctx).Err()
}

func (c *CacheClient) ZAdd(key string, members []redis.Z) error {
	return c.client.ZAdd(c.ctx, key, members...).Err()
}

func (c *CacheClient) ZRevRange(key string, start, stop int64) ([]string, error) {
	return c.client.ZRevRange(c.ctx, key, start, stop).Result()
}
