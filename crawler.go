// Package crawler provides functionalities to crawl a given website's pages
package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/maniakalen/crawler/log"
	"github.com/redis/go-redis/v9"

	"github.com/maniakalen/crawler/robots"
	"golang.org/x/net/html"
)

// Crawler is the main package object used to initialize the crawl
type Crawler struct {
	client      *http.Client
	ctx         *context.Context
	cancel      *context.CancelFunc
	rdb         *redis.Client
	queueLock   sync.Mutex
	countLock   sync.Mutex
	config      *Config
	loadControl chan bool
	delay       time.Duration
	delayChan   chan bool
}

type Config struct {
	Root          *url.URL
	RedisAddress  string
	RedisPort     int
	RedisPass     string
	RedisDb       int
	Headers       map[string]string
	ScanParents   bool
	Filters       []func(p Page) bool
	Channels      Channels
	Id            int
	Throttle      int
	MaxWorkers    int
	MaxQueueEntry int
}

// Page is a struct that carries the scanned url, response and response body string
type Page struct {
	Url  url.URL       // Page url
	Resp http.Response // Page response as returned from the GET request
	Body string        // Response body string
}

type QueueEntry struct {
	Url   string
	Level int
}

func (q QueueEntry) serialize() string {
	res, err := json.Marshal(q)
	if err != nil {
		log.Fatal("failed to marshal queue entry")
	}
	return string(res)
}

func (q *QueueEntry) unserialize(entry string) {
	err := json.Unmarshal([]byte(entry), q)
	if err != nil {
		log.Fatal("Failed to unmarshal queue entry")
	}
}

// Channels is a Page channels map where the index is the response code so we can define different behavior for the different resp codes
type Channels map[int]chan Page

// Number of workers
var cr int = 0 // Number of workers
func worker(c *Crawler) {
	cr++
	cr := cr
	log.Info("Worker ", cr, " started")
	c.IncrWorkersCount()
	defer log.Info("Worker ", cr, " stopped")
	defer func() {
		c.DecrWorkersCount()
	}()
	empty := 0
	for {
		select {
		case <-(*c.ctx).Done():
			log.Debug("Worker ", cr, " is closing due to context")
			return
		default:
			c.WaitDelay()
			entryString := c.fetchFromQueue()
			entry := QueueEntry{}
			entry.unserialize(entryString)
			ustring := entry.Url
			if len(ustring) == 0 {
				if empty >= 10 {
					return
				}
				empty++
				time.Sleep(2 * time.Second)
				c.ProduceDelay()
				continue
			}

			url, err := url.Parse(ustring)
			if err != nil {
				log.Error("Unable to parse url")
			}
			if url.Host == c.config.Root.Host {
				log.Debug("Worker ", cr, " is scanning url: ", url.String())
				go func() {
					err := c.scanUrl(url, entry.Level)
					if err != nil {
						log.Error("Error scanning url: " + err.Error())
					}
				}()
				log.Debug("Worker ", cr, " finished scanning url: ", url.String())
			}
		}
	}
}

// New is the crawler inicialization method
func New(parentCtx context.Context, config *Config) (*Crawler, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	ctx, cancel := context.WithCancel(parentCtx)
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", config.RedisAddress, config.RedisPort),
		Password: config.RedisPass, // no password set
		DB:       config.RedisDb,   // use default DB
	})
	res, _ := rdb.Ping(ctx).Result()
	if res != "PONG" {
		os.Exit(1)
	}
	crawler := &Crawler{
		client:    client,
		ctx:       &ctx,
		cancel:    &cancel,
		rdb:       rdb,
		config:    config,
		delayChan: make(chan bool),
	}
	if config.Throttle > 0 {
		crawler.loadControl = make(chan bool, config.Throttle)
	}
	return crawler, nil
}

func (c *Crawler) StoreScannedItem(item string) {
	key := c.getRedisKey("checked")
	c.rdb.RPush(*c.ctx, key, item)
}

func (c *Crawler) ScannedItemsCount() int64 {
	key := c.getRedisKey("checked")
	res, err := c.rdb.LLen(*c.ctx, key).Result()
	if err != nil {
		log.Error("Unable to get list length")
	}
	return res
}

func (c *Crawler) Done() <-chan struct{} {
	return (*c.ctx).Done()
}

