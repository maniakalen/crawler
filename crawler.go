// Package crawler provides functionalities to crawl a given website's pages
package crawler

import (
	"context"
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
	delayLock   sync.Mutex
	config      *Config
	loadControl chan bool
	delay       time.Duration
	delayChan   chan bool
	delayed     bool
}

// Config is a struct used to configure the crawler
// Filters is a list of methods used to filter the pages BEFORE being processed or sent back to the initiator
type Config struct {
	Root          *url.URL
	RedisAddress  string
	RedisPort     int
	RedisPass     string
	RedisDb       int
	Headers       map[string]string
	ScanParents   bool
	Filters       []func(p Page, config *Config) bool
	Channels      Channels
	Id            int
	Throttle      int
	MaxWorkers    int
	MaxQueueEntry int
	Delay         float64
	DelayListener chan float64
	LanguageCode  string
}

// Page is a struct that carries the scanned url, response and response body string
type Page struct {
	Url  url.URL       // Page url
	Resp http.Response // Page response as returned from the GET request
	Body string        // Response body string
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
			ustring, depth := c.fetchFromQueue()
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
					err := c.scanUrl(url, depth)
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
		delay:     time.Duration(config.Delay) * time.Second,
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
	c.ClearQueue()
	c.ResetWorkersCount()
	(*c.cancel)()
	c.rdb.Close()
}

// Run is the crawler start method
func (c *Crawler) Run() {
	allowed, delay := robots.IsURLAllowed((*c.config.Root).String())
	if c.getQueueSize() == 0 {
		fmt.Println("Parsing sitemap for: ", (*c.config.Root).String())
		c.processSitemap()
		if allowed {
			fmt.Println("Adding root url: ", (*c.config.Root).String())
			c.addToQueue((*c.config.Root).String(), c.config.MaxQueueEntry)
			time.Sleep(1 * time.Second)
		}
		c.ResetWorkersCount()
	}
	if delay.Microseconds() > c.delay.Microseconds() {
		c.delay = delay
		go func() {
			if c.config.DelayListener != nil {
				select {
				case c.config.DelayListener <- delay.Seconds():
				case <-(*c.ctx).Done():
				}
			}
		}()
	}
	go c.ProduceDelay()

	log.Info(fmt.Sprintf("Queue size: %d\n", c.getQueueSize()))
	log.Info(fmt.Sprintf("Scanned items: %d\n", c.ScannedItemsCount()))
	log.Info(fmt.Sprintf("%+v\n", c))
	log.Info(fmt.Sprintf("%+v\n", *c.config))
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

func (c *Crawler) processSitemap() {
	sitemaps, err := robots.GetSitemaps((*c.config.Root).String())
	if err != nil {
		log.Error("Failed to fetch sitemaps list")
	} else {
		urls := make([]robots.URL, 0)
		for _, sitemap := range sitemaps {
			list := robots.ExtractURLs(sitemap)
			urls = append(urls, list...)
		}
		for _, url := range urls {
			if allowed, _ := robots.IsURLAllowed(url.Loc); allowed {
				c.addToQueue(url.Loc, 1)
			}
		}
	}
}

var bmux sync.Mutex

func (c *Crawler) scanUrl(u *url.URL, level int) error {
	if u.String() != "" && !strings.Contains(u.String(), "javascript:") {
		if c.config.Throttle > 0 {
			c.loadControl <- true
			defer func() {
				<-c.loadControl
			}()
		}
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
		if resp.StatusCode == 429 {
			c.delayLock.Lock()
			if c.delay.Nanoseconds() == 0 {
				fmt.Println("Producing delay")
				go c.ProduceDelay()
			}
			c.delay = time.Duration(c.delay.Seconds()+1) * time.Second
			<-time.After(5 * time.Second)
			c.delayLock.Unlock()
		}
		if resp.StatusCode < 300 {
			c.StoreScannedItem(u.String())
		}
		log.Debug("Received response: ", resp.Status, u.String())
		bmux.Lock()
		c.rdb.IncrBy(*c.ctx, "bandwidthused", resp.ContentLength)
		bmux.Unlock()
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
		page := Page{Url: *u, Resp: *resp, Body: string(body)}
		process := true
		for _, filter := range c.config.Filters {
			log.Debug("Applying filter to page: ", u.String(), process)
			process = process && filter(page, c.config)
		}
		if process {
			if channel, exists := c.config.Channels[resp.StatusCode]; exists {
				log.Debug("Sending page to channel: ", u.String())
				channel <- page
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

	}
	return nil
}

func (c *Crawler) scanNode(n *html.Node, level int) {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" {
				if u, err := url.Parse(strings.Trim(a.Val, " ")); err == nil {
					u, err := c.repairUrl(u)
					if allowed, _ := robots.IsURLAllowed(u.String()); allowed && err == nil && u.String() != "" && !strings.Contains(u.String(), "javascript:") && !c.containsString(u.String()) {
						log.Debug("Adding url to queue channel: ", u.String())
						c.addToQueue(u.String(), level-1)
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

func (c *Crawler) addToQueue(item string, depth int) {
	key := c.getRedisKey("queue")
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	log.Debug("Adding to queue ", item)
	added, err := c.rdb.ZAdd(*c.ctx, key, redis.Z{Member: item, Score: 1}).Result()
	if err == nil && added > 0 {
		depthsKey := c.getRedisKey("depths")
		c.rdb.HSet(*c.ctx, depthsKey, item, strconv.Itoa(depth))
	}
}

func (c *Crawler) fetchFromQueue() (string, int) {
	key := c.getRedisKey("queue")
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	res, err := c.rdb.ZPopMin(*c.ctx, key).Result()
	if err != nil {
		log.Error(err)
	}
	if len(res) == 0 {
		return "", 0
	}
	mem := res[0].Member.(string)
	depthsKey := c.getRedisKey("depths")
	d, err := c.rdb.HGet(*c.ctx, depthsKey, mem).Result()
	if err != nil {
		return "", 0
	}
	depth, err := strconv.Atoi(d)
	if err != nil {
		return "", 0
	}
	c.rdb.HDel(*c.ctx, depthsKey, mem).Result()
	return mem, depth
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

func (c *Crawler) ClearQueue() {
	key := c.getRedisKey("queue")
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	c.rdb.Del(*c.ctx, key).Result()
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
	if c.delayed && c.delay.Nanoseconds() > 0 {
		<-c.delayChan
	}
}

func (c *Crawler) ProduceDelay() {
	if !c.delayed && c.delay.Nanoseconds() > 0 {
		c.delayed = true
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
