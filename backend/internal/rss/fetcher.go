package rss

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"html"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"anidock/backend/internal/model"
	"anidock/backend/internal/store"
)

// Fetcher 后台定时轮询各 RSS 源（DESIGN §7.1）。
// 单源失败不影响其它源；错误写入 feed.last_error 供后台展示。
type Fetcher struct {
	Store    *store.Store
	Interval time.Duration
	Matcher  *Matcher
	client   *http.Client

	// 资源文件自动镜像（按 feed.Mirror 开关生效）
	MirrorDir      string
	MaxMirrorBytes int64 // 单文件上限，超限放弃
	mirrorClient   *http.Client

	// 运行时覆盖（后台「设置」热改，无需重启）；返回 0 = 用上方静态值
	IntervalFunc  func() time.Duration
	MirrorMaxFunc func() int64

	// PaceFunc 单次订阅请求的节流间隔（后台「设置」热改）；
	// 每次 HTTP 请求前等待「间隔 + 随机(0..间隔)」，把连发突发摊成不规则节奏。
	// nil = 默认 2s；返回 0 = 关闭节流。
	PaceFunc func() time.Duration

	// hostCool 站点限流冷却：收到 403/429（限流/拉黑信号）后暂停打该 host，
	// 到期自然恢复，避免源站正在封我们时继续高频轰炸。
	hostCool map[string]time.Time

	// PoolURLs 站点池展开：关键词 → 池内各站点的订阅地址（由 main 注入，
	// 每次抓取时读当前生效的站点池设置，加站/减站热生效；可为 nil = 无站点池，
	// 自动源回退到自身物化地址）。rss 包不能直接 import disc（disc 反向依赖本包）。
	PoolURLs func(keyword string) []string

	// 抓取互斥：定时抓取与手动抓取（单源/全部）共用，
	// 避免同一时刻两路并发打向同一批源（双倍请求、易触发源站限流）。
	fetchMu  sync.Mutex
	fetching bool
	// yielded 当前持锁的抓取轮次已被要求提前收尾（发现手动插队请求）：
	// 跑完当前单元（当前源 / 当前镜像文件）后停止本轮，让位给手动抓取。
	yielded bool

	// 手动"插队"队列：抢不到锁的手动刷新在这里登记让位请求（ticket）。
	// 后台（或另一个手动）抓取循环在每个检查点调用 preemptYield：
	// 有 ticket 就跑完当前单元 → 停轮 → 放锁 → 唤醒所有等待者（WaitBeginFetch 拿到锁执行）。
	preemptMu sync.Mutex
	preemptQ  []preemptTicket
}

// preemptTicket 一条插队请求：done 被后台关闭 = 后台已让位（锁即将/已经释放）。
type preemptTicket struct{ done chan struct{} }

// BeginFetch 抢占抓取执行权：true = 获得（用后须 EndFetch），false = 已有抓取在进行。
func (f *Fetcher) BeginFetch() bool {
	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
	if f.fetching {
		return false
	}
	f.fetching = true
	f.yielded = false // 新持锁方从干净状态开始
	return true
}

// WaitBeginFetch 带插队的抢锁：锁空立即获得；被占用则登记插队请求并等待——
// 持锁方（后台轮次 / 另一个手动任务）在每个检查点看到请求后跑完当前单元即让位。
// 同时以短轮询兜底竞态（持锁方在登记前已结束本轮 → 直接拿到释放的锁）。
// ctx 超时/取消返回 false（等待前会撤掉已登记的 ticket）。
func (f *Fetcher) WaitBeginFetch(ctx context.Context) bool {
	for {
		if f.BeginFetch() {
			return true
		}
		f.preemptMu.Lock()
		tk := preemptTicket{done: make(chan struct{})}
		f.preemptQ = append(f.preemptQ, tk)
		f.preemptMu.Unlock()
		for {
			select {
			case <-ctx.Done():
				f.dropTicket(tk)
				return false
			default:
			}
			if f.BeginFetch() {
				f.dropTicket(tk)
				return true
			}
			select {
			case <-ctx.Done():
				f.dropTicket(tk)
				return false
			case <-tk.done: // 后台已让位，下一圈 BeginFetch 拿到锁
			case <-time.After(300 * time.Millisecond): // 轮询兜底：持锁方可能已结束且未处理 ticket
			}
		}
	}
}