func (c *Crawler) Close() {
	log.Info("Closing")
	(*c.cancel)()
	c.rdb.Close()
}

// Run is the crawler start method
func (c *Crawler) Run() {
	allowed, delay := robots.IsURLAllowed((*c.config.Root).String())
	if !allowed {
		return
	}
	c.delay = delay
	go c.ProduceDelay()
	entry := QueueEntry{
		Url:   (*c.config.Root).String(),
		Level: c.config.MaxQueueEntry,
	}
	c.addToQueue(entry.serialize())
	time.Sleep(1 * time.Second)
	log.Info(fmt.Sprintf("Queue size: %d\n", c.getQueueSize()))
	log.Info(fmt.Sprintf("Scanned items: %d\n", c.ScannedItemsCount()))
	c.ResetWorkersCount()
	go func() {
		reps := 0
		for {
			select {
			case <-(*c.ctx).Done():
				log.Debug("Crawler is closing")
				return
			default:
				count := c.GetActiveWorkersCount()
				if c.getQueueSize() > 0 && (c.config.MaxWorkers == 0 || count < c.config.MaxWorkers) {
					go worker(c)
				} else if c.getQueueSize() == 0 && count == 0 {
					log.Info("Crawler is done. closing\n")
					if delay.Seconds() > 0 {
						time.Sleep(delay)
					} else {
						time.Sleep(1 * time.Second)
					}
					reps++
					if reps > 15 {
						c.Close()
					}
				} else {
					//fmt.Printf("Crawler %s is waiting for workers to finish %d\n", c.config.Root.String(), c.getQueueSize())
					time.Sleep(1 * time.Second)
				}
			}
		}
	}()
}

func (c *Crawler) scanUrl(u *url.URL, level int) error {
	if u.String() != "" && !strings.Contains(u.String(), "javascript:") {
		if c.config.Throttle > 0 {
			c.loadControl <- true
			defer func() {
				<-c.loadControl
			}()
		}
		c.StoreScannedItem(u.String())
		log.Debug("Requesting url: ", u.String())
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			log.Error("unable to create request: " + err.Error())
			return fmt.Errorf("unable to create request %+v", err)
		}
		for key, value := range c.config.Headers {
			req.Header.Set(key, value)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("unable to fetch url %+v", err)
		}
		log.Debug("Received response: ", resp.Status, u.String())
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Error("unable to read page body: " + err.Error())
		}
		resp.Body.Close()
		log.Debug("Processing body")
		bodyReader := strings.NewReader(string(body))
		if err != nil {
			body = []byte{}
		}
		if channel, exists := c.config.Channels[resp.StatusCode]; exists {
			page := Page{Url: *u, Resp: *resp, Body: string(body)}
			send := true
			for _, filter := range c.config.Filters {
				log.Debug("Applying filter to page: ", u.String(), send)
				send = send && filter(page)
			}
			if send {
				log.Debug("Sending page to channel: ", u.String())
				channel <- page
			}
		}
		if level > 0 && len(resp.Header["Content-Type"]) > 0 && strings.HasPrefix(resp.Header["Content-Type"][0], "text/html") {
			dur := duration()
			doc, err := html.Parse(bodyReader)
			if err != nil {
				log.Error("unable to parse page body: " + err.Error())
				return fmt.Errorf("unable to parse page body")
			}
			log.Debug("Scanning nodes: ", u.String())
			c.scanNode(doc, level)
			log.Debug(fmt.Sprintf("Node scan for %s done in %v\n", u.String(), dur()))
		}

	}
	return nil
}

func (c *Crawler) scanNode(n *html.Node, level int) {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" {
				log.Debug("Found href: ", a.Val)
				if u, err := url.Parse(strings.Trim(a.Val, " ")); err == nil {
					log.Debug("Parsed url: ", u.String())
					u, err := c.repairUrl(u)
					log.Debug("Repaired url: ", u.String())
					log.Debug(fmt.Sprintf("Error: %+v", err))
					log.Debug(fmt.Sprintf("Contains: %+v", strings.Contains(u.String(), "javascript:")))
					log.Debug(fmt.Sprintf("Contains url: %+v", c.containsString(u.String())))
					if err == nil && u.String() != "" && !strings.Contains(u.String(), "javascript:") && !c.containsString(u.String()) {
						log.Debug("Adding url to queue channel: ", u.String())
						entry := QueueEntry{
							Url:   u.String(),
							Level: level - 1,
						}
						c.addToQueue(entry.serialize())
					}
				} else {
					log.Error("unable to parse url: " + err.Error())
				}
				break
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.scanNode(child, level)
	}
}

