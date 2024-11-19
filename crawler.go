// Package crawler provides functionalities to crawl a given website's pages
package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	ctx          *context.Context
	mux          sync.Mutex
}

// Page is a struct that carries the scanned url, response and response body string
type Page struct {
	Url  url.URL       // Page url
	Resp http.Response // Page response as returned from the GET request
	Body string        // Response body string
}

// Channels is a Page channels map where the index is the response code so we can define different behavior for the different resp codes
type Channels map[int]chan Page

// NewCrawler is the crawler inicialization method
func NewCrawler(urlString string, chans Channels, parents bool, ctx *context.Context) (*Crawler, error) {
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
		ctx:          ctx,
	}
	crawler.urlsChan = make(chan url.URL)
	return crawler, nil
}

// Run is the crawler start method
func (c *Crawler) Run() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func(u *url.URL) {
		go c.scanUrl(u, &wg)
		c.scannedItems[u.String()] = true
		var ur url.URL
		for {
			select {
			case ur = <-c.urlsChan:
				ur, err := c.repairUrl(&ur)
				if err != nil {
					continue
				}
				if u.Host != c.root.Host {
					continue
				}
				if c.containsString(ur.String()) {
					continue
				}
				c.mux.Lock()
				c.scannedItems[ur.String()] = true
				c.mux.Unlock()
				wg.Add(1)
				go c.scanUrl(&ur, &wg)
			case <-(*c.ctx).Done():
				return
			}
		}
	}(c.root)
	wg.Wait()
	close(c.urlsChan)
	for idx, channel := range c.channels {
		close(channel)
		delete(c.channels, idx)
	}
}

func (c *Crawler) scanUrl(u *url.URL, wg *sync.WaitGroup) error {
	defer wg.Done()
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
			select {
			case channel <- page:
			case <-(*c.ctx).Done():
				return fmt.Errorf("closed")
			}
		}
		if strings.HasPrefix(resp.Header["Content-Type"][0], "text/html") {
			doc, err := html.Parse(bodyReader)
			if err != nil {
				return fmt.Errorf("unable to parse page body")
			}
			wg.Add(1)
			go c.scanNode(doc, wg)
		}

	}
	return nil
}

func (c *Crawler) scanNode(n *html.Node, wg *sync.WaitGroup) {
	defer wg.Done()
	if n.Type == html.ElementNode && n.Data == "a" {
		for _, a := range n.Attr {
			if a.Key == "href" {
				if u, err := url.Parse(a.Val); err == nil {
					select {
					case c.urlsChan <- *u:
					case <-(*c.ctx).Done():
						return
					}
				}
				break
			}
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		wg.Add(1)
		go c.scanNode(child, wg)
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
	c.mux.Lock()
	_, contain := c.scannedItems[item]
	c.mux.Unlock()
	return contain
}
