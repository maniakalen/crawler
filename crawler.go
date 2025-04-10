// Package crawler provides functionalities to crawl a given website's pages
package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	log "github.com/maniakalen/crawler/log"
	"github.com/redis/go-redis/v9"

	"golang.org/x/net/html"
)

// Crawler is the main package object used to initialize the crawl
type Crawler struct {
	root        *url.URL
	client      *http.Client
	channels    Channels
	scanParent  bool
	filters     []func(p Page) bool
	ctx         *context.Context
	cancel      *context.CancelFunc
	headers     map[string]string
	loadControl chan bool
	rdb         *redis.Client
	usrCfg      int
	queueLock   sync.Mutex
}

// Page is a struct that carries the scanned url, response and response body string
type Page struct {
	Url  url.URL       // Page url
	Resp http.Response // Page response as returned from the GET request
	Body string        // Response body string
}

// Channels is a Page channels map where the index is the response code so we can define different behavior for the different resp codes
type Channels map[int]chan Page

var n int = runtime.GOMAXPROCS(0) // Number of workers
var cr int = 0                    // Number of workers
func worker(i *int, c *Crawler) {
	cr++
	cr := cr
	log.Info("Worker ", cr, " started")
	defer log.Info("Worker ", cr, " stopped")
	defer func() {
		*i--
	}()
	empty := 0
	for {
		select {
		case <-(*c.ctx).Done():
			log.Debug("Worker ", cr, " is closing due to context")
			return
		default:
		}
		ustring := c.fetchFromQueue()
		if len(ustring) == 0 {
			if empty >= 10 {
				return
			}
			empty++
			time.Sleep(10 * time.Second)
			continue
		}
		url, err := url.Parse(ustring)
		if err != nil {
			log.Error("Unable to parse url")
		}
		if url.Host == c.root.Host {
			log.Debug("Worker ", cr, " is scanning url: ", url.String())
			err := c.scanUrl(url)
			if err != nil {
				log.Error("Error scanning url: " + err.Error())
			}
			log.Debug("Worker ", cr, " finished scanning url: ", url.String())
		}
		time.Sleep(500 * time.Microsecond)
	}
}

// New is the crawler inicialization method
func New(parentCtx context.Context, urlString string, chans Channels, parents bool, filters []func(p Page) bool, headers map[string]string, cscans int, cfg int) (*Crawler, error) {
	urlObject, err := url.Parse(urlString)
	if err != nil {
		log.Error("unable to parse root url: " + err.Error())
		return nil, fmt.Errorf("unable to parse root url")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	if urlObject.Path == "" {
		urlObject.Path = "/"
	}
	ctx, cancel := context.WithCancel(parentCtx)
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	res, err := rdb.Ping(ctx).Result()
	if res != "PONG" {
		os.Exit(1)
	}
	crawler := &Crawler{
		root:        urlObject,
		channels:    chans,
		client:      client,
		scanParent:  parents,
		filters:     filters,
		ctx:         &ctx,
		cancel:      &cancel,
		headers:     headers,
		loadControl: make(chan bool, cscans),
		rdb:         rdb,
		usrCfg:      cfg,
	}
	return crawler, nil
}

func (c *Crawler) StoreScannedItem(item string) {
	key := c.getRedisKey()
	c.rdb.RPush(*c.ctx, key, item)
}

func (c *Crawler) ScannedItemsCount() int64 {
	key := c.getRedisKey()
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
	c.addToQueue((*c.root).String())
	time.Sleep(1 * time.Second)
	log.Info(fmt.Sprintf("Queue size: %d\n", c.getQueueSize()))
	log.Info(fmt.Sprintf("Scanned items: %d\n", c.ScannedItemsCount()))
	go func() {
		i := 0
		reps := 0
		for {
			select {
			case <-(*c.ctx).Done():
				log.Debug("Crawler is closing")
				return
			default:
				if c.getQueueSize() > 0 && i < n {
					i++
					go worker(&i, c)
				} else if c.getQueueSize() == 0 && i == 0 {
					log.Debug("Crawler is done. closing")
					time.Sleep(5 * time.Second)
					reps++
					if reps > 5 {
						c.Close()
					}
				} else {
					log.Debug("Crawler is waiting for workers to finish")
					time.Sleep(5 * time.Second)
				}
			}
		}
	}()
}

func (c *Crawler) scanUrl(u *url.URL) error {
	if u.String() != "" && !strings.Contains(u.String(), "javascript:") {
		c.loadControl <- true
		defer func() {
			<-c.loadControl
		}()
		c.StoreScannedItem(u.String())
		log.Debug("Requesting url: ", u.String())
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			log.Error("unable to create request: " + err.Error())
			return fmt.Errorf("unable to create request %+v", err)
		}
		for key, value := range c.headers {
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
		if channel, exists := c.channels[resp.StatusCode]; exists {
			page := Page{Url: *u, Resp: *resp, Body: string(body)}
			send := true
			for _, filter := range c.filters {
				log.Debug("Applying filter to page: ", u.String(), send)
				send = send && filter(page)
			}
			if send {
				log.Debug("Sending page to channel: ", u.String())
				channel <- page
			}
		}
		if len(resp.Header["Content-Type"]) > 0 && strings.HasPrefix(resp.Header["Content-Type"][0], "text/html") {
			doc, err := html.Parse(bodyReader)
			if err != nil {
				log.Error("unable to parse page body: " + err.Error())
				return fmt.Errorf("unable to parse page body")
			}
			log.Debug("Scanning nodes: ", u.String())
			c.scanNode(doc)
		}

	}
	return nil
}

func (c *Crawler) scanNode(n *html.Node) {
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
						c.addToQueue(u.String())
					}
				} else {
					log.Error("unable to parse url: " + err.Error())
				}
				break
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.scanNode(child)
	}
}