func (c *Crawler) repairUrl(u *url.URL) (url.URL, error) {
	if u.Scheme == "javascript" || (u.Host == "" && u.Path == "") {
		return *u, fmt.Errorf("href is not an url")
	}
	if u.Host != "" && u.Host != c.config.Root.Host {
		return *u, fmt.Errorf("host outside of this site: %v", c.config.Root.Host)
	}
	if u.Scheme == "" {
		u.Scheme = c.config.Root.Scheme
	}
	if u.Path != "" && u.Host == "" {
		u.Host = c.config.Root.Host
	}
	if u.Scheme == "" {
		return *u, fmt.Errorf("url incorrect")
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if !c.config.ScanParents && !strings.HasPrefix(u.String(), c.config.Root.String()) {
		return *u, fmt.Errorf("path is parent")
	}
	return *u, nil

}

func (c *Crawler) addToQueue(item string) {
	key := c.getRedisKey("queue")
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	log.Debug("Adding to queue ", item)
	c.rdb.ZAdd(*c.ctx, key, redis.Z{Member: item, Score: 1}).Result()
}

func (c *Crawler) fetchFromQueue() string {
	key := c.getRedisKey("queue")
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	res, err := c.rdb.ZPopMin(*c.ctx, key).Result()
	if err != nil {
		log.Error(err)
	}
	if len(res) == 0 {
		return ""
	}
	mem := res[0].Member.(string)
	log.Debug("Fetch from queue: ", mem)
	return mem
}

func (c *Crawler) containsString(item string) bool {
	key := c.getRedisKey("checked")
	_, err := c.rdb.LPos(*c.ctx, key, item, redis.LPosArgs{}).Result()
	return err == nil
}

func (c *Crawler) getRedisKey(prefix string) string {
	return fmt.Sprintf("%s:%d:%s", prefix, c.config.Id, c.config.Root.Hostname())
}

func (c *Crawler) getQueueSize() int64 {
	key := c.getRedisKey("queue")
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	res, err := c.rdb.ZCard(*c.ctx, key).Result()
	if err != nil {
		log.Error("Unable to get list length")
	}
	return res
}

func (c *Crawler) ResetWorkersCount() {
	c.countLock.Lock()
	defer c.countLock.Unlock()
	redisWorkerKey := c.getRedisKey("workers")
	c.rdb.Set(*c.ctx, redisWorkerKey, 0, 0)
}

func (c *Crawler) IncrWorkersCount() {
	c.countLock.Lock()
	defer c.countLock.Unlock()
	redisWorkerKey := c.getRedisKey("workers")
	c.rdb.Incr(*c.ctx, redisWorkerKey)
}
func (c *Crawler) DecrWorkersCount() {
	c.countLock.Lock()
	defer c.countLock.Unlock()
	redisWorkerKey := c.getRedisKey("workers")
	c.rdb.IncrBy(*c.ctx, redisWorkerKey, -1)
}

func (c *Crawler) GetActiveWorkersCount() int {
	c.countLock.Lock()
	defer c.countLock.Unlock()
	redisWorkerKey := c.getRedisKey("workers")
	res, err := c.rdb.Get(*c.ctx, redisWorkerKey).Result()
	if err != nil {
		log.Fatal("Failed to get workers count from redis")
	}
	count, err := strconv.Atoi(res)
	if err != nil {
		log.Fatal("Failed to get workers count")
	}
	return count
}

func (c *Crawler) WaitDelay() {
	if c.delay.Nanoseconds() > 0 {
		<-c.delayChan
	}
}

func (c *Crawler) ProduceDelay() {
	if c.delay.Nanoseconds() > 0 {
		for {
			select {
			case <-(*c.ctx).Done():
				return
			default:
				time.Sleep(c.delay)
				c.delayChan <- true
			}
		}
	}
}

func duration() func() time.Duration {
	start := time.Now()
	return func() time.Duration {
		return time.Since(start)
	}
}
