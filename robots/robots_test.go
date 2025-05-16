package robots

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/temoto/robotstxt"
)

func TestRobotsCacheIsURLAllowed(t *testing.T) {
	GlobalRobotsCache.RobotName = "LinkCompass"
	tests := []struct {
		name               string
		domain             string
		robotstxt          string
		testUrl            string
		expectedAllowed    bool
		expectedCrawlDelay float64
	}{
		{
			name:               "Test title",
			domain:             "testsite",
			robotstxt:          "User-agent: *\nCrawl-delay: 60",
			testUrl:            "https://testsite/",
			expectedAllowed:    true,
			expectedCrawlDelay: 60,
		},
		{
			name:               "Test title",
			domain:             "testsite",
			robotstxt:          "User-agent: LinkCompass\nDisallow: /\nCrawl-delay: 60",
			testUrl:            "https://testsite/",
			expectedAllowed:    false,
			expectedCrawlDelay: 60,
		},
		{
			name:               "Test title",
			domain:             "testsite",
			robotstxt:          "User-agent: LinkCompass\nDisallow: /\nCrawl-delay: 60",
			testUrl:            "https://testsite/page1",
			expectedAllowed:    false,
			expectedCrawlDelay: 60,
		},
		{
			name:               "Test title",
			domain:             "testsite",
			robotstxt:          "User-agent: LinkCompass\nDisallow: /page2\nCrawl-delay: 60",
			testUrl:            "https://testsite/page1",
			expectedAllowed:    true,
			expectedCrawlDelay: 60,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := robotstxt.FromString(tt.robotstxt)

			assert.NoError(t, err)
			botEntry := &robotsEntry{}
			botEntry.data = data
			botEntry.fetchedAt = time.Now()
			botEntry.expiresAt = time.Now().Add(30 * time.Second)
			botEntry.crawlDelay = data.FindGroup(GlobalRobotsCache.RobotName).CrawlDelay
			GlobalRobotsCache.cache[tt.domain] = botEntry

			allowed, delay := IsURLAllowed(tt.testUrl)
			assert.Equal(t, tt.expectedAllowed, allowed)
			assert.Equal(t, tt.expectedCrawlDelay, delay.Seconds())
		})
	}
}

func TestRobotsSitemapsList(t *testing.T) {
	GlobalRobotsCache.RobotName = "LinkCompass"
	tests := []struct {
		name           string
		domain         string
		robotstxt      string
		testUrl        string
		expectedUrlOne string
	}{
		{
			name:           "Test sitemaps 1",
			domain:         "www.reuters.com",
			robotstxt:      "User-agent: *\nSITEMAP: https://www.reuters.com/arc/outboundfeeds/sitemap-index/?outputType=xml\nSITEMAP: https://www.reuters.com/arc/outboundfeeds/news-sitemap-index/?outputType=xml",
			testUrl:        "https://www.reuters.com",
			expectedUrlOne: "https://www.reuters.com/arc/outboundfeeds/sitemap-index/?outputType=xml",
		},
		{
			name:           "Test sitemap 2",
			domain:         "www.cnn.com",
			robotstxt:      "User-agent: *\nSitemap: https://www.cnn.com/sitemaps/cnn/index.xml\nSitemap: https://www.cnn.com/sitemaps/cnn/news.xml",
			testUrl:        "https://www.cnn.com",
			expectedUrlOne: "https://www.cnn.com/sitemaps/cnn/index.xml",
		},
		{
			name:           "Test sitemap 3",
			domain:         "www.cnn.com",
			robotstxt:      "User-agent: *\nsitemap: https://www.cnn.com/sitemaps/cnn/index.xml\nsitemap: https://www.cnn.com/sitemaps/cnn/news.xml",
			testUrl:        "https://www.cnn.com",
			expectedUrlOne: "https://www.cnn.com/sitemaps/cnn/index.xml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := robotstxt.FromString(tt.robotstxt)

			assert.NoError(t, err)
			botEntry := &robotsEntry{}
			botEntry.data = data
			botEntry.fetchedAt = time.Now()
			botEntry.expiresAt = time.Now().Add(30 * time.Second)
			botEntry.crawlDelay = data.FindGroup(GlobalRobotsCache.RobotName).CrawlDelay
			GlobalRobotsCache.cache[tt.domain] = botEntry

			sitemaps, err := GetSitemaps(tt.testUrl)
			assert.NoError(t, err)
			assert.Len(t, sitemaps, 2)
			assert.Equal(t, tt.expectedUrlOne, sitemaps[0])

		})
	}
}
