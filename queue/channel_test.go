package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelFetchAndAdd(t *testing.T) {
	tests := []struct {
		name               string
		queueItemsToAdd    []CrawlItem
		queueItemsToFetch  int
		expectedItemsCount int
	}{
		{
			name:               "FetchLessThanAdded",
			queueItemsToAdd:    []CrawlItem{{URL: "https://testing.com/page1", Depth: 1}, {URL: "https://testing.com/page2", Depth: 1}, {URL: "https://testing.com/page3", Depth: 1}, {URL: "https://testing.com/page4", Depth: 1}, {URL: "https://testing.com/page5", Depth: 1}},
			queueItemsToFetch:  3,
			expectedItemsCount: 3,
		},

		{
			name:               "FetchMoreThanAdded",
			queueItemsToAdd:    []CrawlItem{{URL: "https://testing.com/page1", Depth: 1}, {URL: "https://testing.com/page2", Depth: 1}},
			queueItemsToFetch:  5,
			expectedItemsCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			que := NewChannelQueue()

			for _, item := range tt.queueItemsToAdd {
				que.Add(item)

			}
			items := <-que.Fetch(int64(tt.queueItemsToFetch))
			assert.Equal(t, tt.expectedItemsCount, len(items))
		})
	}
}

func TestChannelQueueSize(t *testing.T) {
	que := NewChannelQueue()
	items := []CrawlItem{{URL: "https://testing.com/page1", Depth: 1}, {URL: "https://testing.com/page2", Depth: 1}, {URL: "https://testing.com/page3", Depth: 1}, {URL: "https://testing.com/page4", Depth: 1}, {URL: "https://testing.com/page5", Depth: 1}}
	for _, item := range items {
		que.Add(item)
	}
	assert.Equal(t, 5, que.Size())
}