func (c *Crawler) repairUrl(u *url.URL) (url.URL, error) {
	if u.Scheme == "javascript" || (u.Host == "" && u.Path == "") {
		return *u, fmt.Errorf("href is not an url")
	}
	if u.Host != "" && u.Host != c.root.Host {
		return *u, fmt.Errorf("host outside of this site: %v", c.root.Host)
	}
	if u.Scheme == "" {
		u.Scheme = c.root.Scheme
	}
	if u.Path != "" && u.Host == "" {
		u.Host = c.root.Host
	}
	if u.Scheme == "" {
		return *u, fmt.Errorf("url incorrect")
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if !c.scanParent && !strings.HasPrefix(u.String(), c.root.String()) {
		return *u, fmt.Errorf("path is parent")
	}
	return *u, nil

}

func (c *Crawler) addToQueue(item string) {
	key := c.getQueueRedisKey()
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	c.rdb.ZAdd(*c.ctx, key, redis.Z{Member: item, Score: 1}).Result()
}

func (c *Crawler) fetchFromQueue() string {
	key := c.getQueueRedisKey()
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	res, err := c.rdb.ZPopMin(*c.ctx, key).Result()
	if err != nil {
		log.Error(err)
	}
	if len(res) == 0 {
		return ""
	}
	return res[0].Member.(string)
}

func (c *Crawler) containsString(item string) bool {
	key := c.getRedisKey()
	_, err := c.rdb.LPos(*c.ctx, key, item, redis.LPosArgs{}).Result()
	return err == nil
}

func (c *Crawler) getRedisKey() string {
	return fmt.Sprintf("checked:%d:%s", c.usrCfg, c.root.Hostname())
}

func (c *Crawler) getQueueRedisKey() string {
	return fmt.Sprintf("queue:%d:%s", c.usrCfg, c.root.Hostname())
}

func (c *Crawler) getQueueSize() int64 {
	key := c.getQueueRedisKey()
	c.queueLock.Lock()
	defer c.queueLock.Unlock()
	res, err := c.rdb.ZCard(*c.ctx, key).Result()
	if err != nil {
		log.Error("Unable to get list length")
	}
	return res
}
