package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"anidock/backend/internal/model"
	"anidock/backend/internal/rss/disc"
	"anidock/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

// ---------- RSS 自动发现（v0.2.0） ----------
//
// 算法在 disc 包（宽搜 → 种子扩展 → 归属打分 → 流派分组 → 候选地址回验）。
// 接口层是后台任务：POST 启动任务（全局互斥，同一时刻仅一个），
// GET 轮询最近一次结果（关页/断连不中断，任务跑完落库），apply 按勾选候选批量建 RSS 源。

// discoveryOut 发现任务的 API 响应（JSON 列已反序列化）。
type discoveryOut struct {
	ID         int64              `json:"id"`
	AnimeID    int64              `json:"anime_id"`
	Site       string             `json:"site"`
	Status     string             `json:"status"`
	Phase      string             `json:"phase,omitempty"`
	Scanned    int                `json:"scanned"`
	Matched    int                `json:"matched"`
	Queries    []string           `json:"queries,omitempty"`
	Candidates []disc.Candidate   `json:"candidates"`
	Unmatched  []discoveryItemOut `json:"unmatched,omitempty"`
	Notes      []string           `json:"notes,omitempty"`
	Error      string             `json:"error,omitempty"`
	StartedAt  string             `json:"started_at"`
	FinishedAt string             `json:"finished_at,omitempty"`
}

// discoveryItemOut 低置信条目（人工确认区）。
type discoveryItemOut struct {
	Title string  `json:"title"`
	Link  string  `json:"link,omitempty"`
	Group string  `json:"group,omitempty"`
	Show  string  `json:"show,omitempty"`
	Score float64 `json:"score"`
	Level string  `json:"level"`
}

func toDiscoveryOut(d *store.Discovery) discoveryOut {
	out := discoveryOut{
		ID: d.ID, AnimeID: d.AnimeID, Site: d.Site, Status: d.Status, Phase: d.Phase,
		Scanned: d.Scanned, Matched: d.Matched, Error: d.Error,
		StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
	}
	if d.Queries != "" {
		_ = json.Unmarshal([]byte(d.Queries), &out.Queries)
	}
	if d.Candidates != "" {
		_ = json.Unmarshal([]byte(d.Candidates), &out.Candidates)
	}
	if out.Candidates == nil {
		out.Candidates = []disc.Candidate{}
	}
	if d.Unmatched != "" {
		var un []disc.Item
		_ = json.Unmarshal([]byte(d.Unmatched), &un)
		out.Unmatched = make([]discoveryItemOut, 0, len(un))
		for _, it := range un {
			out.Unmatched = append(out.Unmatched, discoveryItemOut{
				Title: it.Title, Link: it.Link, Group: it.Group, Show: it.Show,
				Score: it.Score, Level: it.Level,
			})
		}
	}
	if d.Notes != "" {
		_ = json.Unmarshal([]byte(d.Notes), &out.Notes)
	}
	return out
}

// discoveryTarget 从库内番剧构造发现目标（title/原名/别名/年/季/季标签）。
func discoveryTarget(a *model.Anime) disc.Target {
	return disc.Target{
		Title:       a.Title,
		TitleOrig:   a.TitleOrig,
		Aliases:     a.Aliases,
		Year:        a.Year,
		Quarter:     a.Quarter,
		SeasonLabel: a.SeasonLabel,
	}
}

func mustJSONStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// DiscoverRss POST /api/admin/anime/{id}/discover-rss
// 请求体 {"queries": ["可选补充宽搜词"], "no_verify": false, "auto_add": false}（均可空）。
// auto_add = 任务完成后自动把默认合格候选（有地址 ∧ 非 low ∧ 纯度 100%）批量建源，
// 与手动 apply 同一套去重（组名/地址/关键词重复均跳过）。
// 启动后台自动发现任务（全局互斥：同一时刻仅一个任务），立即返回任务 id；
// 结果经 GET /anime/{id}/discover-rss 轮询。
// 按当前站点池（设置页配置；兄弟站同数据源）尝试：主站不可达时自动切下一站，
// 产出候选的身份是站点无关的关键词（兄弟站物化 URL 在 apply 阶段完成）。
func (a *Admin) DiscoverRss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	an, err := a.Store.QueryAnimeByID(id)
	if err != nil {
		writeErr(w, 404, errors.New("番剧不存在"))
		return
	}
	var req struct {
		Queries  []string `json:"queries"`
		NoVerify bool     `json:"no_verify"`
		AutoAdd  bool     `json:"auto_add"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // 空体 = 自动查询集 + 完整回验
	sites := EffectiveSitePoolList(a.Store, a.Cfg)
	if len(sites) == 0 {
		writeErr(w, 400, errors.New("未配置订阅站点池：请先在「设置 → 抓取任务 → 订阅站点池」填写站点地址（默认空 = 关闭自动 RSS）"))
		return
	}
	if !a.discBusy.CompareAndSwap(0, 1) {
		writeErr(w, 409, errors.New("已有自动发现任务在进行，请稍候再试"))
		return
	}
	poolName := make([]string, len(sites))
	for i, s := range sites {
		poolName[i] = s.Name
	}
	rowID, err := a.Store.CreateDiscovery(id, strings.Join(poolName, "+"))
	if err != nil {
		a.discBusy.Store(0)
		writeErr(w, 500, err)
		return
	}
	target := discoveryTarget(an)
	opt := disc.Options{
		Queries:  req.Queries,
		NoVerify: req.NoVerify,
		OnPhase:  func(p string) { _ = a.Store.UpdateDiscoveryPhase(rowID, p) },
	}
	go a.runDiscovery(rowID, id, target, sites, opt, req.AutoAdd)
	writeJSON(w, 200, map[string]int64{"task_id": rowID})
}

// runDiscovery 后台执行：用 context.Background()，关闭网页/断连不中断任务，
// 跑完把报告落库（前端重开页面轮询即可看到结果）。
// autoAdd = 发现完成后自动把默认合格候选批量建源（结果摘要并入报告 notes）。
func (a *Admin) runDiscovery(rowID, animeID int64, t disc.Target, sites []disc.Site, opt disc.Options, autoAdd bool) {
	defer a.discBusy.Store(0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rep, err := disc.Discover(ctx, t, sites, opt)
	if err != nil {
		_ = a.Store.FinishDiscovery(rowID, false, err.Error(), "", "", "", "", 0, 0)
		return
	}
	cands := rep.Candidates
	if cands == nil {
		cands = []disc.Candidate{}
	}
	// 自动添加：组合逻辑择优建源（组名/地址/关键词重复均跳过，applyItems 内统一去重）
	if autoAdd {
		summary := a.autoAddCandidates(animeID, cands, false)
		rep.Notes = append(rep.Notes, "已自动添加 RSS 源："+summary)
		log.Printf("[discover] %s 自动添加完成: %s", rep.Target.Title, summary)
	}
	un := make([]discoveryItemOut, 0, len(rep.Unmatched))
	for _, it := range rep.Unmatched {
		un = append(un, discoveryItemOut{
			Title: it.Title, Link: it.Link, Group: it.Group, Show: it.Show,
			Score: it.Score, Level: it.Level,
		})
	}
	_ = a.Store.FinishDiscovery(rowID, true, "",
		mustJSONStr(rep.Queries), mustJSONStr(cands), mustJSONStr(un), mustJSONStr(rep.Notes),
		rep.Scanned, rep.Matched)
}

// autoAddCandidates 组合逻辑择优建源核心（单番剧发现 / 批量发现自动导入共用）：
// 合格候选（有地址 ∧ 非 low ∧ 纯度 100% ∧ 未命中黑名单）
// 按 ① 偏好字幕组/关键词（偏好列表越靠前优先级越高）→ ② 集数多 → ③ 首发早 排序，
// 创建数取 [最少, 最多]（设置页热改），返回摘要文本（含组合逻辑配置与跳过原因）。
// 优先级按名次分配：自动建的源排在番剧已有源之后，起点 = max(已有源优先级, 100) 向上取整到 10 的倍数 + 10
// （无已有源则从 100 起），名次每后一位 +10（数值小 = 优先）——选择顺序直接成为接管顺序。
// mirror = 本批自动建的源是否开启自动镜像（仅批量发现可选；单番剧发现恒为 false）。
func (a *Admin) autoAddCandidates(animeID int64, cands []disc.Candidate, mirror bool) string {
	maxPicks := EffectiveDiscMaxPicks(a.Store, a.Cfg)
	minPicks := EffectiveDiscMinPicks(a.Store, a.Cfg)
	prefs := EffectiveDiscPrefGroups(a.Store, a.Cfg)
	black := EffectiveDiscBlackGroups(a.Store, a.Cfg)
	var elig []disc.Candidate
	var blackOut []string
	for _, c := range cands {
		if c.URL == "" || c.Confidence == "low" || c.Purity < 1 {
			continue
		}
		if disc.BlackHit(c, black) {
			blackOut = append(blackOut, c.Group)
			continue // 黑名单命中：不自动选中
		}
		elig = append(elig, c)
	}
	ranked := disc.Rank(elig, prefs)
	n := maxPicks
	if n < minPicks {
		n = minPicks
	}
	if n > len(ranked) {
		n = len(ranked)
	}
	// 优先级按名次分配（数值小 = 优先）：自动建的源排在番剧已有源之后（人工调优的源保持在上），
	// 起点 = max(已有源优先级, 100) 向上取整到 10 的倍数 + 10（无已有源则从 100 起），
	// 名次每后一位 +10——组合逻辑选出的顺序直接体现为抓取/展示的接管顺序。
	maxPri := 0
	if feeds, err := a.Store.ListFeedsForAnime(animeID); err == nil {
		for _, f := range feeds {
			if f.Priority > maxPri {
				maxPri = f.Priority
			}
		}
	}
	base := 100
	if maxPri >= base {
		base = maxPri/10*10 + 10
	}
	var items []applyItemReq
	for i, c := range ranked[:n] {
		items = append(items, applyItemReq{Group: c.Group, Keyword: c.Keyword, URL: c.URL, Name: c.Group, Priority: base + i*10, Mirror: mirror})
	}
	policy := fmt.Sprintf("最多 %d / 最少 %d", maxPicks, minPicks)
	if len(prefs) > 0 {
		policy += "；偏好组 " + strings.Join(prefs, "/")
	}
	if len(black) > 0 {
		policy += "；黑名单 " + strings.Join(black, "/")
	}
	policy += "；同层按 集数↓ → 首发↑"
	blackNote := ""
	if len(blackOut) > 0 {
		blackNote = fmt.Sprintf("；黑名单排除 %d 个（%s）", len(blackOut), strings.Join(blackOut, "、"))
	}
	if len(items) == 0 {
		return fmt.Sprintf("无合格候选（%s）%s", policy, blackNote)
	}
	created, skipped := a.applyItems(animeID, candIndex(cands), items)
	return fmt.Sprintf("创建 %d 个（%s）%s%s", len(created), policy, skipSummary(skipped), blackNote)
}

// DiscoveryStatus GET /api/admin/anime/{id}/discover-rss —— 该番剧最近一次发现记录（前端轮询端点）。
// 尚无记录时 404（前端按"未发现过"处理）。
func (a *Admin) DiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	d, err := a.Store.LatestDiscovery(id)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if d == nil {
		writeErr(w, 404, errors.New("尚无自动发现记录"))
		return
	}
	writeJSON(w, 200, toDiscoveryOut(d))
}

type applyItemReq struct {
	Group    string `json:"group"`
	Keyword  string `json:"keyword"` // 站点无关关键词（候选身份；老报告无此字段时回退 URL 匹配）
	URL      string `json:"url"`
	Name     string `json:"name"`
	Priority int    `json:"priority"`
	Mirror   bool   `json:"mirror"`
	Color    string `json:"color"`
}

type applyItemOut struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Site     string `json:"site,omitempty"`
	Priority int    `json:"priority"`
	Reason   string `json:"reason,omitempty"` // 跳过原因
}

// candIndex 候选身份索引：优先 (group, keyword)，老报告无关键词时回退 (group, url)。
func candIndex(cands []disc.Candidate) map[string]disc.Candidate {
	idx := map[string]disc.Candidate{}
	for _, c := range cands {
		if c.Keyword != "" {
			idx[c.Group+"|kw|"+c.Keyword] = c
		}
		if c.URL != "" {
			if _, ok := idx[c.Group+"|url|"+c.URL]; !ok {
				idx[c.Group+"|url|"+c.URL] = c
			}
		}
	}
	return idx
}

// applyItems 批量建源核心（手动 apply 与发现后自动添加共用）：
// 每个条目按 (group, keyword|url) 匹配发现报告实际产出的候选（防传入任意地址），
// 每个候选只建**一个逻辑源**（feed 行）：带关键词的源抓取时自动按全局站点池
// 展开各站地址（供应商无感，加站/减站热生效），rss_url 仅存池首位站点的展示/兜底地址。
// 去重（命中任一即跳过，不重复建源）：同番剧已有同名（字幕组）源 / 相同 RSS 地址 /
// 相同关键词逻辑源。
func (a *Admin) applyItems(animeID int64, idx map[string]disc.Candidate, items []applyItemReq) (created, skipped []applyItemOut) {
	sites := EffectiveSitePoolList(a.Store, a.Cfg)
	for _, it := range items {
		name := it.Name
		if name == "" {
			name = it.Group
		}
		pri := it.Priority
		if pri <= 0 {
			pri = 100
		}
		key := ""
		if it.Keyword != "" {
			key = it.Group + "|kw|" + it.Keyword
		} else if it.URL != "" {
			key = it.Group + "|url|" + it.URL
		}
		if key == "" {
			skipped = append(skipped, applyItemOut{Name: name, Reason: "无订阅关键词/地址"})
			continue
		}
		cand, ok := idx[key]
		if !ok {
			skipped = append(skipped, applyItemOut{Name: name, URL: it.URL, Reason: "不是本次发现报告中的候选"})
			continue
		}
		// 字幕组查重：同番剧下已有同名源（大小写不敏感，含手动添加的）→ 直接跳过
		if f, err := a.Store.FeedByName(animeID, name); err == nil && f != nil {
			skipped = append(skipped, applyItemOut{Name: f.Name, URL: f.RSSURL, Site: f.Site, Priority: f.Priority, Reason: "该字幕组已订阅"})
			continue
		}
		// 带关键词的候选：只建一个逻辑源——rss_url 存"池首位站点"的物化地址（展示/兜底），
		// 抓取时按全局站点池自动展开各站（供应商无感）；老报告候选（无关键词）：按报告地址建单站源
		rssURL, site := cand.URL, disc.HostOf(cand.URL)
		if cand.Keyword != "" {
			if len(sites) == 0 {
				skipped = append(skipped, applyItemOut{Name: name, Reason: "未配置订阅站点池（设置 → 订阅站点池）"})
				continue
			}
			site = sites[0].Name
			rssURL = sites[0].URL(cand.Keyword)
		}
		aid := animeID
		if f, err := a.Store.FeedByURL(animeID, rssURL); err == nil && f != nil {
			skipped = append(skipped, applyItemOut{Name: f.Name, URL: f.RSSURL, Site: f.Site, Priority: f.Priority, Reason: "RSS 地址已存在"})
			continue
		}
		if cand.Keyword != "" {
			if f, err := a.Store.FeedByKeyword(animeID, cand.Keyword); err == nil && f != nil {
				skipped = append(skipped, applyItemOut{Name: f.Name, URL: f.RSSURL, Site: f.Site, Priority: f.Priority, Reason: "该逻辑源已订阅"})
				continue
			}
		}
		fid, err := a.Store.CreateFeed(name, rssURL, "", &aid, pri, 1, it.Mirror, it.Color, site, cand.Keyword)
		if err != nil {
			skipped = append(skipped, applyItemOut{Name: name, URL: rssURL, Site: site, Reason: "创建失败: " + err.Error()})
			continue
		}
		created = append(created, applyItemOut{ID: fid, Name: name, URL: rssURL, Site: site, Priority: pri})
	}
	if created == nil {
		created = []applyItemOut{}
	}
	if skipped == nil {
		skipped = []applyItemOut{}
	}
	return
}

// skipSummary 跳过原因按类别计数摘要（自动添加结果落 notes 用）。
func skipSummary(skipped []applyItemOut) string {
	if len(skipped) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, s := range skipped {
		counts[s.Reason]++
	}
	parts := make([]string, 0, len(counts))
	for reason, n := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", reason, n))
	}
	sort.Strings(parts)
	return fmt.Sprintf("，跳过 %d 个（%s）", len(skipped), strings.Join(parts, "、"))
}

// ApplyDiscovery POST /api/admin/anime/{id}/discover-rss/apply
// {"report_id": 3, "items": [{"group":"字幕组A","keyword":"番剧G 字幕组A"}]}
// 每个条目按 (group, keyword) 匹配该发现报告实际产出的候选（老报告回退 (group, url) 匹配），
// 防传入任意地址；建源与去重规则同 applyItems（组名/地址/关键词重复均跳过）。
func (a *Admin) ApplyDiscovery(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, errors.New("非法 id"))
		return
	}
	if _, err := a.Store.QueryAnimeByID(id); err != nil {
		writeErr(w, 404, errors.New("番剧不存在"))
		return
	}
	var req struct {
		ReportID int64          `json:"report_id"`
		Items    []applyItemReq `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	if req.ReportID <= 0 || len(req.Items) == 0 {
		writeErr(w, 400, errors.New("report_id 与 items 必填"))
		return
	}
	d, err := a.Store.GetDiscovery(req.ReportID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if d == nil {
		writeErr(w, 404, errors.New("发现记录不存在"))
		return
	}
	if d.AnimeID != id {
		writeErr(w, 400, errors.New("发现记录不属于该番剧"))
		return
	}
	var cands []disc.Candidate
	if d.Candidates != "" {
		_ = json.Unmarshal([]byte(d.Candidates), &cands)
	}
	created, skipped := a.applyItems(id, candIndex(cands), req.Items)
	writeJSON(w, 200, map[string]any{"created": created, "skipped": skipped})
}
