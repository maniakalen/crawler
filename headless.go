package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type Headless struct {
}

func NewHeadless() *Headless {
	return &Headless{}
}

func (h *Headless) fetch(URL string) (*Page, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"),
		chromedp.WindowSize(1920, 1080),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	parsedURL, err := url.Parse(URL)
	if err != nil {
		log.Println("Failed to parse url ", URL)
		return nil, errors.New("failed to parse url")
	}
	var html string
	resp, err := chromedp.RunResponse(ctx,
		// Enable network to set headers
		network.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetExtraHTTPHeaders(map[string]interface{}{
				"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
				"Accept-Encoding":           "gzip, deflate, br",
				"Accept-Language":           "en-US,en;q=0.9",
				"Cache-Control":             "max-age=0",
				"Sec-Ch-Ua":                 `"Not A(Brand";v="99", "Google Chrome";v="125", "Chromium";v="125"`,
				"Sec-Ch-Ua-Mobile":          "?0",
				"Sec-Ch-Ua-Platform":        "Windows",
				"Sec-Fetch-Dest":            "document",
				"Sec-Fetch-Mode":            "navigate",
				"Sec-Fetch-Site":            "none",
				"Sec-Fetch-User":            "?1",
				"Upgrade-Insecure-Requests": "1",
				"DNT":                       "1",
			}).Do(ctx)
		}),
		// visit the target page
		chromedp.Navigate(URL),
		// wait for the page to load
		chromedp.Sleep(2000*time.Millisecond),
		// extract the raw HTML from the page
		chromedp.ActionFunc(func(ctx context.Context) error {
			// select the root node on the page
			rootNode, err := dom.GetDocument().Do(ctx)
			if err != nil {
				return err
			}
			html, err = dom.GetOuterHTML().WithNodeID(rootNode.NodeID).Do(ctx)
			return err
		}),
	)
	if err != nil {
		log.Println("Error while performing the automation logic:", err)
		return nil, errors.New("failed to get response")
	}
	m, err := json.Marshal(resp.Headers)
	if err != nil {
		slog.Error("Unable to marshal headers")
		return nil, errors.New("failed to marshal headers")
	}
	var headers map[string]interface{}
	err = json.Unmarshal(m, &headers)
	if err != nil {
		slog.Error("Failed to unmarshal headers")
		return nil, errors.New("failed to unmarshal headers")
	}
	page := &Page{
		URL: parsedURL,
		Resp: &PageResponse{
			StatusCode: int(resp.Status),
			Headers:    headers,
			Body:       strings.NewReader(html),
		},
		Body: html,
	}
	return page, nil
}
