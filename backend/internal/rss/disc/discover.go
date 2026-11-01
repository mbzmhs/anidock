package disc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"anidock/backend/internal/rss"
)

// Discover 执行完整发现流程：
// 宽搜(多关键词) → 条目解析 → 归属打分(名称/播出窗口/季词/跨组共识)
// → 流派分组(组×简繁/无修/规格) → 每流派最优关键词搜索+回验。
// sites 为站点池（兄弟站同数据源）：按序尝试，主站不可达/无结果时切换下一站，
// 后续种子扩展与候选回验都在命中的站点（active）上进行；候选地址的"身份"
// 是站点无关的关键词（Candidate.Keyword），物化到具体站点的 URL 由调用方推导。
func Discover(ctx context.Context, t Target, sites []Site, opt Options) (*Report, error) {
	opt.fillDefaults()
	if len(sites) == 0 {
		return nil, fmt.Errorf("站点池为空（请在设置中配置订阅站点 URL 模板/地址）")
	}
	f := newFetcher(opt)

	queries := opt.Queries
	if len(queries) == 0 {
		queries = BuildQueries(t)
	}
	if len(queries) > opt.MaxQueries {
		queries = queries[:opt.MaxQueries]
	}
	rep := &Report{Target: t, Site: sites[0].Name, Queries: queries}

	// ---------- 1) 宽搜（站点池按序回退） ----------
	var active Site
	siteSearch := func(s Site) {
		if opt.OnPhase != nil {
			opt.OnPhase("宽搜")
		}
		seen := map[string]bool{}
		for _, q := range queries {
			if ctx.Err() != nil {
				break
			}
			url := s.URL(q)
			raw, err := f.get(ctx, url)
			if err != nil {
				rep.Notes = append(rep.Notes, fmt.Sprintf("宽搜失败 %q: %v", q, err))
				continue
			}
			n := 0
			for _, r := range raw {
				if n >= opt.PerQueryCap {
					break
				}
				key := strings.TrimSpace(r.Link)
				if key == "" {
					key = "t:" + strings.TrimSpace(r.Title)
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				p := rss.ParseTitle(r.Title)
				rep.Items = append(rep.Items, Item{
					Title:     r.Title,
					Link:      r.Link,
					PubTime:   parsePub(r.PubDate),
					Query:     q,
					Group:     p.Group,
					Show:      p.Show,
					Episode:   p.Episode,
					EpisodeTo: p.EpisodeTo,
					Quality:   p.Quality,
					Spec:      p.Spec,
				})
				n++
			}
		}
	}
	for i := range sites {
		active = sites[i]
		siteSearch(active)
		if len(rep.Items) > 0 {
			break
		}
		if i+1 < len(sites) {
			rep.Notes = append(rep.Notes, fmt.Sprintf("站点 %s 无结果/不可达，切换 %s 重试", active.Name, sites[i+1].Name))
		}
	}
	if len(rep.Items) == 0 {
		rep.Notes = append(rep.Notes, "所有站点的所有宽搜关键词均无结果（站点均不可达或关键词全部落空）")
		return rep, nil
	}
	rep.Site = active.Name

	// ---------- 1.5) 第二轮：种子扩展 ----------
	now := time.Now().UTC()
	if opt.OnPhase != nil {
		opt.OnPhase("种子扩展")
	}
	names := targetNames(t)
	consensus := consensusOf(rep.Items)
	for i := range rep.Items {
		attribute(&rep.Items[i], t, names, consensus, now)
	}
	rep.Items, consensus = seedExpand(ctx, f, active, t, opt, rep, names, consensus)
	if len(rep.Items) == 0 {
		return rep, nil
	}

	// ---------- 2) 归属打分（含跨组共识，基于两轮合并集重算） ----------
	consensus = consensusOf(rep.Items)
	for i := range rep.Items {
		attribute(&rep.Items[i], t, names, consensus, now)
	}

	// ---------- 3) 流派分组（组 × 变体） ----------
	lines := map[string]*lineGroup{}
	var order []string
	for _, it := range rep.Items {
		if it.Level == "low" || it.Group == "" {
			continue
		}
		key := it.Group // 先按组聚合，变体标签最后用最新条目生成
		ln := lines[key]
		if ln == nil {
			ln = &lineGroup{group: it.Group}
			lines[key] = ln
			order = append(order, key)
		}
		ln.items = append(ln.items, it)
		if it.PubTime.After(ln.lastPub) {
			ln.lastPub = it.PubTime
		}
		if ln.score < it.Score {
			ln.score = it.Score
		}
	}
	// 排序：先按置信档（high 优先——已完结但高置信的组不应被"更新"的低置信组挤出预算），
	// 同档内按「最新进度」（越新的流派越可能值得订阅）
	tierOf := func(k string) int {
		for _, it := range lines[k].items {
			if it.Level == "high" {
				return 0
			}
		}
		return 1
	}
	sort.SliceStable(order, func(i, j int) bool {
		ti, tj := tierOf(order[i]), tierOf(order[j])
		if ti != tj {
			return ti < tj
		}
		return lines[order[j]].lastPub.Before(lines[order[i]].lastPub)
	})
	if len(order) > opt.MaxGroups {
		order = order[:opt.MaxGroups]
	}

	// ---------- 4) 每流派：最优关键词搜索 + 回验 ----------
	if !opt.NoVerify && opt.OnPhase != nil {
		opt.OnPhase("回验候选地址")
	}
	for _, key := range order {
		if ctx.Err() != nil {
			break
		}
		ln := lines[key]
		// 组自己写法的番名：取组内最高频的 Show 写法
		showOwn := modeString(modeShows(ln.items))
		// 已知集数（期望覆盖）
		expected := map[int]struct{}{}
		for _, it := range ln.items {
			if e := intEps(it.Episode); e > 0 {
				expected[e] = struct{}{}
			}
		}
		var eps []int
		for e := range expected {
			eps = append(eps, e)
		}
		sort.Ints(eps)

		c := Candidate{
			Group:    ln.group,
			Episodes: eps,
			Variant:  variantLine(ln.items),
		}
		// 时间线
		first, last := now, now
		for _, it := range ln.items {
			if !it.PubTime.IsZero() {
				if it.PubTime.Before(first) {
					first = it.PubTime
				}
				if it.PubTime.After(last) {
					last = it.PubTime
				}
			}
		}
		c.FirstPub = first.Format("2006-01-02")
		c.LastPub = last.Format("2006-01-02")
		c.Sample = lastItem(ln.items).Title
		c.Pace = paceOf(ln.items, now)
		c.Confidence = ln.level()

		if !opt.NoVerify {
			var url, sample, vnote, kw string
			var purity, coverage float64
			var tried []string
			var vEps []int
			url, kw, purity, coverage, sample, vEps, vnote, tried = searchOptimal(ctx, f, active, t,
				ln.group, showOwn, expected, consensus, opt.CandsPerGroup, now)
			c.URL, c.Keyword, c.Purity, c.Coverage, c.Tried = url, kw, purity, coverage, tried
			if sample != "" {
				c.Sample = sample
			}
			// 宽搜只能采到部分集数；回验 feed 通常是该组完整 per-show 列表，
			// 两者取并集 → 候选展示完整集数（如 1~14 而不是散落的几集）
			if url != "" && len(vEps) > 0 {
				c.Episodes = mergeInts(c.Episodes, vEps)
			}
			switch {
			case url == "":
				c.Note = "回验无可用地址（候选 URL 全部失败）"
				c.Confidence = "low"
			case purity < 1:
				c.Note = fmt.Sprintf("纯度 %.0f%%（<100%%，feed 内混有他组/他季条目）", purity*100)
			case coverage < 0.5 && len(expected) >= 2:
				c.Note = fmt.Sprintf("覆盖 %.0f%%（关键词可能过窄，早期集数缺失）", coverage*100)
			}
			if vnote != "" {
				if c.Note != "" {
					c.Note += "；"
				}
				c.Note += "回验异常: " + vnote
			}
			if c.Confidence != "low" && purity < 1 {
				c.Confidence = "low"
			}
		} else {
			c.Note = "未回验（--no-verify）"
		}
		rep.Candidates = append(rep.Candidates, c)
	}

	// ---------- 5) 低置信区 ----------
	for _, it := range rep.Items {
		if it.Level == "low" {
			rep.Unmatched = append(rep.Unmatched, it)
		}
	}
	rep.Matched = len(rep.Items) - len(rep.Unmatched)
	rep.Scanned = len(rep.Items)
	return rep, nil
}

// mergeInts 两个集数列表取并集并升序。
func mergeInts(a, b []int) []int {
	m := map[int]struct{}{}
	for _, v := range a {
		m[v] = struct{}{}
	}
	for _, v := range b {
		m[v] = struct{}{}
	}
	out := make([]int, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// ---------- 辅助 ----------

// seedExpand 第二轮宽搜：用第一轮命中条目的番名写法（组自己的命名）作为新关键词。
// 只对"疑似本番"的条目取种子，避免被无关大词带偏；新条目并入原集合并去重。
func seedExpand(ctx context.Context, f *fetcher, site Site, t Target, opt Options,
	rep *Report, names []string, consensus map[string]int) ([]Item, map[string]int) {
	// 种子：matched 条目的高频番名（优先最长汉字串，4~28 字；无汉字才用假名串）
	var seeds []string
	seenSeed := map[string]bool{}
	pushSeed := func(show string) {
		run := longestRun(show, isHanRune)
		if utf8.RuneCountInString(run) < 4 {
			run = longestRun(show, isCJKishRune) // 回退：含假名
		}
		n := utf8.RuneCountInString(run)
		if n < 4 || n > 28 {
			return
		}
		key := Normalize(run)
		if seenSeed[key] {
			return
		}
		seenSeed[key] = true
		seeds = append(seeds, run)
	}
	for _, it := range rep.Items {
		if it.Level == "low" || it.Show == "" {
			continue
		}
		pushSeed(it.Show)
		if len(seeds) >= 4 {
			break
		}
	}
	// 目标名本身不重复搜
	inQueries := map[string]bool{}
	for _, q := range rep.Queries {
		inQueries[Normalize(q)] = true
	}
	added := 0
	for _, s := range seeds {
		if ctx.Err() != nil {
			break
		}
		if inQueries[Normalize(s)] {
			continue
		}
		url := site.URL(s)
		raw, err := f.get(ctx, url)
		if err != nil {
			rep.Notes = append(rep.Notes, fmt.Sprintf("种子扩展失败 %q: %v", s, err))
			continue
		}
		rep.Queries = append(rep.Queries, s)
		n := 0
		for _, r := range raw {
			if n >= opt.PerQueryCap {
				break
			}
			key := strings.TrimSpace(r.Link)
			if key == "" {
				key = "t:" + strings.TrimSpace(r.Title)
			}
			exists := false
			for _, old := range rep.Items {
				if old.Link == r.Link && r.Link != "" {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			p := rss.ParseTitle(r.Title)
			rep.Items = append(rep.Items, Item{
				Title:     r.Title,
				Link:      r.Link,
				PubTime:   parsePub(r.PubDate),
				Query:     s,
				Group:     p.Group,
				Show:      p.Show,
				Episode:   p.Episode,
				EpisodeTo: p.EpisodeTo,
				Quality:   p.Quality,
				Spec:      p.Spec,
			})
			n++
			added++
		}
		if added >= opt.PerQueryCap { // 总增量上限，防止种子滚雪球
			break
		}
	}
	if added > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("种子扩展新增 %d 条（%d 个新关键词）", added, len(seeds)))
	}
	return rep.Items, consensus
}

// longestRun 取串中最长的"谓词连续段"。
func longestRun(s string, pred func(rune) bool) string {
	best := ""
	run := []rune(nil)
	flush := func() {
		if len(run) > utf8.RuneCountInString(best) {
			best = string(run)
		}
		run = nil
	}
	for _, r := range s {
		if pred(r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	return best
}

// isHanRune 汉字（不含假名/韩文）。
func isHanRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

// lineGroup 一个流派（此处以组聚合，变体标签由最新条目生成）。
type lineGroup struct {
	group   string
	score   float64 // 组内最高归属分
	items   []Item
	lastPub time.Time
}

// level 流派置信度（按组内最高归属分）。
func (l *lineGroup) level() string {
	switch {
	case l.score >= 0.60:
		return "high"
	case l.score >= 0.40:
		return "medium"
	default:
		return "low"
	}
}

func parsePub(s string) time.Time {
	n := rss.NormalizePubDate(s)
	for _, layout := range []string{time.RFC3339, time.RFC1123, time.RFC1123Z} {
		if t, err := time.Parse(layout, n); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// paceOf 更新节奏：按最近一条与当前时间差 + 集间中位间隔。
func paceOf(items []Item, now time.Time) string {
	if len(items) == 0 {
		return "无数据"
	}
	var ts []time.Time
	for _, it := range items {
		if !it.PubTime.IsZero() {
			ts = append(ts, it.PubTime)
		}
	}
	if len(ts) == 0 {
		return "无时间"
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	last := ts[len(ts)-1]
	idle := now.Sub(last).Hours() / 24
	if int(idle) > 60 {
		return fmt.Sprintf("停更 %d 天", int(idle))
	}
	median := 7.0
	if len(ts) >= 3 {
		gaps := make([]float64, 0, len(ts)-1)
		for i := 1; i < len(ts); i++ {
			gaps = append(gaps, ts[i].Sub(ts[i-1]).Hours()/24)
		}
		sort.Float64s(gaps)
		median = gaps[len(gaps)/2]
	}
	switch {
	case median < 3:
		return "日更"
	case median < 14:
		return "周更"
	default:
		return "慢更"
	}
}

func lastItem(items []Item) Item {
	best := items[0]
	for _, it := range items {
		if it.PubTime.After(best.PubTime) {
			best = it
		}
	}
	return best
}

func modeShows(items []Item) []string {
	count := map[string]int{}
	var order []string
	for _, it := range items {
		s := strings.TrimSpace(it.Show)
		if s == "" {
			continue
		}
		if count[s] == 0 {
			order = append(order, s)
		}
		count[s]++
	}
	// 按频次降序（同频保持首见序）
	sort.SliceStable(order, func(i, j int) bool { return count[order[j]] < count[order[i]] })
	return order
}

func modeString(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
