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
		opt, err := redis.ParseURL("redis://" + os.Getenv("REDIS_HOST"))
		if err != nil {
			log.Println(err)
			return
		}
		client := redis.NewClient(opt)

		if __DEBUG__ {
			client = redis.NewClient(&redis.Options{
				Addr:     "localhost:6379",
				Password: "", // no password set
				DB:       0,  // use default DB
			})
		}
		ccOnce = &CacheClient{
			client: client,
			ctx:    context.Background(),
		}
		_, err = client.Ping(ccOnce.ctx).Result()
		if err != nil {
			log.Println(err)
			ccOnce = nil
		}
	})
	return ccOnce
}

func (c *CacheClient) Set(key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(c.ctx, key, value, expiration).Err()
}

func (c *CacheClient) Get(key string) (string, error) {
	return c.client.Get(c.ctx, key).Result()
}

func (c *CacheClient) GetKeys(keyPattern string) ([]string, error) {
	keys, _, err := c.client.Scan(c.ctx, 0, keyPattern, 0).Result()
	return keys, err
}

func (c *CacheClient) GetEx(key string, expiration time.Duration) (string, error) {
	return c.client.GetEx(c.ctx, key, expiration).Result()
}

func (c *CacheClient) GetBytes(key string) ([]byte, error) {
	return c.client.Get(c.ctx, key).Bytes()
}

func (c *CacheClient) Exists(key string) bool {
	return c.client.Exists(c.ctx, key).Val() > 0
}

func (c *CacheClient) Clear() error {
	return c.client.FlushDB(c.ctx).Err()
}
