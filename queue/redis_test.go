package queue

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestRedisFetchAndAdd(t *testing.T) {
	tests := []struct {
		name               string
		queueItemsToAdd    []CrawlItem
		queueItemsToFetch  int
		expectedItemsCount int
	}{
		{
			name:               "FetchLessThanAdded",
			queueItemsToAdd:    []CrawlItem{{URL: "https://testing.com/page1", Depth: 1}, {URL: "https://testing.com/page2", Depth: 1}, {URL: "https://testing.com/page3", Depth: 1}, {URL: "https://testing.com/page4", Depth: 1}, {URL: "https://testing.com/page5", Depth: 1}},
			queueItemsToFetch:  3,
			expectedItemsCount: 3,
		},

		{
			name:               "FetchMoreThanAdded",
			queueItemsToAdd:    []CrawlItem{{URL: "https://testing.com/page1", Depth: 1}, {URL: "https://testing.com/page2", Depth: 1}},
			queueItemsToFetch:  5,
			expectedItemsCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := 1
			host := "testing.com"
			key := fmt.Sprintf("queue:%d:%s", id, host)
			keyd := fmt.Sprintf("depths:%d:%s", id, host)
			options := make([]MockRedisOption, 0)
			options = append(options, WithMockRedisZCardAll(key), WithMockRedisZPopMin(key, tt.queueItemsToFetch))
			for _, item := range tt.queueItemsToAdd {
				options = append(options, WithMockRedisZAdd(key, item.URL))
				options = append(options, WithMockRedisHSet(keyd, item.URL, item.Depth))
				options = append(options, WithMockRedisHGet(keyd, item.URL))
			}
			que := NewRedisQueue(id, host, MockInstance(t, options...))
			for _, it := range tt.queueItemsToAdd {
				que.Add(it)
			}
			channel := que.Fetch(int64(tt.queueItemsToFetch))
			if channel == nil {
				t.Fatal("Null channel")
			}

			items := <-channel

			assert.Equal(t, tt.expectedItemsCount, len(items))

		})
	}
}

func TestRedisQueueSize(t *testing.T) {
	items := []CrawlItem{
		{URL: "https://testing.com/page1", Depth: 1},
		{URL: "https://testing.com/page2", Depth: 1},
		{URL: "https://testing.com/page3", Depth: 1},
		{URL: "https://testing.com/page4", Depth: 1},
		{URL: "https://testing.com/page5", Depth: 1},
	}
	id := 1
	host := "testing.com"
	key := fmt.Sprintf("queue:%d:%s", id, host)
	keyd := fmt.Sprintf("depths:%d:%s", id, host)
	options := make([]MockRedisOption, 0)
	options = append(options, WithMockRedisZCardAll(key))
	for _, item := range items {
		options = append(options, WithMockRedisZAdd(key, item.URL))
		options = append(options, WithMockRedisHSet(keyd, item.URL, item.Depth))
		options = append(options, WithMockRedisHGet(keyd, item.URL))
	}
	que := NewRedisQueue(id, host, MockInstance(t, options...))
	for _, item := range items {
		que.Add(item)
	}
	assert.Equal(t, int64(5), que.Size())
}

type MockRedisOption func(ctx context.Context, redis *redis.Client) error

func WithMockRedisSet(key, value string, expires time.Duration) MockRedisOption {
	return func(ctx context.Context, redis *redis.Client) error {
		return redis.Set(ctx, key, value, expires).Err()
	}
}

func WithMockRedisLPush(key string, values ...string) MockRedisOption {
	return func(ctx context.Context, redis *redis.Client) error {
		return redis.LPush(ctx, key, values).Err()
	}
}

func WithMockRedisHSet(key string, fields ...interface{}) MockRedisOption {
	return func(ctx context.Context, redis *redis.Client) error {
		return redis.HSet(ctx, key, fields).Err()
	}
}

func WithMockRedisHGet(key string, field string) MockRedisOption {
	return func(ctx context.Context, redis *redis.Client) error {
		return redis.HGet(ctx, key, field).Err()
	}
}

func WithMockRedisZCardAll(key string) MockRedisOption {
	return func(ctx context.Context, redis *redis.Client) error {
		return redis.ZCard(ctx, key).Err()
	}
}

func WithMockRedisZPopMin(key string, count int) MockRedisOption {
	return func(ctx context.Context, redis *redis.Client) error {
		return redis.ZPopMin(ctx, key, int64(count)).Err()
	}
}
func WithMockRedisZAdd(key string, item interface{}) MockRedisOption {
	return func(ctx context.Context, red *redis.Client) error {
		return red.ZAdd(ctx, key, redis.Z{Member: item, Score: 1}).Err()
	}
}

func MockInstance(t *testing.T, options ...MockRedisOption) *redis.Client {
	slog.Info("[nitroredis] starting fake redis")

	var client *redis.Client
	RedisCtx := context.Background()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "redis:6.2.14",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	if err != nil {
		t.Errorf("could not start redis: %s", err)
	}

	port, _ := redisC.MappedPort(ctx, "6379/tcp")
	host, _ := redisC.Host(ctx)
	slog.Debug("host", "host", host)
	addr := host + ":" + port.Port()

	client = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: "",
		DB:       0, // use default DB
	})

	t.Cleanup(func() {
		client.Close()
	})

	for _, option := range options {
		err := option(RedisCtx, client)

		if err != nil {
			t.Error(err)
		}
	}
	return client
}
