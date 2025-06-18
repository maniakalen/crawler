package queue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisQueue struct {
	ID   int
	Host string
	lock sync.Mutex
	rdb  *redis.Client
	ctx  context.Context
}

type RedisConnConfig struct {
	Port     int
	DB       int
	Address  string
	Password string
	Username string
}

func NewRedisConnection(config RedisConnConfig) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", config.Address, config.Port),
		Username: config.Username,
		Password: config.Password, // no password set
		DB:       config.DB,       // use default DB
	})
	return rdb
}
func NewRedisQueue(ID int, host string, rdb *redis.Client) *RedisQueue {

	queue := &RedisQueue{
		ID:   ID,
		Host: host,
		rdb:  rdb,
		ctx:  context.Background(),
	}
	res, _ := rdb.Ping(queue.ctx).Result()
	if res != "PONG" {
		os.Exit(1)
	}
	return queue
}

func (c *RedisQueue) getRedisKey(prefix string) string {
	return fmt.Sprintf("%s:%d:%s", prefix, c.ID, c.Host)
}

func (c *RedisQueue) Size() int {
	key := c.getRedisKey("queue")
	c.lock.Lock()
	defer c.lock.Unlock()
	res, err := c.rdb.ZCard(c.ctx, key).Result()
	if err != nil {
		slog.Error("Unable to get list length")
	}
	return int(res)
}

func (c *RedisQueue) Reset() bool {
	key := c.getRedisKey("queue")
	c.lock.Lock()
	defer c.lock.Unlock()
	c.rdb.Del(c.ctx, key).Result()
	return true
}

func (c *RedisQueue) Add(item CrawlItem) bool {
	key := c.getRedisKey("queue")
	c.lock.Lock()
	defer c.lock.Unlock()
	added, err := c.rdb.ZAdd(c.ctx, key, redis.Z{Member: item.URL, Score: 1}).Result()
	if err == nil && added > 0 {
		depthsKey := c.getRedisKey("depths")
		c.rdb.HSet(c.ctx, depthsKey, item.URL, strconv.Itoa(item.Depth))
	}
	return true
}

func (c *RedisQueue) Fetch(count int64) <-chan []CrawlItem {
	channel := make(chan []CrawlItem)
	go func() {
		defer close(channel)
		key := c.getRedisKey("queue")
		c.lock.Lock()
		defer c.lock.Unlock()
		res, err := c.rdb.ZPopMin(c.ctx, key, count).Result()
		if err != nil {
			slog.Error(err.Error())
		}
		if len(res) == 0 {
			return
		}
		fmt.Println("Items length:", len(res))
		depthsKey := c.getRedisKey("depths")
		items := make([]CrawlItem, 0)
		for _, item := range res {
			mem := item.Member.(string)
			if err != nil {
				return
			}
			d, err := c.rdb.HGet(c.ctx, depthsKey, mem).Result()
			if err != nil {
				fmt.Println(err.Error())
				return
			}
			depth, err := strconv.Atoi(d)

			if err != nil {
				fmt.Println(err.Error())
				return
			}
			c.rdb.HDel(c.ctx, depthsKey, mem).Result()
			items = append(items, CrawlItem{URL: mem, Depth: depth})
		}
		channel <- items
	}()
	time.Sleep(1 * time.Second)
	return channel
}

func (c *RedisQueue) Close() {
	c.rdb.Close()
}
