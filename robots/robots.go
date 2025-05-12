package robots

import (
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

type RobotsCache struct {
	cache     map[string]*robotsEntry
	mutex     sync.RWMutex
	RobotName string
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

// Fetch and parse robots.txt with caching
func (rc *RobotsCache) GetRobotsForDomain(domain string) (*robotsEntry, error) {
	rc.mutex.RLock()
	entry, exists := rc.cache[domain]
	rc.mutex.RUnlock()

	now := time.Now()
	// Return cached entry if valid
	if exists && now.Before(entry.expiresAt) {
		return entry, nil
	}

	// Fetch new robots.txt
	robotsURL := "https://" + domain + "/robots.txt"
	resp, err := http.Get(robotsURL)
	if err != nil {
		// Handle error, default to permissive
		return createEmptyRobotsEntry(), err
	}
	defer resp.Body.Close()

	// Parse robots.txt
	robots, err := robotstxt.FromResponse(resp)
	if err != nil {
		return createEmptyRobotsEntry(), err
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
func IsURLAllowed(targetURL string) (bool, time.Duration) {
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return false, 0
	}

	domain := parsedURL.Hostname()
	robotsEntry, err := GlobalRobotsCache.GetRobotsForDomain(domain)
	if err != nil {
		// Default to allowed but with conservative delay
		return true, 10 * time.Second
	}

	path := parsedURL.Path
	if path == "" {
		path = "/"
	}

	// Check if path is allowed for our user agent
	group := robotsEntry.data.FindGroup(GlobalRobotsCache.RobotName)
	allowed := group.Test(path)

	return allowed, robotsEntry.crawlDelay
}

func createEmptyRobotsEntry() *robotsEntry {
	return &robotsEntry{}
}

func extractCrawlDelay(data *robotstxt.RobotsData, agent string) time.Duration {
	group := data.FindGroup(agent)
	return group.CrawlDelay
}
