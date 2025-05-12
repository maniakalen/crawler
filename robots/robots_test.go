package robots

import (
	"fmt"
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
			fmt.Printf("%+v\n", delay)
			assert.Equal(t, tt.expectedAllowed, allowed)
			assert.Equal(t, tt.expectedCrawlDelay, delay.Seconds())
		})
	}
}