// dropTicket 撤掉自己登记的插队请求（等待超时 / 已拿到锁）。
func (f *Fetcher) dropTicket(tk preemptTicket) {
	f.preemptMu.Lock()
	defer f.preemptMu.Unlock()
	for i, t := range f.preemptQ {
		if t == tk {
			f.preemptQ = append(f.preemptQ[:i], f.preemptQ[i+1:]...)
			return
		}
	}
}

// preemptYield 检查点（每个源之前 / 每个镜像文件之前）：有待插队请求时，
// 标记本轮提前收尾并唤醒所有等待者。无请求时廉价返回 false。
func (f *Fetcher) preemptYield() bool {
	f.preemptMu.Lock()
	n := len(f.preemptQ)
	if n > 0 {
		for _, tk := range f.preemptQ {
			close(tk.done)
		}
		f.preemptQ = nil
	}
	f.preemptMu.Unlock()
	if n == 0 {
		return false
	}
	f.fetchMu.Lock()
	f.yielded = true
	f.fetchMu.Unlock()
	log.Printf("[rss] 检测到手动刷新插队请求，后台抓取跑完当前单元后提前结束本轮（剩余源留待下轮）")
	return true
}

// preempted 当前持锁轮次是否已被要求提前收尾（检查点命中过插队请求）。
func (f *Fetcher) preempted() bool {
	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
	return f.yielded
}

// EndFetch 释放抓取执行权。
func (f *Fetcher) EndFetch() {
	f.fetchMu.Lock()
	f.fetching = false
	f.fetchMu.Unlock()
}

// currentInterval 当前生效的轮询间隔。
func (f *Fetcher) currentInterval() time.Duration {
	if f.IntervalFunc != nil {
		if d := f.IntervalFunc(); d > 0 {
			return d
		}
	}
	if f.Interval > 0 {
		return f.Interval
	}
	return 3 * time.Minute
}

// currentMirrorMax 当前生效的单文件镜像上限。
func (f *Fetcher) currentMirrorMax() int64 {
	if f.MirrorMaxFunc != nil {
		if n := f.MirrorMaxFunc(); n > 0 {
			return n
		}
	}
	if f.MaxMirrorBytes > 0 {
		return f.MaxMirrorBytes
	}
	return 1 << 20
}

func NewFetcher(st *store.Store, interval time.Duration) *Fetcher {
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	return &Fetcher{
		Store:          st,
		Interval:       interval,
		Matcher:        NewMatcher(),
		client:         &http.Client{Timeout: 45 * time.Second},
		MaxMirrorBytes: 1 << 20, // 只镜像种子文件，1MiB 足够
		mirrorClient:   &http.Client{Timeout: 30 * time.Minute}, // 大文件给足时间
		hostCool:       map[string]time.Time{},
	}
}

// Start 启动后台轮询（启动时随机相位后再跑首轮，之后按轮询间隔轮询，
// 每轮前再随机偏移；间隔支持运行时热改，每次 tick 后检测变化并重建 ticker）。
// 随机相位让多实例（本地/生产）不在同一时刻起跳、每轮节拍不规则，
// 降低「整齐连发」被源站识别为机器流量的特征。
func (f *Fetcher) Start(ctx context.Context) {
	go func() {
		if !sleepCtx(ctx, time.Duration(rand.Int63n(60*int64(time.Second)))) {
			return
		}
		f.fetchAllEnabled(ctx)
		cur := f.currentInterval()
		t := time.NewTicker(cur)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !sleepCtx(ctx, f.cycleJitter()) {
					return
				}
				f.fetchAllEnabled(ctx)
			}
			if n := f.currentInterval(); n != cur {
				t.Stop()
				cur = n
				t = time.NewTicker(cur)
			}
		}
	}()
}

