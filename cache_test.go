package rss2masto

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClientInterface defines the interface for Redis operations
type RedisClientInterface interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	GetEx(ctx context.Context, key string, expiration time.Duration) *redis.StringCmd
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	FlushDB(ctx context.Context) *redis.StatusCmd
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	Ping(ctx context.Context) *redis.StatusCmd
}

// MockRedisClient implements a mock Redis client for testing
type MockRedisClient struct {
	data         map[string]interface{}
	sortedSets   map[string][]redis.Z
	pingError    error
	setError     error
	getError     error
	existsResult int64
}

func NewMockRedisClient() *MockRedisClient {
	return &MockRedisClient{
		data:       make(map[string]interface{}),
		sortedSets: make(map[string][]redis.Z),
	}
}

func (m *MockRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	if m.setError != nil {
		cmd.SetErr(m.setError)
	} else {
		m.data[key] = value
		cmd.SetVal("OK")
	}
	return cmd
}

func (m *MockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	if m.getError != nil {
		cmd.SetErr(m.getError)
	} else if val, exists := m.data[key]; exists {
		cmd.SetVal(val.(string))
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (m *MockRedisClient) GetEx(ctx context.Context, key string, expiration time.Duration) *redis.StringCmd {
	return m.Get(ctx, key)
}

func (m *MockRedisClient) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	cmd := redis.NewSliceCmd(ctx)
	var results []interface{}
	for _, key := range keys {
		if val, exists := m.data[key]; exists {
			results = append(results, val)
		} else {
			results = append(results, nil)
		}
	}
	cmd.SetVal(results)
	return cmd
}

func (m *MockRedisClient) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(m.existsResult)
	return cmd
}

func (m *MockRedisClient) FlushDB(ctx context.Context) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	m.data = make(map[string]interface{})
	m.sortedSets = make(map[string][]redis.Z)
	cmd.SetVal("OK")
	return cmd
}

func (m *MockRedisClient) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	cmd := redis.NewScanCmd(ctx, nil)
	var keys []string
	for key := range m.data {
		keys = append(keys, key)
	}
	cmd.SetVal(keys, 0)
	return cmd
}

func (m *MockRedisClient) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	m.sortedSets[key] = append(m.sortedSets[key], members...)
	cmd.SetVal(int64(len(members)))
	return cmd
}

func (m *MockRedisClient) ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	cmd := redis.NewStringSliceCmd(ctx)
	if members, exists := m.sortedSets[key]; exists {
		var result []string
		for _, member := range members {
			result = append(result, member.Member.(string))
		}
		cmd.SetVal(result)
	} else {
		cmd.SetVal([]string{})
	}
	return cmd
}

func (m *MockRedisClient) Ping(ctx context.Context) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	if m.pingError != nil {
		cmd.SetErr(m.pingError)
	} else {
		cmd.SetVal("PONG")
	}
	return cmd
}

// TestCacheClient wraps CacheClient for testing with mock
type TestCacheClient struct {
	client RedisClientInterface
	ctx    context.Context
}

func (c *TestCacheClient) Set(key string, value interface{}, expiration time.Duration) error {
	return c.client.Set(c.ctx, key, value, expiration).Err()
}

func (c *TestCacheClient) Get(key string) (string, error) {
	return c.client.Get(c.ctx, key).Result()
}

func (c *TestCacheClient) GetEx(key string, expiration time.Duration) (string, error) {
	return c.client.GetEx(c.ctx, key, expiration).Result()
}

func (c *TestCacheClient) GetBytes(key string) ([]byte, error) {
	return c.client.Get(c.ctx, key).Bytes()
}

func (c *TestCacheClient) MGet(keys []string) ([]interface{}, error) {
	return c.client.MGet(c.ctx, keys...).Result()
}

func (c *TestCacheClient) Exists(key string) bool {
	return c.client.Exists(c.ctx, key).Val() > 0
}

func (c *TestCacheClient) Clear() error {
	return c.client.FlushDB(c.ctx).Err()
}

func (c *TestCacheClient) GetKeys(keyPattern string, count ...int64) ([]string, error) {
	limit := int64(-1)
	if len(count) > 0 {
		limit = count[0]
	}
	keys, _, err := c.client.Scan(c.ctx, 0, keyPattern, limit).Result()
	return keys, err
}

func (c *TestCacheClient) ZAdd(key string, members []redis.Z) error {
	return c.client.ZAdd(c.ctx, key, members...).Err()
}

