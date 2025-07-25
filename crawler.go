package crawler

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/maniakalen/crawler/queue"
	"github.com/maniakalen/crawler/robots"
	"github.com/temoto/robotstxt"
)

// Config holds crawler configuration
type Config struct {
	StartURL            string
	AllowedDomains      []string // Domains to stay within
	UserAgents          []string
	CrawlDelay          time.Duration // Delay between requests to the same domain
	MaxDepth            int           // Maximum crawl depth
	MaxRetries          int           // Max retries for a failed request
	RequestTimeout      time.Duration
	QueueIdleTimeout    time.Duration
	ProxyURL            string // e.g., "http://user:pass@host:port"
	RobotsUserAgent     string // User agent to use for robots.txt checks
	ConcurrentRequests  int    // Number of concurrent fetch workers
	Channels            Channels
	Headers             map[string]string
	LanguageCode        string
	Filters             []func(Page, *Config) bool
	MaxIdleConnsPerHost int
	MaxIdleConns        int
	Proxies             []string
}

// Crawler represents the web crawler
type Crawler struct {
	config        Config
	robotsData    map[string]*robotstxt.RobotsData // Host -> RobotsData
	visited       map[string]bool
	visitedLock   sync.Mutex
	queue         queue.QueueInterface
	wg            sync.WaitGroup
	httpClient    *http.Client
	httpTransport *http.Transport
	proxyIdx      int
	//queue       chan CrawlItem
}

// CrawlItem represents an item in the crawl queue

// Page is a struct that carries the scanned url, response and response body string
type Page struct {
	URL  *url.URL      // Page url
	Resp http.Response // Page response as returned from the GET request
	Body string        // Response body string
}

// Channels is a Page channels map where the index is the response code so we can define different behavior for the different resp codes
type Channels map[int]chan Page

// NewCrawler initializes a new Crawler
func NewCrawler(config Config, queue queue.QueueInterface) (*Crawler, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %v", err)
	}

	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConnsPerHost,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		Proxy:               nil,
	}

	if config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy URL: %v", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := &http.Client{
		Jar:       jar,
		Timeout:   config.RequestTimeout,
		Transport: transport,
	}
	robots.GlobalRobotsCache.SetClient(client)

	return &Crawler{
		config:     config,
		httpClient: client,
		robotsData: make(map[string]*robotstxt.RobotsData),
		visited:    make(map[string]bool),
		queue:      queue,
		//queue:      make(chan CrawlItem, config.ConcurrentRequests*10), // Buffered channel
	}, nil
}

// getRandomUserAgent selects a random User-Agent
func (c *Crawler) getRandomUserAgent() string {
	if len(c.config.UserAgents) == 0 {
		return "GoCrawler/1.0 (+http://example.com/bot.html)" // Default if none provided
	}
	return c.config.UserAgents[rand.Intn(len(c.config.UserAgents))]
}

// canCrawl checks robots.txt for permission
func (c *Crawler) canCrawl(targetURL string) bool {
	return robots.IsURLAllowed(targetURL)
}

// fetch performs the HTTP GET request
func (c *Crawler) fetch(targetURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	for key, value := range c.config.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("User-Agent", c.getRandomUserAgent())
	// Add other headers if needed (e.g., Accept-Language)
	c.rotateProxy()
	log.Printf("Fetching: %s", targetURL)
	resp, err := c.httpClient.Do(req)

	// Basic retry logic
	for i := 0; i < c.config.MaxRetries && (err != nil || (resp != nil && resp.StatusCode >= 500)); i++ {
		log.Printf("Retrying (%d/%d) %s due to error or bad status", i+1, c.config.MaxRetries, targetURL)
		time.Sleep(time.Second * time.Duration(2*(i+1))) // Exponential backoff
		resp, err = c.httpClient.Do(req)
	}

	if err != nil {
		return nil, fmt.Errorf("failed after %d retries for %s: %v", c.config.MaxRetries, targetURL, err)
	}
	return resp, nil
}