// cycleJitter 每轮定时抓取前的随机偏移（0..min(90s, 轮询间隔/2)）。
func (f *Fetcher) cycleJitter() time.Duration {
	max := 90 * time.Second
	if h := f.currentInterval() / 2; h > 0 && h < max {
		max = h
	}
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// sleepCtx 可取消的等待；ctx 结束返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// currentPace 当前生效的单请求节流间隔（0 = 关闭）。
func (f *Fetcher) currentPace() time.Duration {
	if f.PaceFunc != nil {
		return f.PaceFunc()
	}
	return 2 * time.Second
}

// paceWait 单请求节流：等待「间隔 + 随机(0..间隔)」（间隔 2s → 实际 2~4s）。
// 所有抓取经 BeginFetch 互斥后串行，故全局节奏 = 对每个 host 的节奏。
func (f *Fetcher) paceWait(ctx context.Context) {
	d := f.currentPace()
	if d <= 0 {
		return
	}
	wait := d + time.Duration(rand.Int63n(int64(d)))
	_ = sleepCtx(ctx, wait)
}

// hostOf 订阅地址的 host（限流冷却键）。
func hostOf(u string) string {
	if i := strings.Index(u, "://"); i > 0 {
		host := u[i+3:]
		if j := strings.IndexByte(host, '/'); j > 0 {
			host = host[:j]
		}
		return strings.ToLower(host)
	}
	return ""
}

// cooldownUntil 该 host 的限流冷却截止时间（无 = 零值/ok=false）。
func (f *Fetcher) cooldownUntil(host string) (time.Time, bool) {
	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
	until, ok := f.hostCool[host]
	return until, ok
}

// setCooldown 该 host 进入 15 分钟限流冷却，并顺带清理过期项。
func (f *Fetcher) setCooldown(host string) {
	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
	now := time.Now()
	for k, v := range f.hostCool {
		if v.Before(now) {
			delete(f.hostCool, k)
		}
	}
	f.hostCool[host] = now.Add(15 * time.Minute)
	log.Printf("[rss] host %s 触发限流信号，冷却 15 分钟（期间暂停对该站请求）", host)
}

// FetchAll 手动立即抓取全部启用源，返回 feedID→错误（成功为空串）。
func (f *Fetcher) FetchAll(ctx context.Context) map[int64]string {
	feeds, err := f.Store.ListFeeds()
	if err != nil {
		return map[int64]string{}
	}
	results := map[int64]string{}
	for _, feed := range feeds {
		if !feed.Enabled {
			continue
		}
		if f.preemptYield() { // 源间插队检查点：有手动刷新在等 → 停止本轮让位
			break
		}
		if err := f.FetchFeed(ctx, feed); err != nil {
			results[feed.ID] = err.Error()
			log.Printf("[rss] feed %q 抓取失败: %v", feed.Name, err)
			continue
		}
		if f.preempted() { // 本源内部（镜像下载前）触发了让位
			break
		}
		results[feed.ID] = ""
	}
	return results
}

// feedURLs 该逻辑源本轮要抓的订阅地址列表：
//   - 带关键词的自动源：按当前站点池逐站展开（抓取时读设置，加站/减站热生效，
//     供应商对使用者无感——一个逻辑源一行，站点池是全局设置）；
//   - 池为空或无关键词：回退到源自身物化的 RSS 地址（手动源 / 池被清空时的兜底）。
func (f *Fetcher) feedURLs(feed *model.Feed) []string {
	var urls []string
	if kw := strings.TrimSpace(feed.Keyword); kw != "" && f.PoolURLs != nil {
		urls = f.PoolURLs(kw)
	}
	if len(urls) == 0 && strings.TrimSpace(feed.RSSURL) != "" {
		urls = append(urls, strings.TrimSpace(feed.RSSURL))
	}
	seen := map[string]bool{}
	out := urls[:0]
	for _, u := range urls {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// FetchFeed 抓取单个逻辑源：按池序（池首位 = 最高优先站点）逐一尝试
// 下载 → 解析 → 逐条 item 解析/匹配 → upsert（全部写同一个 feed_id）。
// 兄弟站数据相同：**主站成功即停**，不再请求后续站点（省请求）；
// 主站失败才切下一站（无感接管）。全部失败才记 last_error（逐站原因拼接）。
func (f *Fetcher) FetchFeed(ctx context.Context, feed *model.Feed) error {
	urls := f.feedURLs(feed)
	if len(urls) == 0 {
		err := errors.New("无可用订阅地址（站点池为空且源未配置地址）")
		f.Store.SetFeedStatus(feed.ID, "", err.Error())
		return err
	}
	animes, _ := f.Store.AllAnime()
	var errs []string
	for _, u := range urls {
		if err := f.fetchFeedURL(ctx, feed, u, animes); err == nil {
			if len(errs) > 0 {
				log.Printf("[rss] feed %q 前序站点失败已切换: %s", feed.Name, strings.Join(errs, "；"))
			}
			f.Store.SetFeedStatus(feed.ID, store.NowStr(), "")
			return nil
		} else {
			errs = append(errs, hostOf(u)+": "+err.Error())
			log.Printf("[rss] feed %q 站点 %s 抓取失败: %v", feed.Name, hostOf(u), err)
		}
	}
	msg := strings.Join(errs, "；")
	f.Store.SetFeedStatus(feed.ID, "", msg)
	return errors.New(msg)
}

// fetchFeedURL 抓取单个站点地址：限流冷却检查 → 节流等待 → 下载 → 解析 → 逐条 upsert（含镜像下载）。
func (f *Fetcher) fetchFeedURL(ctx context.Context, feed *model.Feed, u string, animes []*model.Anime) error {
	if host := hostOf(u); host != "" {
		if until, ok := f.cooldownUntil(host); ok && time.Now().Before(until) {
			return fmt.Errorf("host %s 处于限流冷却（剩 %s）", host, time.Until(until).Round(time.Second))
		}
	}
	f.paceWait(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (anidock-rss-fetcher)")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 403/429 = 源站限流/封禁信号：进入冷却，停止对该站的高频重试
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		if host := hostOf(u); host != "" {
			f.setCooldown(host)
		}
		return fmt.Errorf("RSS 返回 HTTP %d（疑似限流，该站已冷却）", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("RSS 返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	items, err := ParseFeedXML(body)
	if err != nil {
		return err
	}

	// 绑定的番剧（per-show 源的季词冲突过滤用）
	var bind *model.Anime
	if feed.AnimeID != nil {
		for _, an := range animes {
			if an.ID == *feed.AnimeID {
				bind = an
				break
			}
		}
	}

	newCount := 0
	now := time.Now().UTC()
	for _, it := range items {
		parsed := ParseTitle(it.Title)
		if parsed.Group == "" {
			parsed.Group = feed.Name
		}
		if bind != nil && seasonConflict(bind, it.Title, pubTimeOf(it.PubDate), now) {
			log.Printf("[rss] feed %q 跳过季词冲突条目: %s", feed.Name, it.Title)
			continue
		}
		detailURL, downloadURL := resolveLinks(it)
		animeID := feed.AnimeID
		if animeID == nil {
			if hit := f.Matcher.Match(parsed.Show, animes, feed.AnimeID); hit != nil {
				animeID = &hit.ID
			}
		}
		release := &model.Release{
			FeedID:      feed.ID,
			AnimeID:     animeID,
			Title:       it.Title,
			Episode:     parsed.Episode,
			EpisodeTo:   parsed.EpisodeTo,
			URL:         detailURL,
			DownloadURL: downloadURL,
			Quality:     parsed.Quality,
			Spec:        parsed.Spec,
			GroupName:   parsed.Group,
			PublishedAt: NormalizePubDate(it.PubDate),
		}
		dedup := DedupKey(feed.ID, detailURL, it.Title, parsed.Group)
		if err := f.Store.UpsertRelease(release, dedup); err != nil {
			log.Printf("[rss] upsert 失败 feed=%s item=%q: %v", feed.Name, it.Title, err)
			continue
		}
		newCount++
		// 开启镜像的源：为新条目自动下载资源文件（已镜像/失败的条目下轮重试）
		if feed.Mirror && isMirrorableURL(downloadURL) {
			// enclosure type 只对其自身的 url 有效；从 description 提取的直链不能套用
			encType := ""
			if it.Enclosure != "" && downloadURL == it.Enclosure {
				encType = it.EncType
			}
			if rel, err := f.Store.GetReleaseByDedup(feed.ID, dedup); err == nil && rel != nil && rel.MirrorPath == "" {
				if f.preemptYield() { // 文件间插队检查点：跑完当前文件后让位
					break
				}
				f.mirrorOne(feed.ID, downloadURL, rel, encType)
			}
		}
	}
	log.Printf("[rss] feed %q (%s) 抓取完成: %d 条", feed.Name, u, newCount)
	return nil
}

// ---------- 季词冲突过滤 ----------
//
// 实现移至 season.go：与 disc 发现侧共用的统一季数提取器 SeasonSet，
// 以及无季词条目（S1 老文件惯例不带季词）的发布时间/BDrip 兜底防线。

// isMirrorableURL 镜像只收种子文件：http(s) 直链，且扩展名为 .torrent 或无扩展名
// （无扩展名时由 mirrorOne 按响应 Content-Type 再判定）。明确的视频/压缩包等扩展名一律排除。
func isMirrorableURL(u string) bool {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false
	}
	if ext := mirrorExtFromURL(u); ext != "" {
		return isTorrentExt(ext)
	}
	return true // 无扩展名，留给响应头 Content-Type 判定
}

// isTorrentExt 扩展名是否为种子文件(.torrent)。
func isTorrentExt(ext string) bool {
	return strings.EqualFold(ext, ".torrent")
}

// isTorrentMime MIME 是否为种子文件(bittorrent)。
func isTorrentMime(ct string) bool {
	return mimeExt(ct) == ".torrent"
}

// mirrorOne 只镜像种子文件(.torrent)。落盘名恒为 hash+.torrent。
// 判定：URL 扩展名为 .torrent 直接采信；URL 不带扩展名时，只要 enclosure type 或
// 响应 Content-Type 写明是种子(bittorrent)就照常镜像，其余一律丢弃。
// 流程：流式写入 .part 临时文件（带大小上限）→ os.Rename 原子落盘 → 回写 release.mirror_path。
// 任一步失败只删临时文件，不影响条目本身；下轮抓取会重试。最终文件已存在时直接复用（崩溃自愈）。
func (f *Fetcher) mirrorOne(feedID int64, url string, rel *model.Release, encType string) {
	if f.MirrorDir == "" {
		return
	}
	// enclosure type 明确不是种子(如视频)且 URL 不带 .torrent → 连请求都不发
	if encType != "" && mimeExt(encType) != "" && !isTorrentMime(encType) && mirrorExtFromURL(url) == "" {
		return
	}
	hash := mirrorHash(feedID, url)
	// 只收种子文件，落盘名恒为 hash+.torrent
	final := filepath.Join(f.MirrorDir, hash+".torrent")

	// 自愈：文件已在上轮完成但没来得及回写
	if st, err := os.Stat(final); err == nil && st.Size() > 0 {
		_ = f.Store.SetReleaseMirror(rel.ID, hash+".torrent", st.Size())
		return
	}
	if _, err := os.Stat(final); err == nil {
		_ = os.Remove(final) // 0 字节残留
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (anidock-mirror)")
	resp, err := f.mirrorClient.Do(req)
	if err != nil {
		log.Printf("[mirror] 下载失败 feed_id=%d %s: %v", feedID, url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[mirror] 下载失败 feed_id=%d %s: HTTP %d", feedID, url, resp.StatusCode)
		return
	}

	// 非种子一律不收：URL 不带 .torrent 时，只有 enclosure type 或响应 Content-Type
	// 表明是种子(bittorrent)才落盘，否则丢弃（不落盘、不记镜像）。
	ct := resp.Header.Get("Content-Type")
	if mirrorExtFromURL(url) == "" && !isTorrentMime(encType) && !isTorrentMime(ct) {
		log.Printf("[mirror] 非种子文件跳过 feed_id=%d ct=%s", feedID, ct)
		return
	}

	tmp := final + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		log.Printf("[mirror] 创建临时文件失败: %v", err)
		return
	}
	// CopyN 上限+1：读到超过上限立即中止；正常读完返回 io.EOF 属预期（上限支持运行时热改）
	maxBytes := f.currentMirrorMax()
	n, err := io.CopyN(out, resp.Body, maxBytes+1)
	out.Close()
	if n > maxBytes {
		_ = os.Remove(tmp)
		log.Printf("[mirror] 超过大小上限(%dMB)放弃: %s", maxBytes>>20, url)
		return
	}
	if err != nil && err != io.EOF {
		_ = os.Remove(tmp)
		log.Printf("[mirror] 下载中断 feed_id=%d %s: %v", feedID, url, err)
		return
	}
	if n <= 0 {
		_ = os.Remove(tmp)
		log.Printf("[mirror] 空文件放弃: %s", url)
		return
	}
	// 原子落盘（同目录 rename），之后才对外可见
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		log.Printf("[mirror] 落盘失败: %v", err)
		return
	}
	if err := f.Store.SetReleaseMirror(rel.ID, hash+".torrent", n); err != nil {
		log.Printf("[mirror] 回写失败: %v", err)
		return
	}
	log.Printf("[mirror] 完成 feed_id=%d %s.torrent (%dKB)", feedID, hash, n>>10)
}

// mirrorHash 文件名主体：(feed_id, url) 的 sha1，稳定且防重名。
func mirrorHash(feedID int64, url string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d|%s", feedID, url)))
	return hex.EncodeToString(sum[:])
}

// mirrorExtFromURL URL 路径（去 query）上的扩展名，合法（1~5 位字母数字）才采信。
func mirrorExtFromURL(url string) string {
	ext := filepath.Ext(strings.SplitN(url, "?", 2)[0])
	if len(ext) <= 11 && reMirrorExt.MatchString(ext) {
		return ext
	}
	return ""
}

var reMirrorExt = regexp.MustCompile(`^\.[a-zA-Z0-9]{1,10}$`)

// mimeToExt 常见资源格式的 MIME → 扩展名。
var mimeToExt = map[string]string{
	"application/zip":            ".zip",
	"application/x-zip-compressed": ".zip",
	"application/x-7z-compressed":  ".7z",
	"application/x-rar-compressed": ".rar",
	"application/x-rar":            ".rar",
	"application/rar":              ".rar",
	"video/x-matroska":             ".mkv",
	"video/mp4":                    ".mp4",
	"video/x-msvideo":              ".avi",
	"video/webm":                   ".webm",
	"video/x-flv":                  ".flv",
	"video/mpeg":                   ".mpg",
	"application/x-bittorrent":     ".torrent",
	"application/x-torrent":        ".torrent",
	"application/torrent":          ".torrent",
}

// mimeExt 从 MIME（enclosure type 或响应 Content-Type）映射扩展名，未知返回 ""。
func mimeExt(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ext, ok := mimeToExt[ct]; ok {
		return ext
	}
	return ""
}

func (f *Fetcher) fetchAllEnabled(ctx context.Context) {
	if !f.BeginFetch() {
		return // 手动抓取正在进行，跳过本轮，避免并发打向同一批源
	}
	defer f.EndFetch()
	_ = f.FetchAll(ctx)
}

// DedupKey 去重键：有 url 用 url，否则用 (feedID, title, group) 哈希。
func DedupKey(feedID int64, url, title, group string) string {
	if url != "" {
		return url
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s|%s", feedID, group, title)
	return fmt.Sprintf("hash:%x", h.Sum64())
}

// ---------- RSS / Atom 解析 ----------

type rssDoc struct {
	Channel rssChannel `xml:"channel"`
}
type rssChannel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}
type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Guid        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Enclosure   struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"enclosure"`
}

type atomDoc struct {
	XMLName xml.Name `xml:"feed"`
	Entries []struct {
		Title   string `xml:"title"`
		Updated string `xml:"updated"`
		Published string `xml:"published"`
		Links   []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		Content     string `xml:"content"`
		ContentHTML string `xml:"content>html"`
		Summary     string `xml:"summary"`
	} `xml:"entry"`
}

// reResourceURL 路径（去 query）以资源文件扩展名结尾 = 直链资源。
var reResourceURL = regexp.MustCompile(`\.(?:torrent|mp4|mkv|avi|flv|webm|mpg|zip|rar|7z)$`)

// isResourceURL 判断 URL 是否为种子/媒体/归档直链（详情页不会带这些扩展名）。
func isResourceURL(u string) bool {
	return reResourceURL.MatchString(strings.ToLower(strings.SplitN(strings.TrimSpace(u), "?", 2)[0]))
}

// resolveLinks 确定一条条目的详情页与下载链接（DESIGN §7.2 / 附录 A）。
// 常规 feed：link = 详情页，enclosure = 种子/资源，无 enclosure 时从 description 提取磁力/直链。
// 部分种子站的 RSS：无 enclosure，link 本身就是 .torrent 直链、guid 才是详情页 → 两者对调，
// 否则种子分析不出来、条目只剩一个指向下载文件的"详情页"。
func resolveLinks(it Item) (detailURL, downloadURL string) {
	detailURL = strings.TrimSpace(it.Link)
	downloadURL = strings.TrimSpace(it.Enclosure)
	if downloadURL == "" && isResourceURL(detailURL) {
		downloadURL = detailURL
		if g := strings.TrimSpace(it.Guid); g != "" {
			detailURL = g
		}
	}
	if downloadURL == "" {
		_, downloadURL = ParseLinks(detailURL, StripHTML(it.Description))
	}
	return
}

// ParseFeedXML 按文档头判断 RSS 2.0 / Atom，统一归一化为 []Item。
func ParseFeedXML(body []byte) ([]Item, error) {
	return parseFeedXML(body)
}

// parseFeedXML 同上（内部实现）。
func parseFeedXML(body []byte) ([]Item, error) {
	head := string(body[:min(len(body), 300)])
	if strings.Contains(head, "<rss") {
		var rss rssDoc
		if err := xml.Unmarshal(body, &rss); err != nil {
			return nil, fmt.Errorf("解析 RSS: %w", err)
		}
		items := make([]Item, 0, len(rss.Channel.Items))
		for _, it := range rss.Channel.Items {
			// 站点常把转义文本再包进 CDATA（"字幕组F&amp;字幕组A"），
			// XML 解析器不会二次解转义，需手动还原实体。
			items = append(items, Item{Title: html.UnescapeString(it.Title), Link: it.Link, Guid: it.Guid, Description: html.UnescapeString(it.Description), PubDate: it.PubDate, Enclosure: it.Enclosure.URL, EncType: it.Enclosure.Type})
		}
		return items, nil
	}
	var atom atomDoc
	if err := xml.Unmarshal(body, &atom); err != nil {
		return nil, fmt.Errorf("解析 Atom: %w", err)
	}
	if len(atom.Entries) == 0 {
		return nil, nil
	}
	items := make([]Item, 0, len(atom.Entries))
	for _, e := range atom.Entries {
		link, desc := "", e.Content
		if desc == "" {
			desc = e.ContentHTML
		}
		if e.Summary != "" {
			desc = e.Summary
		}
		for _, l := range e.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}
		pub := e.Published
		if pub == "" {
			pub = e.Updated
		}
		items = append(items, Item{Title: html.UnescapeString(e.Title), Link: link, Description: html.UnescapeString(desc), PubDate: pub})
	}
	return items, nil
}

// NormalizePubDate 兼容 RFC1123(RSS) / RFC3339(Atom) → UTC RFC3339。
func NormalizePubDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	// 有些源带 GMT 等时区名
	if t, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

// pubTimeOf 原始 PubDate 字符串 → time（解析失败返回零值；季词兜底防线用）。
func pubTimeOf(s string) time.Time {
	n := NormalizePubDate(s)
	if n == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, n); err == nil {
		return t
	}
	return time.Time{}
}

// StripHTML 去掉 description 里的 HTML 标签（提取链接前）。
var reTags = regexp.MustCompile(`<[^>]+>`)

func StripHTML(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
