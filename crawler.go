package crawler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Crawler struct {
	root         *url.URL
	client       *http.Client
	scannedItems map[string]bool
	urlsChan     chan url.URL
	channels     Channels
}

type Page struct {
	Url  url.URL
	Resp http.Response
	Body string
}

type Channels map[int]chan Page

func NewCrawler(urlString string, chans Channels) (*Crawler, error) {
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
	}
	crawler.urlsChan = make(chan url.URL)
	return crawler, nil
}

func (c *Crawler) Run() {
	done := make(chan bool)
	go func(u *url.URL) {
		go c.scanUrl(u)
		c.scannedItems[u.String()] = true
		var ur url.URL
		i := 0
		for {
			select {
			case ur = <-c.urlsChan:
			case <-time.After(20 * time.Second):
				fmt.Println("Time elapsed")
				close(c.urlsChan)
				close(done)
				return
			}
			i++
			if i == 100 {
				<-time.After(200 * time.Millisecond)
				i = 0
			}
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
			c.scannedItems[ur.String()] = true
			go c.scanUrl(&ur)
		}
	}(c.root)
	<-done
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
			go c.scanNode(doc)
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
	return *u, nil

}

func (c *Crawler) containsString(item string) bool {
	_, contain := c.scannedItems[item]
	return contain
}