func (c *TestCacheClient) ZRevRange(key string, start, stop int64) ([]string, error) {
	return c.client.ZRevRange(c.ctx, key, start, stop).Result()
}

func TestCacheClient_Set(t *testing.T) {
	mockClient := NewMockRedisClient()
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	err := cache.Set("test_key", "test_value", time.Hour)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if mockClient.data["test_key"] != "test_value" {
		t.Errorf("Expected 'test_value', got %v", mockClient.data["test_key"])
	}
}

func TestCacheClient_Set_Error(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.setError = errors.New("set error")
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	err := cache.Set("test_key", "test_value", time.Hour)
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestCacheClient_Get(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["test_key"] = "test_value"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	value, err := cache.Get("test_key")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if value != "test_value" {
		t.Errorf("Expected 'test_value', got %v", value)
	}
}

func TestCacheClient_Get_NotFound(t *testing.T) {
	mockClient := NewMockRedisClient()
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	_, err := cache.Get("nonexistent_key")
	if err == nil {
		t.Error("Expected error for nonexistent key, got nil")
	}
}

func TestCacheClient_GetEx(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["test_key"] = "test_value"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	value, err := cache.GetEx("test_key", time.Hour)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if value != "test_value" {
		t.Errorf("Expected 'test_value', got %v", value)
	}
}

func TestCacheClient_GetBytes(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["test_key"] = "test_value"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	bytes, err := cache.GetBytes("test_key")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if string(bytes) != "test_value" {
		t.Errorf("Expected 'test_value', got %v", string(bytes))
	}
}

func TestCacheClient_MGet(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["key1"] = "value1"
	mockClient.data["key2"] = "value2"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	values, err := cache.MGet([]string{"key1", "key2", "key3"})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(values) != 3 {
		t.Errorf("Expected 3 values, got %d", len(values))
	}
	if values[0] != "value1" || values[1] != "value2" || values[2] != nil {
		t.Errorf("Unexpected values: %v", values)
	}
}

func TestCacheClient_Exists_True(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.existsResult = 1
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	exists := cache.Exists("test_key")
	if !exists {
		t.Error("Expected key to exist")
	}
}

func TestCacheClient_Exists_False(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.existsResult = 0
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	exists := cache.Exists("test_key")
	if exists {
		t.Error("Expected key to not exist")
	}
}

func TestCacheClient_Clear(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["test_key"] = "test_value"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	err := cache.Clear()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(mockClient.data) != 0 {
		t.Error("Expected data to be cleared")
	}
}

func TestCacheClient_GetKeys(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["key1"] = "value1"
	mockClient.data["key2"] = "value2"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	keys, err := cache.GetKeys("*")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}
}

func TestCacheClient_GetKeys_WithCount(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.data["key1"] = "value1"
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	keys, err := cache.GetKeys("*", 10)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("Expected 1 key, got %d", len(keys))
	}
}

func TestCacheClient_ZAdd(t *testing.T) {
	mockClient := NewMockRedisClient()
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	members := []redis.Z{
		{Score: 1.0, Member: "member1"},
		{Score: 2.0, Member: "member2"},
	}

	err := cache.ZAdd("test_set", members)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(mockClient.sortedSets["test_set"]) != 2 {
		t.Errorf("Expected 2 members, got %d", len(mockClient.sortedSets["test_set"]))
	}
}

func TestCacheClient_ZRevRange(t *testing.T) {
	mockClient := NewMockRedisClient()
	mockClient.sortedSets["test_set"] = []redis.Z{
		{Score: 1.0, Member: "member1"},
		{Score: 2.0, Member: "member2"},
	}
	cache := &TestCacheClient{
		client: mockClient,
		ctx:    context.Background(),
	}

	members, err := cache.ZRevRange("test_set", 0, -1)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if len(members) != 2 {
		t.Errorf("Expected 2 members, got %d", len(members))
	}
}

func TestCache_Singleton(t *testing.T) {
	// Reset singleton for testing
	once = sync.Once{}
	ccOnce = nil

	// Mock environment for non-debug mode
	originalDebugMode := debugMode
	debugMode = false
	t.Setenv("REDIS_HOST", "localhost:6379")

	cache1 := Cache()
	cache2 := Cache()

	if cache1 != cache2 {
		t.Error("Cache() should return the same instance (singleton)")
	}

	// Restore original debug mode
	debugMode = originalDebugMode
}
