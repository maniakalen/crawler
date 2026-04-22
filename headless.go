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
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
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
		// visit the target page
		chromedp.Navigate(URL),
		// wait for the page to load
		chromedp.Sleep(2000*time.Millisecond),
		// bypass bot detection
		chromedp.Evaluate(`Object.defineProperty(navigator, 'webdriver', {get: () => undefined})`, nil),
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
