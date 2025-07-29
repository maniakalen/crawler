package crawler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maniakalen/crawler/queue"
	"github.com/temoto/robotstxt"
	// Ensure this matches your import in crawler.go
)

// Helper to create a default config for testing
func newTestConfig() Config {
	return Config{
		UserAgents: []string{
			"TestAgent/1.0",
			"TestAgent/2.0",
		},
		CrawlDelay:         1 * time.Millisecond, // Faster for tests
		MaxDepth:           2,
		MaxRetries:         1,
		RequestTimeout:     2 * time.Second,
		QueueIdleTimeout:   30 * time.Second,
		RobotsUserAgent:    "TestRobotsAgent",
		ConcurrentRequests: 2,
	}
}

// Helper to discard log output during tests if needed
func quietLogs() func() {
	log.SetOutput(io.Discard)
	return func() {
		log.SetOutput(os.Stderr) // Or os.Stdout, depending on your preference
	}
}

// TestNewCrawler tests the initialization of the crawler
func TestNewCrawler(t *testing.T) {
	cfg := newTestConfig()
	cfg.ProxyURL = "http://localhost:8080"

	crawler, err := NewCrawler(cfg, queue.NewChannelQueue(context.Background()))
	if err != nil {
		t.Fatalf("NewCrawler() error = %v, wantErr %v", err, false)
	}
	if crawler.httpClient.Transport == nil {
		t.Fatal("NewCrawler() httpClient.Transport is nil")
	}

	// Check if proxy is set (simplified check)
	// Note: Accessing transport.Proxy directly requires type assertion.
	// For a robust check, you might need to make a request through the client
	// with a proxy server that records requests.
	// This basic check verifies the proxy URL parsing and setting logic.
	if transport, ok := crawler.httpClient.Transport.(*http.Transport); ok {
		if transport.Proxy == nil {
			t.Error("NewCrawler() proxy not set in transport")
		}
	} else {
		t.Error("NewCrawler() transport is not *http.Transport")
	}

	// Test invalid proxy URL
	cfg.ProxyURL = "::invalid_url"
	_, err = NewCrawler(cfg, queue.NewChannelQueue(context.Background()))
	if err == nil {
		t.Errorf("NewCrawler() with invalid proxy URL, error = nil, wantErr true")
	}
}

