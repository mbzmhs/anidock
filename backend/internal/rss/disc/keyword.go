package disc

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"anidock/backend/internal/rss"
)

var (
	reBraces    = regexp.MustCompile(`\{[^}]*\}`)
	reParens    = regexp.MustCompile(`\([^)]*\)`)
	reSlashPart = regexp.MustCompile(`\s+/+\s+`)
)

// showCore 从组的番名写法里提炼**核心短词**：
// 去掉 {...} 原名块、(…)/(…) 规格备注、" / " 后的外语备选名，再取最长汉字串。
// 站点关键词搜索对长串（含 /()）会 404，且长词命中面窄，
// 所以订阅关键词用「核心短词 + 组名」，回验通过才算数。
func showCore(show string) string {
	s := reBraces.ReplaceAllString(show, " ")
	s = reParens.ReplaceAllString(s, " ")
	s = reSlashPart.Split(s, 2)[0]
	s = strings.Join(strings.Fields(s), " ")
	if run := longestRun(s, isHanRune); utf8.RuneCountInString(run) >= 4 {
		return run
	}
	if strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(show)
}

// BuildKeywords 为「组 × 番」构造候选订阅关键词（最可靠 → 最兜底）。
// 首选「该组核心番名 + 组名」（实测 100% 纯净）。
// feed 名常是多别名结构，每个别名段各自提炼核心词作为候选；完整写法仅作末位兜底（长串易 404）。
func BuildKeywords(showOwn, seasonW, group string) []string {
	showOwn = strings.TrimSpace(showOwn)

	// 按 " / " 切别名段（主写法在前），每段提炼核心词
	var cores []string
	seenCore := map[string]bool{}
	for _, seg := range reSlashPart.Split(showOwn, -1) {
		c := showCore(seg)
		if c == "" || seenCore[c] {
			continue
		}
		seenCore[c] = true
		cores = append(cores, c)
		if len(cores) >= 3 {
			break
		}
	}
	if len(cores) == 0 {
		cores = []string{showOwn}
	}

	var cands []string
	push := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || s == group {
			return
		}
		for _, c := range cands {
			if c == s {
				return
			}
		}
		cands = append(cands, s)
	}
	withSeason := func(base string) {
		if seasonW == "" || strings.Contains(base, seasonW) {
			push(joinNonEmpty(base, group))
		} else {
			push(joinNonEmpty(base, seasonW, group))
			push(joinNonEmpty(base, group))
		}
	}
	for _, c := range cores {
		withSeason(c)
		// 核心词自带季词时再去一版（feed 名可能不带季词）
		strip := strings.TrimSpace(reSeason.ReplaceAllString(c, " "))
		strip = strings.Join(strings.Fields(strip), " ")
		if strip != "" && strip != c {
			withSeason(strip)
		}
	}
	if showOwn != cores[0] {
		// 兜底：组的完整写法（feed 名与之完全一致时可用）
		if seasonW == "" || !strings.Contains(showOwn, seasonW) {
			push(joinNonEmpty(showOwn, seasonW, group))
		}
		push(joinNonEmpty(showOwn, group))
	}
	return cands
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// verifyURL 回验一个候选订阅地址：
//   纯度 = feed 中「组名一致 ∧ 归属目标番」的条目占比（混组会拉低；
//     组名一致但仅季词冲突的旧季条目视为无害——feed 是 per-show 源，
//     抓取侧有季词冲突过滤，不会入库）
//   覆盖 = 宽搜已知集数被该 feed 覆盖的比例
//   eps  = 该 feed 中通过条目解析出的集数（升序去重，供候选补全集数）
//   抓取失败/空 feed 时返回 err（调用方据此与"纯度低"区分）
func verifyURL(ctx context.Context, f *fetcher, site Site, t Target,
	group, url string, expectedEps map[int]struct{}, consensus map[string]int, now time.Time) (purity, coverage float64, sample string, eps []int, err error) {
	items, err := f.get(ctx, url)
	if err != nil {
		return 0, 0, "", nil, err
	}
	if len(items) == 0 {
		return 0, 0, "", nil, fmt.Errorf("feed 无条目")
	}
	names := targetNames(t)
	pass, total := 0, len(items)
	epGot := map[int]struct{}{}
	var best time.Time
	var bestTitle string
	for _, r := range items {
		p := rss.ParseTitle(r.Title)
		g := p.Group
		if g == "" {
			g = group // 该站 feed 内条目可能无组前缀，按 feed 归属
		}
		it := Item{
			Title: r.Title, Link: r.Link,
			PubTime: parsePub(r.PubDate),
			Group:   g, Show: p.Show,
			Episode: p.Episode, Quality: p.Quality, Spec: p.Spec,
		}
		attribute(&it, t, names, consensus, now)
		if normGroupEq(g, group) && (it.Level != "low" || it.SeasonReject) {
			pass++
			if it.SeasonReject {
				continue // 旧季条目只算纯度无害，集数/示例不进本季节数集
			}
			if e := intEps(p.Episode); e > 0 {
				epGot[e] = struct{}{}
			}
			if it.PubTime.After(best) {
				best, bestTitle = it.PubTime, r.Title
			}
		}
	}
	purity = float64(pass) / float64(total)
	if len(expectedEps) == 0 {
		coverage = 1
	} else {
		hit := 0
		for e := range expectedEps {
			if _, ok := epGot[e]; ok {
				hit++
			}
		}
		coverage = float64(hit) / float64(len(expectedEps))
	}
	eps = make([]int, 0, len(epGot))
	for e := range epGot {
		eps = append(eps, e)
	}
	sort.Ints(eps)
	return purity, coverage, bestTitle, eps, nil
}

