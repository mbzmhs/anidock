package handler

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"anidock/backend/internal/model"
	"anidock/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

type errResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, errResp{Error: err.Error()})
}

// Public 前台公开只读接口（纯查本地库，运行时不碰 Bangumi/RSS 源，DESIGN §1/§9.1）。
type Public struct{ Store *store.Store }

// parseAnimeListParams 解析 /api/anime 与 /api/admin/anime 共用的列表参数。
func parseAnimeListParams(r *http.Request) (model.AnimeFilter, int, int) {
	q := r.URL.Query()
	f := model.AnimeFilter{Q: q.Get("q"), Sort: q.Get("sort")}
	if v := q.Get("year"); v != "" {
		f.Year, _ = strconv.Atoi(v)
	}
	if v := q.Get("quarter"); v != "" {
		f.Quarter, _ = strconv.Atoi(v)
	}
	if q.Get("has_eps") == "1" {
		f.HasEps = true
	}
	if q.Get("has_feeds") == "1" {
		f.HasFeeds = true
	}
	// ids：逗号分隔的十进制 id，供外部客户端一次拉回多部番剧摘要（替代逐部请求）。
	// 缺失/空值 = 不筛选；解析失败的片段跳过；去重；超过 1000 个只取前 1000。
	if v := q.Get("ids"); v != "" {
		seen := make(map[int64]struct{}, 64)
		for _, part := range strings.Split(v, ",") {
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			f.Ids = append(f.Ids, id)
			if len(f.Ids) >= 1000 {
				break
			}
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	return f, limit, offset
}

// ListAnime GET /api/anime?year=&quarter=&q=&sort=&limit=&offset=
// limit>0 时返回分页结果；不传 limit 返回全量（侧边栏季度计数依赖全量）。
func (p *Public) ListAnime(w http.ResponseWriter, r *http.Request) {
	f, limit, offset := parseAnimeListParams(r)
	list, err := p.Store.ListAnime(f, limit, offset)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.Anime{}
	}
	writeJSON(w, 200, list)
}

// CountAnime GET /api/anime/count?year=&quarter=&q= —— 分页用总数
func (p *Public) CountAnime(w http.ResponseWriter, r *http.Request) {
	f, _, _ := parseAnimeListParams(r)
	n, err := p.Store.CountAnime(f)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"total": n})
}

// GetAnime GET /api/anime/{id}
func (p *Public) GetAnime(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	a, err := p.Store.GetAnime(id)
	if err != nil {
		writeErr(w, 404, errors.New("番剧不存在"))
		return
	}
	writeJSON(w, 200, a)
}

// AnimeEpisodes GET /api/anime/{id}/episodes —— 每集去重后的剧集列表（DESIGN §8）。
func (p *Public) AnimeEpisodes(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	episodes, err := p.Store.BestEpisodes(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if episodes == nil {
		episodes = []*model.EpisodeView{}
	}
	writeJSON(w, 200, episodes)
}

// AnimeFeeds GET /api/anime/{id}/feeds —— 隶属于该番剧的 RSS 源
func (p *Public) AnimeFeeds(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	feeds, err := p.Store.ListFeedsForAnime(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if feeds == nil {
		feeds = []*model.Feed{}
	}
	writeJSON(w, 200, feeds)
}

// ListFeeds GET /api/feeds
func (p *Public) ListFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := p.Store.ListFeeds()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if feeds == nil {
		feeds = []*model.Feed{}
	}
	writeJSON(w, 200, feeds)
}

// Latest GET /api/latest?limit=
func (p *Public) Latest(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	list, err := p.Store.ListLatest(limit)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.Release{}
	}
	writeJSON(w, 200, list)
}

// ---------- RSS 订阅（给外部 RSS 阅读器） ----------

