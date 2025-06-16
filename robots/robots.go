package robots

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"log/slog"

	"github.com/temoto/robotstxt"
)

type RobotsCache struct {
	cache      map[string]*robotsEntry
	mutex      sync.RWMutex
	RobotName  string
	httpClient *http.Client
}

type robotsEntry struct {
	data       *robotstxt.RobotsData
	fetchedAt  time.Time
	expiresAt  time.Time
	crawlDelay time.Duration
}

var GlobalRobotsCache = &RobotsCache{
	cache: make(map[string]*robotsEntry),
}

func (rc *RobotsCache) SetClient(client *http.Client) {
	rc.httpClient = client
}

// Fetch and parse robots.txt with caching
func (rc *RobotsCache) GetRobotsForDomain(scheme string, domain string) (*robotsEntry, error) {
	rc.mutex.RLock()
	entry, exists := rc.cache[domain]
	rc.mutex.RUnlock()

	now := time.Now()
	// Return cached entry if valid
	if exists && now.Before(entry.expiresAt) {
		return entry, nil
	}

	// Fetch new robots.txt
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", scheme, domain)

	req, err := http.NewRequest("GET", robotsURL, nil)
	if err != nil {
		slog.Error("failed to generate robots request")
	}
	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return createEmptyRobotsEntry(), err
	}
	defer resp.Body.Close()
	// Parse robots.txt
	robots, err := robotstxt.FromResponse(resp)
	if err != nil {
		log.Println(err.Error())
	}
	// Extract crawl delay
	crawlDelay := extractCrawlDelay(robots, rc.RobotName)

	// Create new cache entry
	newEntry := &robotsEntry{
		data:       robots,
		fetchedAt:  now,
		expiresAt:  now.Add(24 * time.Hour), // Cache for 24 hours
		crawlDelay: crawlDelay,
	}

	// Update cache
	rc.mutex.Lock()
	rc.cache[domain] = newEntry
	rc.mutex.Unlock()

	return newEntry, nil
}

// Check if URL is allowed
func IsURLAllowed(targetURL string) bool {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return false
	}
	robotsEntry, err := GlobalRobotsCache.GetRobotsForDomain(parsedURL.Scheme, parsedURL.Host)
	if err != nil {
		return false
	}

	path := parsedURL.Path
	if path == "" {
		path = "/"
	}
	return robotsEntry.data.TestAgent(targetURL, GlobalRobotsCache.RobotName)
}

func GetSitemaps(targetURL string) ([]string, error) {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}

	robotsEntry, err := GlobalRobotsCache.GetRobotsForDomain(parsedURL.Scheme, parsedURL.Host)
	if err != nil {
		// Default to allowed but with conservative delay
		return nil, err
	}
	return robotsEntry.data.Sitemaps, nil
}

func createEmptyRobotsEntry() *robotsEntry {
	return &robotsEntry{}
}

func extractCrawlDelay(data *robotstxt.RobotsData, agent string) time.Duration {
	group := data.FindGroup(agent)
	return group.CrawlDelay
}