// process processes a single URL
func (c *Crawler) process(item queue.CrawlItem) {
	if item.Depth > c.config.MaxDepth {
		log.Printf("Max depth reached for %s", item.URL)
		return
	}
	log.Println("Processing: ", item.URL)
	// Check if already visited
	c.visitedLock.Lock()
	if c.visited[item.URL] {
		c.visitedLock.Unlock()
		return
	}
	c.visited[item.URL] = true
	c.visitedLock.Unlock()

	// Respect robots.txt
	if !c.canCrawl(item.URL) {
		log.Printf("Disallowed by robots.txt: %s", item.URL)
		return
	}

	// Implement domain-specific delay
	// More sophisticated rate limiting would be per-host, not global.
	time.Sleep(c.config.CrawlDelay)
	resp, err := c.fetch(item.URL)
	if err != nil {
		log.Printf("Failed to fetch %s: %v", item.URL, err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("unable to read page body: " + err.Error())
	}
	log.Printf("Fetched [%d]: %s", resp.StatusCode, item.URL)

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			log.Printf("Blocked or rate limited for %s (Status %d). Consider increasing delay or changing IP/UA.", item.URL, resp.StatusCode)
			// Could implement logic here to slow down even more, or stop for this domain
			// For simplicity, we just log and return
			return
		}
		log.Printf("Failed to fetch %s: Status %s", item.URL, resp.Status)
		return
	}

	// Check content type if necessary
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		log.Printf("Skipping non-HTML content: %s", item.URL)
		return
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		log.Printf("Failed to parse HTML from %s: %v", item.URL, err)
		return
	}

	// Find and process links
	baseURL, _ := url.Parse(item.URL)
	page := Page{URL: baseURL, Resp: *resp, Body: string(body)}
	process := true
	for _, filter := range c.config.Filters {
		process = process && filter(page, &c.config)
	}
	if process {
		doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
			href, exists := s.Attr("href")
			if !exists {
				return
			}
			// Resolve relative URLs
			resolvedURL, err := baseURL.Parse(href)
			if err != nil {
				log.Printf("Error resolving URL %s from base %s: %v", href, baseURL, err)
				return
			}
			absURL := resolvedURL.String()
			absURL = strings.Split(absURL, "#")[0] // Remove fragment
			// Check if URL is within allowed domains
			isAllowed := false
			for _, domain := range c.config.AllowedDomains {
				if strings.Contains(resolvedURL.Host, domain) {
					isAllowed = true
					break
				}
			}
			if isAllowed {
				c.visitedLock.Lock()
				if !c.visited[absURL] {
					c.visitedLock.Unlock() // Unlock before sending to avoid deadlock if channel is full
					if item.Depth+1 <= c.config.MaxDepth {
						c.wg.Add(1)
						go func() {
							defer c.wg.Done()
							c.queue.Add(queue.CrawlItem{URL: absURL, Depth: item.Depth + 1})
						}()
					}
				} else {
					c.visitedLock.Unlock()
				}
			}
		})

		// --- TODO: Extract desired data from the page here ---
		// Example: title := doc.Find("title").First().Text()
		// fmt.Printf("Title of %s: %s\n", item.URL, title)

		if channel, exists := c.config.Channels[resp.StatusCode]; exists {
			log.Println("sending to channel:", resp.StatusCode)
			channel <- page
			log.Println("sent to channel:", resp.StatusCode)
		}
	}
}

func (c *Crawler) processSitemap() {
	sitemaps, err := robots.GetSitemaps(c.config.StartURL)
	if err != nil {
		slog.Error("Failed to fetch sitemaps list")
	} else {
		URLs := make([]robots.URL, 0)
		for _, sitemap := range sitemaps {
			list := robots.ExtractURLs(sitemap)
			URLs = append(URLs, list...)
		}
		for _, URL := range URLs {
			if allowed := c.canCrawl(URL.Loc); allowed {
				c.queue.Add(queue.CrawlItem{URL: URL.Loc, Depth: 0})
			}
		}
	}
}

func (c *Crawler) rotateProxy() {
	if len(c.config.Proxies) > 0 {
		log.Println("Rotating proxies")
		proxyUrl, err := url.Parse(c.config.Proxies[c.proxyIdx])
		if err != nil {
			log.Fatal("Failed to parse proxy server")
		}
		c.httpClient.Transport.(*http.Transport).Proxy = http.ProxyURL(proxyUrl)
		c.proxyIdx++
		if c.proxyIdx >= 10 {
			c.proxyIdx = 0
		}
	}
}

// Start begins the crawling process
func (c *Crawler) Start() {
	//c.wg.Add(1) // For the initial URL
	func() {
		//defer c.wg.Done()
		allowed := c.canCrawl(c.config.StartURL)
		if allowed {
			log.Printf("Url %s is allowed. Entering queue", c.config.StartURL)
			c.queue.Add(queue.CrawlItem{URL: c.config.StartURL, Depth: 0})
		}
		log.Println("Parsing sitemap for: ", c.config.StartURL)
		c.processSitemap()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	// Start worker goroutines
	for i := 0; i < c.config.ConcurrentRequests; i++ {
		c.wg.Add(1)
		go func(workerID int) {
			defer c.wg.Done()
			for {
				i++
				select {
				case <-ctx.Done():
					return
				case <-time.After(c.config.QueueIdleTimeout):
					return
				case items := <-c.queue.Fetch(100):
					if len(items) == 0 && c.queue.Size() == 0 {
						return
					}
					for _, item := range items {
						c.process(item)
					}
				}
			}
		}(i)
	}

	// Wait for all items to be processed.
	// This simple wait group might not be perfect for dynamic queueing;
	// a more robust solution involves checking queue size and active workers.
	// For this example, ensure wg.Add is called before sending to queue.
	go func() {
		// Close queue when all initial + discovered items are done
		c.wg.Wait()
		c.queue.Close()
		cancel()
	}()

	<-ctx.Done()
	time.Sleep(3 * time.Second)
	// Keep main alive until queue is closed and workers are done (they exit when queue closes)
	// A simple way to wait for workers to finish after queue is closed:
	// // This is a simplistic wait for workers, proper worker lifecycle mgmt is more complex.
	log.Println("Crawling finished.")
}
