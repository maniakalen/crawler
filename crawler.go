// Package crawler provides functionalities to crawl a given website's pages
package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// Crawler is the main package object used to initialize the crawl
type Crawler struct {
	root         *url.URL
	client       *http.Client
	scannedItems map[string]bool
	urlsChan     chan url.URL
	channels     Channels
	scanParent   bool
	mu           sync.Mutex
}

// Page is a struct that carries the scanned url, response and response body string
type Page struct {
	Url  url.URL       // Page url
	Resp http.Response // Page response as returned from the GET request
	Body string        // Response body string
}

// Channels is a Page channels map where the index is the response code so we can define different behavior for the different resp codes
type Channels map[int]chan Page

var n int = runtime.GOMAXPROCS(0) / 2
var wg sync.WaitGroup

func worker(c *Crawler, ctx context.Context) {
	defer fmt.Println("Worker closing")
	defer wg.Done()
	for {
		select {
		case url := <-c.urlsChan:
			url, err := c.repairUrl(&url)
			if err == nil && url.Host == c.root.Host && !c.containsString(url.String()) {
				c.mu.Lock()
				c.scannedItems[url.String()] = true
				c.mu.Unlock()
				go c.scanUrl(&url)
			}

		case <-ctx.Done():
			return
		}
		time.Sleep(50 * time.Microsecond)
	}
}

// NewCrawler is the crawler inicialization method
func NewCrawler(urlString string, chans Channels, parents bool) (*Crawler, error) {
	urlObject, err := url.Parse(urlString)
	if err != nil {
		return nil, fmt.Errorf("unable to parse root url")
	}
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:    20,
			IdleConnTimeout: 30 * time.Second,
		},
	}
	if urlObject.Path == "" {
		urlObject.Path = "/"
	}
	crawler := &Crawler{
		root:         urlObject,
		channels:     chans,
		client:       client,
		scannedItems: map[string]bool{},
		scanParent:   parents,
	}
	crawler.urlsChan = make(chan url.URL, 1)

	return crawler, nil
}

// Run is the crawler start method
func (c *Crawler) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg.Add(n)
	for i := 0; i < n; i++ {
		go worker(c, ctx)
	}
	c.urlsChan <- *c.root
	wg.Wait()
}

func (c *Crawler) scanUrl(u *url.URL) error {
	if u.String() != "" && !strings.Contains(u.String(), "javascript:") {
		resp, err := c.client.Get(u.String())
		if err != nil {
			return fmt.Errorf("unable to fetch url %+v", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyReader := strings.NewReader(string(body))
		if err != nil {
			body = []byte{}
		}
		if channel, exists := c.channels[resp.StatusCode]; exists {
			page := Page{Url: *u, Resp: *resp, Body: string(body)}
			channel <- page
		}
		if strings.HasPrefix(resp.Header["Content-Type"][0], "text/html") {
			doc, err := html.Parse(bodyReader)
			if err != nil {
				return fmt.Errorf("unable to parse page body")
			}
			c.scanNode(doc)
		}

	}
	return nil
}

func (c *Crawler) scanNode(n *html.Node) {
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" {
				if u, err := url.Parse(a.Val); err == nil {
					c.urlsChan <- *u
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
		return *u, fmt.Errorf("host outside of this site")
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

func (c *Crawler) containsString(item string) bool {
	c.mu.Lock()
	_, contain := c.scannedItems[item]
	c.mu.Unlock()
	return contain
}
