package queue

type QueueInterface interface {
	Add(item CrawlItem) bool
	Fetch(count int64) <-chan []CrawlItem
	Reset() bool
	Size() int
	Close()
}

type CrawlItem struct {
	URL   string
	Depth int
}
