package disc

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"anidock/backend/internal/rss"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// fetcher 带缓存与限速的请求器（同一 URL 只抓一次，回验可复用宽搜结果）。
type fetcher struct {
	client *http.Client
	delay  time.Duration
	cache  map[string][]rss.Item
	notes  []string
}

func newFetcher(o Options) *fetcher {
	return &fetcher{
		client: &http.Client{Timeout: o.Timeout},
		delay:  o.Delay,
		cache:  map[string][]rss.Item{},
	}
}

// get 抓取并解析一个 RSS URL；失败时记入 notes 并返回错误（调用方决定跳过还是终止）。
func (f *fetcher) get(ctx context.Context, url string) ([]rss.Item, error) {
	if items, ok := f.cache[url]; ok {
		return items, nil
	}
	if f.delay > 0 && len(f.cache) > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpError{code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	items, err := rss.ParseFeedXML(body)
	if err != nil {
		return nil, err
	}
	f.cache[url] = items
	return items, nil
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "HTTP " + strconv.Itoa(e.code) }
