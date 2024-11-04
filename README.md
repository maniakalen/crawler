# Crawler

This module is used for crawling the web page given and return Page object for each status that has configured channel for it.

You can configure new channels using the Channels map like

```
	chans := crawler.Channels{
		404: make(chan crawler.Page),
		200: make(chan crawler.Page),
	}
```