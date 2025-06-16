package queue

import (
	"time"
)

type ChannelQueue struct {
	channel chan CrawlItem
	size    int
}

func NewChannelQueue() *ChannelQueue {
	return &ChannelQueue{
		channel: make(chan CrawlItem, 100),
	}
}

func (c *ChannelQueue) Add(item CrawlItem) bool {
	c.size++
	go func() {
		c.channel <- item
	}()
	return true
}

func (c *ChannelQueue) Fetch(count int64) <-chan []CrawlItem {
	buff := make(chan []CrawlItem, 1)
	defer close(buff)
	items := make([]CrawlItem, 0)
out:
	for {
		select {
		case item := <-c.channel:
			items = append(items, item)
			count--
			c.size--
			if count <= 0 {
				break out
			}
		case <-time.After(500 * time.Millisecond):
			break out
		}
	}
	buff <- items
	return buff
}

func (c *ChannelQueue) Reset() bool {
	s := len(c.channel)
	close(c.channel)
	c.channel = make(chan CrawlItem, s)
	return true
}

func (c *ChannelQueue) Size() int {
	return c.size
}

func (c *ChannelQueue) Close() {
	close(c.channel)
	c.channel = nil
}