// hostOf URL 的 host 部分（回验异常摘要用，避免 note 里堆整串转义 URL）。
func hostOf(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return u
	}
	host := u[i+3:]
	if j := strings.IndexByte(host, '/'); j > 0 {
		host = host[:j]
	}
	return host
}

func normGroupEq(a, b string) bool {
	na, nb := Normalize(a), Normalize(b)
	if na == nb {
		return true
	}
	// 联名署名：条目组名是拼接形（"字幕组C&字幕组A"），其中任一段
	// 与候选组名一致即视为同组（字幕组A 隶属字幕组D，旧季常用联名署名）
	return groupSegmentHit(na, nb) || groupSegmentHit(nb, na)
}

// groupSegmentHit seg（联名组名，可能由 &/＆/和/与/×/、/、/ 拼接）中存在
// 与 full 完全相等的独立段。按段精确匹配而非子串，避免 "字幕组A" 误配 "字幕组A2"。
func groupSegmentHit(seg, full string) bool {
	if seg == "" || full == "" {
		return false
	}
	for _, part := range strings.FieldsFunc(seg, func(r rune) bool {
		return r == '&' || r == '＆' || r == '和' || r == '与' || r == '×' || r == '、' || r == '/'
	}) {
		if strings.TrimSpace(part) == full {
			return true
		}
	}
	return false
}

func intEps(e *float64) int {
	if e == nil {
		return 0
	}
	n := int(*e)
	if n < 0 || n > 200 {
		return 0
	}
	return n
}

// searchOptimal 按候选顺序回验，选「纯度 100% 且覆盖最高」的地址；
// 若无纯地址，取纯度/覆盖最优者并标注警告。note 汇总回验中的异常
// （host: 原因 摘要，去重、超长截断，避免堆整串 URL）。
// 返回值含站点无关的关键词 kw（候选的身份）与最优 feed 的集数 eps
// （与宽搜集数取并集后作为候选的完整集数）。
func searchOptimal(ctx context.Context, f *fetcher, site Site, t Target,
	group, showOwn string, expectedEps map[int]struct{}, consensus map[string]int, budget int, now time.Time) (url, kw string, purity, coverage float64, sample string, eps []int, note string, tried []string) {
	kws := BuildKeywords(showOwn, showSeasonWord(t), group)
	if budget > 0 && len(kws) > budget {
		kws = kws[:budget]
	}
	type result struct {
		url    string
		kw     string
		purity float64
		cover  float64
		sample string
		eps    []int
		err    error
	}
	var results []result
	for _, k := range kws {
		u := site.URL(k)
		p, c, s, epList, err := verifyURL(ctx, f, site, t, group, u, expectedEps, consensus, now)
		results = append(results, result{u, k, p, c, s, epList, err})
		tried = append(tried, u)
		if err == nil && p >= 1 && c >= 1 {
			break // 只有"纯净且全覆盖"才提前停：覆盖过半就停会漏掉覆盖更全的变体
			// （资源组常对早期集换标题写法，如 romaji/中文混用——站点按关键词过滤 RSS，
			// 选错变体 = 订阅后静默缺集）
		}
	}
	// 排序：错误垫底；纯度 > 覆盖
	sort.SliceStable(results, func(i, j int) bool {
		ei, ej := results[i].err != nil, results[j].err != nil
		if ei != ej {
			return !ei
		}
		if ei {
			return false
		}
		if results[i].purity != results[j].purity {
			return results[i].purity > results[j].purity
		}
		return results[i].cover > results[j].cover
	})
	if len(results) == 0 {
		return "", "", 0, 0, "", nil, "", tried
	}
	best := results[0]
	if best.err != nil {
		// 全部候选地址都失败：返回空地址，由调用方标"回验无可用地址"
		return "", best.kw, 0, 0, "", nil, "", tried
	}
	// 选中地址正常时，兄弟候选的失败（长关键词 404/空 feed）属正常噪音，不进警告
	return best.url, best.kw, best.purity, best.cover, best.sample, best.eps, "", tried
}
