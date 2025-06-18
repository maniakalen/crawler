package queue

import (
	"context"
	"sync"
	"time"
)

type ChannelQueue struct {
	ctx     context.Context
	channel chan CrawlItem
	size    int
	mux     sync.Mutex
}

func NewChannelQueue(ctx context.Context) *ChannelQueue {
	queue := &ChannelQueue{
		ctx:     ctx,
		channel: make(chan CrawlItem, 100),
	}
	go func() {
		<-ctx.Done()
		queue.Close()
	}()
	return queue
}

func (c *ChannelQueue) Add(item CrawlItem) bool {
	c.mux.Lock()
	defer c.mux.Unlock()
	c.size++
	go func() {
		select {
		case c.channel <- item:
		case <-c.ctx.Done():
		}
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
			c.mux.Lock()
			c.size--
			c.mux.Unlock()
			if count <= 0 {
				break out
			}
		case <-time.After(500 * time.Millisecond):
			break out
		case <-c.ctx.Done():
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
	c.mux.Lock()
	c.size = 0
	c.mux.Unlock()
	return true
}

func (c *ChannelQueue) Size() int {
	return c.size
}

func (c *ChannelQueue) Close() {
	close(c.channel)
	c.channel = nil
}