// TestGetRandomUserAgent tests the User-Agent rotation
func TestGetRandomUserAgent(t *testing.T) {
	cfg := newTestConfig()
	crawler, _ := NewCrawler(cfg, queue.NewChannelQueue(context.Background()))

	ua1 := crawler.getRandomUserAgent()
	ua2 := crawler.getRandomUserAgent()

	if !contains(cfg.UserAgents, ua1) {
		t.Errorf("getRandomUserAgent() returned %s, not in provided list", ua1)
	}
	if !contains(cfg.UserAgents, ua2) {
		t.Errorf("getRandomUserAgent() returned %s, not in provided list", ua2)
	}

	// Test default UA
	cfg.UserAgents = []string{}
	crawlerNoUA, _ := NewCrawler(cfg, queue.NewChannelQueue(context.Background()))
	defaultUA := crawlerNoUA.getRandomUserAgent()
	if defaultUA != "GoCrawler/1.0 (+http://example.com/bot.html)" {
		t.Errorf("getRandomUserAgent() with no UAs = %s, want default", defaultUA)
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// TestCanCrawl tests robots.txt logic

// TestProcessURL tests the core URL processing logic
func TestProcessURL(t *testing.T) {
	restoreLogs := quietLogs()
	defer restoreLogs()

	var server *httptest.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" { // Allow all for simplicity in these tests
			fmt.Fprintln(w, "User-agent: *")
			fmt.Fprintln(w, "Allow: /")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/page1":
			fmt.Fprintln(w, `<html><body><a href="/page2">Page 2</a><a href="http://external.com/page3">External</a></body></html>`)
		case "/page2":
			fmt.Fprintln(w, `<html><body><p>Page 2 Content</p><a href="/page1">Page 1 Backlink</a><a href="/page3_depth_limit">Page 3 DL</a></body></html>`)
		case "/page3_depth_limit":
			fmt.Fprintln(w, `<html><body><p>Page 3 Content</p></body></html>`)
		case "/nolinx":
			fmt.Fprintln(w, `<html><body><p>No links here.</p></body></html>`)
		case "/404page":
			http.NotFound(w, r)
		case "/403page":
			http.Error(w, "Forbidden", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tests := []struct {
		name                 string
		startPath            string
		maxDepth             int
		allowedDomains       []string
		expectedQueueLen     int // Approximate, depends on concurrency and exact link finding
		expectedVisitedCount int
		expectErrorForStart  bool
		requiresHeadless     bool
	}{
		{
			name:                 "SimpleCrawl",
			startPath:            "/page1",
			maxDepth:             1,
			allowedDomains:       []string{strings.Replace(server.URL, "http://", "", 1)},
			expectedQueueLen:     1, // page2
			expectedVisitedCount: 1, // page1, page2
			requiresHeadless:     false,
		},
		{
			name:                 "DepthLimit",
			startPath:            "/page1",
			maxDepth:             0, // Only start URL
			allowedDomains:       []string{strings.Replace(server.URL, "http://", "", 1)},
			expectedQueueLen:     0,
			expectedVisitedCount: 1, // page1
			requiresHeadless:     false,
		},
		{
			name:                 "ExternalLinkIgnored",
			startPath:            "/page1",
			maxDepth:             1,
			allowedDomains:       []string{strings.Replace(server.URL, "http://", "", 1)}, // external.com is not allowed
			expectedQueueLen:     1,                                                       // page2
			expectedVisitedCount: 1,
			requiresHeadless:     false, // page1, page2
		},
		{
			name:                 "NoLinks",
			startPath:            "/nolinx",
			maxDepth:             1,
			allowedDomains:       []string{strings.Replace(server.URL, "http://", "", 1)},
			expectedQueueLen:     0,
			expectedVisitedCount: 1,
			requiresHeadless:     false,
		},
		{
			name:                 "PageNotFound",
			startPath:            "/404page",
			maxDepth:             1,
			allowedDomains:       []string{strings.Replace(server.URL, "http://", "", 1)},
			expectedQueueLen:     0,
			expectedVisitedCount: 1,    // Visited (attempted)
			expectErrorForStart:  true, // Fetch itself will log an error
			requiresHeadless:     false,
		},
		{
			name:                 "PageForbidden",
			startPath:            "/403page",
			maxDepth:             1,
			allowedDomains:       []string{strings.Replace(server.URL, "http://", "", 1)},
			expectedQueueLen:     0,
			expectedVisitedCount: 1, // Visited (attempted)
			expectErrorForStart:  true,
			requiresHeadless:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTestConfig()
			cfg.StartURL = server.URL + tt.startPath
			cfg.MaxDepth = tt.maxDepth
			cfg.AllowedDomains = tt.allowedDomains
			cfg.RequireHeadless = tt.requiresHeadless
			// Use a single concurrent request for predictable queue/visited counts in test
			cfg.ConcurrentRequests = 1
			cqueue := queue.NewChannelQueue(context.Background())
			crawler, _ := NewCrawler(cfg, cqueue)
			// Ensure robots.txt checks use the mock server
			crawler.httpClient = server.Client()
			crawler.robotsData = make(map[string]*robotstxt.RobotsData)

			initialItem := queue.CrawlItem{URL: cfg.StartURL, Depth: 0}

			// We are testing `process` in isolation here mostly.
			// The `queue` is normally populated by `Start` or other `process` calls.
			// To test `process`, we need to simulate an item being dequeued.
			// We will collect new items that `process` would add to the queue.

			// Using a WaitGroup to simulate how `process` interacts with `wg.Done`
			crawler.wg.Add(1) // for the initial item being processed
			// Process the item
			go func() {
				crawler.wg.Done()
				crawler.process(initialItem)
			}() // Run in goroutine as process calls wg.Done

			crawler.wg.Wait() // Wait for the item.Depth=0 process call to complete
			var itemsFound []queue.CrawlItem
			for items := range cqueue.Fetch(1) {
				itemsFound = append(itemsFound, items...)
			}

			if len(itemsFound) != tt.expectedQueueLen {
				t.Errorf("%s: process() generated %d items for queue, want %d. Items: %v", tt.name, len(itemsFound), tt.expectedQueueLen, itemsFound)
			}

			crawler.visitedLock.Lock()
			actualVisitedCount := len(crawler.visited)
			crawler.visitedLock.Unlock()
			if actualVisitedCount != tt.expectedVisitedCount {
				t.Errorf("%s: process() resulted in %d visited sites, want %d. Visited: %v", tt.name, actualVisitedCount, tt.expectedVisitedCount, crawler.visited)
			}
		})
	}
}

// TestStartCrawl provides a higher-level integration test for the Start method
func TestStartCrawl(t *testing.T) {
	restoreLogs := quietLogs()
	defer restoreLogs()

	var requestCount struct {
		sync.Mutex
		m map[string]int
	}
	requestCount.m = make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Lock()
		requestCount.m[r.URL.Path]++
		requestCount.Unlock()
		if r.URL.Path == "/robots.txt" {
			fmt.Fprintln(w, "User-agent: *")
			fmt.Fprintln(w, "Allow: /")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/start":
			//fmt.Println(`<html><body><a href="/pageA">Page A</a> <a href="/pageB">Page B</a></body></html>`)
			fmt.Fprintln(w, `<html><body><a href="/pageA">Page A</a> <a href="/pageB">Page B</a></body></html>`)
		case "/pageA":
			//fmt.Println(`<html><body><a href="/start">Back to Start</a> <a href="/pageC">Page C</a></body></html>`)
			fmt.Fprintln(w, `<html><body><a href="/start">Back to Start</a> <a href="/pageC">Page C</a></body></html>`)
		case "/pageB":
			//fmt.Println(`<html><body><p>No more links here</p></body></html>`)
			fmt.Fprintln(w, `<html><body><p>No more links here</p></body></html>`)
		case "/pageC": // Will be beyond MaxDepth=1 for /start
			//fmt.Println(`<html><body><p>Page C content</p></body></html>`)
			fmt.Fprintln(w, `<html><body><p>Page C content</p></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := newTestConfig()
	cfg.StartURL = server.URL + "/start"
	cfg.AllowedDomains = []string{strings.Replace(server.URL, "http://", "", 1)}
	cfg.MaxDepth = 1 // /start (0), /pageA (1), /pageB (1). /pageC is depth 2 from /start via /pageA.
	cfg.ConcurrentRequests = 2
	cfg.CrawlDelay = 1 * time.Millisecond

	crawler, err := NewCrawler(cfg, queue.NewChannelQueue(context.Background()))
	if err != nil {
		t.Fatalf("Failed to create crawler: %v", err)
	}
	// Ensure robots.txt and pages are fetched from the mock server
	crawler.httpClient = server.Client()

	// Run the crawler. The Start method has its own goroutine management.
	// We need a way to know when it's "done" or use a timeout.
	// The original Start method's wait logic is simple.
	// For testing, we might rely on observing visited sites or a timeout.

	crawler.Start() // This blocks in the original code due to how wg and queue are handled.
	// For testing, it might be better if Start was non-blocking or returned a channel.
	// Given current Start(): it will run, workers will start, process queue, then wg.Wait leads to close(queue)
	// The workers will then exit. The main Start goroutine waits for a simple `doneProcessing` which is not very robust.

	// Check visited pages
	crawler.visitedLock.Lock()
	defer crawler.visitedLock.Unlock()
	expectedVisited := map[string]bool{
		server.URL + "/start": true,
		server.URL + "/pageA": true,
		server.URL + "/pageB": true,
		// server.URL + "/pageC" should NOT be visited due to MaxDepth
	}
	if len(crawler.visited) != len(expectedVisited) {
		t.Errorf("Expected %d visited pages, got %d. Visited: %v", len(expectedVisited), len(crawler.visited), crawler.visited)
	}
	for url := range expectedVisited {
		if !crawler.visited[url] {
			t.Errorf("Expected URL %s to be visited, but it was not.", url)
		}
	}
	if crawler.visited[server.URL+"/pageC"] {
		t.Errorf("URL %s/pageC should NOT have been visited due to MaxDepth.", server.URL)
	}

	// Check request counts (ensure pages were actually fetched and not just marked visited)
	requestCount.Lock()
	defer requestCount.Unlock()
	if requestCount.m["/start"] == 0 {
		t.Errorf("Expected /start to be fetched, count is 0")
	}
	if requestCount.m["/pageA"] == 0 {
		t.Errorf("Expected /pageA to be fetched, count is 0")
	}
	if requestCount.m["/pageB"] == 0 {
		t.Errorf("Expected /pageB to be fetched, count is 0")
	}
	if requestCount.m["/pageC"] > 0 {
		t.Errorf("Expected /pageC not to be fetched (MaxDepth), count is %d", requestCount.m["/pageC"])
	}
}