type rssItemOut struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
	// enclosure = 资源文件/种子本体：支持附件的阅读器会直接显示可点链接与文件类型（无下载链时整体省略）
	Enclosure *struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"enclosure"`
}

type rssFeedOut struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title       string       `xml:"title"`
		Link        string       `xml:"link"`
		Description string       `xml:"description"`
		Language    string       `xml:"language"`
		LastBuild   string       `xml:"lastBuildDate"`
		TTL         int          `xml:"ttl"` // 建议轮询间隔（分钟）
		Items       []rssItemOut `xml:"item"`
	} `xml:"channel"`
}

// AnimeRSS GET /api/anime/{id}/rss —— 某番剧「聚合优选列表」的 RSS 2.0 订阅流。
// 每集一条：内容为该集当前最优源（best）。guid 按 (番剧, 集数, best release) 生成：
//   - best 不变 → guid 稳定，订阅方不会收到重复推送；
//   - 更优质的源替换 best → 出现同集数的新条目（标题/来源不同），相当于"该集资源升级"通知，
//     旧条目从流中消失但阅读器已归档的历史记录不受影响。
func (p *Public) AnimeRSS(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	a, err := p.Store.GetAnime(id)
	if err != nil {
		writeErr(w, 404, errors.New("番剧不存在"))
		return
	}
	episodes, err := p.Store.BestEpisodes(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	baseURL := scheme + "://" + r.Host
	sitePage := baseURL + "/anime/" + strconv.FormatInt(id, 10)

	// 按发布时间倒序（阅读器习惯最新在前），只给最近 100 集
	sort.Slice(episodes, func(i, j int) bool {
		return pubTime(episodes[i].Best).After(pubTime(episodes[j].Best))
	})
	if len(episodes) > 100 {
		episodes = episodes[:100]
	}

	var ch struct {
		Title       string       `xml:"title"`
		Link        string       `xml:"link"`
		Description string       `xml:"description"`
		Language    string       `xml:"language"`
		LastBuild   string       `xml:"lastBuildDate"`
		TTL         int          `xml:"ttl"`
		Items       []rssItemOut `xml:"item"`
	}
	ch.Title = a.Title
	ch.Link = sitePage
	ch.Description = fmt.Sprintf("%s每集最优源聚合更新（%d 集）", seasonStudio(a), len(episodes))
	ch.Language = "zh-CN"
	ch.LastBuild = time.Now().UTC().Format(time.RFC1123)
	ch.TTL = 60
	ch.Items = make([]rssItemOut, 0, len(episodes))

	for _, e := range episodes {
		b := e.Best
		// 条目主链接统一指向本站详情页（阅读器"打开页面"落到我们的聚合页）；
		// 资源方的原详情页只留在 description 里做辅助参考
		link := sitePage
		// 合集（episode_to 非空）→ "第 X~Y 集（合集）"；单集取 best 的集号
		//（e.Episode 是分组键，合集/集数未知时为负值，不能直接展示）
		from, hasFrom := 0.0, false
		if b.Episode != nil {
			from, hasFrom = *b.Episode, true
		}
		var epLabel string
		switch {
		case e.EpisodeTo != nil && hasFrom:
			epLabel = fmt.Sprintf("第 %g~%g 集（合集）", from, *e.EpisodeTo)
		case e.EpisodeTo == nil && e.Episode > 0:
			epLabel = fmt.Sprintf("第 %g 集", e.Episode)
		default:
			epLabel = "集数未知"
		}
		// 标题只保留 作品原标题 + 集数 + 来源/画质（不再带源方的规格长尾巴与别名堆）
		title := "《" + a.Title + "》"
		if e.EpisodeTo != nil && hasFrom {
			title += fmt.Sprintf(" 第 %g~%g 集（合集）", from, *e.EpisodeTo)
		} else if e.Episode > 0 {
			title += fmt.Sprintf(" 第 %g 集", e.Episode)
		}
		meta := strings.TrimSpace(b.FeedName + " " + b.Quality)
		if meta != "" {
			title += " · " + meta
		}

		// 已有本地镜像 → 下载/种子一律给本站镜像直链（无镜像才回退资源方原链）
		dlURL := b.DownloadURL
		if b.DownloadURL != "" && b.MirrorPath != "" {
			dlURL = baseURL + "/mirror/" + b.MirrorPath
		}
		// 注意：不要预先 html 转义，encoding/xml 编码器会对文本与属性值各转义一次
		desc := fmt.Sprintf("<p>%s ｜ 来源：%s%s</p>", epLabel, b.FeedName, specSuffix(b))
		if b.URL != "" {
			desc += fmt.Sprintf("<p><a href=\"%s\">原详情页</a></p>", b.URL)
		}
		if dlURL != "" {
			// 链接 + 纯文本地址各来一份：渲染 HTML 的阅读器点链接，不渲染的也能直接看到/复制地址
			desc += fmt.Sprintf("<p>下载/种子：<a href=\"%s\">%s</a></p>", dlURL, dlURL)
		}
		it := rssItemOut{
			Title:       title,
			Link:        link,
			GUID:        fmt.Sprintf("anime-%d-ep-%g-release-%d", id, e.Episode, b.ID),
			PubDate:     rfc1123(b.PublishedAt),
			Description: desc,
		}
		if dlURL != "" {
			// enclosure：镜像直链的文件类型可从文件名精确推断；原链则按 URL 特征猜
			enc := struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			}{URL: dlURL, Type: guessEnclosureType(dlURL)}
			it.Enclosure = &enc
		}
		ch.Items = append(ch.Items, it)
	}

	feed := rssFeedOut{Version: "2.0"}
	feed.Channel = ch
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(feed); err != nil {
		return
	}
}

// guessEnclosureType 按 URL 特征猜文件 MIME，写进 enclosure type（阅读器据此展示"文件/种子"）。
func guessEnclosureType(u string) string {
	if strings.HasPrefix(strings.TrimSpace(u), "magnet:") {
		return "application/x-bittorrent"
	}
	ext := strings.ToLower(filepath.Ext(strings.SplitN(u, "?", 2)[0]))
	switch ext {
	case ".zip":
		return "application/zip"
	case ".7z":
		return "application/x-7z-compressed"
	case ".rar":
		return "application/x-rar-compressed"
	case ".mkv":
		return "video/x-matroska"
	case ".mp4":
		return "video/mp4"
	case ".torrent":
		return "application/x-bittorrent"
	}
	return "application/octet-stream"
}

func seasonStudio(a *model.Anime) string {
	parts := []string{}
	if a.SeasonLabel != "" {
		parts = append(parts, a.SeasonLabel)
	}
	if a.Studio != "" {
		parts = append(parts, a.Studio)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ") + " "
}

func specSuffix(b model.Release) string {
	spec := b.Spec
	if spec == "" {
		spec = b.Quality
	}
	if spec == "" {
		return ""
	}
	return " ｜ " + spec
}

func pubTime(rel model.Release) time.Time {
	if t, err := time.Parse(time.RFC3339, rel.PublishedAt); err == nil {
		return t
	}
	return time.Time{}
}

// rfc1123 RSS 要求 RFC1123 日期格式；库内存的是 RFC3339，解析失败时回退当前时间。
func rfc1123(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC1123)
	}
	if t, err := time.Parse(time.RFC1123, s); err == nil {
		return t.Format(time.RFC1123)
	}
	return time.Now().UTC().Format(time.RFC1123)
}

// ---------- media 静态 ----------

// 兼容 md5(32) 与 sha1(40) 两种内容哈希命名
var reMediaFile = regexp.MustCompile(`^[a-f0-9]{32,40}\.(?:jpg|jpeg|png|webp|gif)$`)

// 扩展名放宽到 10 位（.torrent 等 7 位扩展名也要能过；只允许字母数字，防穿越）
var reMirrorFile = regexp.MustCompile(`^[a-f0-9]{40}\.[A-Za-z0-9]{1,10}$`)

// ServeMedia GET /media/{file} —— 下载落地、按哈希命名的图片（DESIGN §5.4）。
func ServeMedia(mediaDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("f")
		if name == "" {
			name = r.URL.Path[len("/media/"):]
		}
		if !reMediaFile.MatchString(name) {
			writeErr(w, 400, errors.New("非法文件名"))
			return
		}
		http.ServeFile(w, r, mediaDir+"/"+name)
	}
}

// ServeMirror GET /mirror/{file} —— 自动镜像的资源文件（支持 Range，浏览器可断点/边下边播）。
func ServeMirror(mirrorDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("f")
		if name == "" {
			name = r.URL.Path[len("/mirror/"):]
		}
		if !reMirrorFile.MatchString(name) {
			writeErr(w, 400, errors.New("非法文件名"))
			return
		}
		http.ServeFile(w, r, mirrorDir+"/"+name)
	}
}
