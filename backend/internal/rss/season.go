package rss

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"anidock/backend/internal/model"
)

// ---------- 番剧季数提取（全项目统一实现） ----------
//
// disc（发现侧）与 fetcher（入库侧）共用本提取器，保证两侧对同一标题
// 判出的季数一致。此前 disc/match.go 与 rss/fetcher.go 各有一套，
// 覆盖面不同（罗马数字/部/两/Part），行为分裂。

var (
	reSeasonCN  = regexp.MustCompile(`第\s*(\d{1,2}|[一二两三四五六七八九十]+)\s*[季期部]`)
	reSeasonEn  = regexp.MustCompile(`(?i)\bseason\s?(\d{1,2})`)
	reSeasonSP  = regexp.MustCompile(`(?i)\b([SP])(\d{1,2})\b`)
	reSeasonPt  = regexp.MustCompile(`(?i)\bpart\s?(\d{1,2})`)
	reSeasonRom = regexp.MustCompile(`[ⅠⅡⅢⅣⅤⅥⅦⅧⅨⅩ]`)
)

var romNum = map[rune]int{'Ⅰ': 1, 'Ⅱ': 2, 'Ⅲ': 3, 'Ⅳ': 4, 'Ⅴ': 5, 'Ⅵ': 6, 'Ⅶ': 7, 'Ⅷ': 8, 'Ⅸ': 9, 'Ⅹ': 10}
var cnDig = map[rune]int{'一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// SeasonSet 从标题提取显式番剧季数集合。
// 空集 = 无季词（S1 惯例不写季词）；含 ≥2 个不同季数 = 跨季合集包
// （如"第一季+第二季"、"S1-S3"，不属于任何单季）。
func SeasonSet(s string) map[int]bool {
	out := map[int]bool{}
	add := func(n int) {
		if n > 0 && n < 100 {
			out[n] = true
		}
	}
	for _, m := range reSeasonCN.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			add(n)
			continue
		}
		add(cnNums(m[1]))
	}
	for _, m := range reSeasonEn.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			add(n)
		}
	}
	for _, m := range reSeasonSP.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[2]); err == nil {
			add(n)
		}
	}
	for _, m := range reSeasonPt.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			add(n)
		}
	}
	for _, m := range reSeasonRom.FindAllString(s, -1) {
		if r := []rune(m); len(r) == 1 {
			if n, ok := romNum[r[0]]; ok {
				add(n)
			}
		}
	}
	return out
}

// cnNums 中文数字（一~九十九）转 int。
func cnNums(s string) int {
	r := []rune(s)
	for i, c := range r {
		if c != '十' {
			continue
		}
		tens := 1
		if i > 0 {
			if d, ok := cnDig[r[0]]; ok {
				tens = d
			} else {
				return 0
			}
		}
		if len(r) > i+1 {
			if d, ok := cnDig[r[i+1]]; ok {
				return tens*10 + d
			}
		}
		return tens * 10
	}
	if len(r) == 1 {
		if d, ok := cnDig[r[0]]; ok {
			return d
		}
	}
	return 0
}

// ---------- 播出窗口（disc 归属打分与 fetcher 旧季防线共用） ----------

// AiringWindow 由 (年, 季度) 估算播出窗口 [开播前 21 天, 开播后 240 天]。
// 冬番(quarter=1) 跨年前后，开播点取上年 12 月 10 日。
func AiringWindow(year, quarter int) (time.Time, time.Time, bool) {
	if year <= 0 {
		return time.Time{}, time.Time{}, false
	}
	var start time.Time
	switch quarter {
	case 1:
		start = time.Date(year-1, time.December, 10, 0, 0, 0, 0, time.UTC)
	case 2:
		start = time.Date(year, time.March, 1, 0, 0, 0, 0, time.UTC)
	case 3:
		start = time.Date(year, time.June, 1, 0, 0, 0, 0, time.UTC)
	case 4:
		start = time.Date(year, time.September, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}, time.Time{}, false
	}
	return start.AddDate(0, 0, -21), start.AddDate(0, 0, 240), true
}

// ---------- 入库侧季词冲突过滤 ----------
//
// 订阅站的宽松匹配不会把季词当精确条件（实测某主流订阅站：关键词含"第三季"，
// 同番第二季的旧文件照样命中）。对绑定到具体番剧的 per-show 源，旧季文件
// 会与本季同集号文件在 (anime,episode,group) 唯一索引上互相覆盖，污染展示，
// 抓取侧必须拦一道。拦截规则（按序）：
//
//  1. 跨季合集包：条目季词含 ≥2 个不同季数（"第一季+第二季"）→ 拦；
//  2. 条目有且仅有一个季词：与番剧自身（唯一的）显式季数不同 → 拦；
//     （番剧无季词 = S1 惯例 → 不拦，沿用原有保守语义）
//  3. 条目无季词、番剧显式为第 N 季（N>1）时，按发布时间兜底：
//     S1 老文件通常不带季词，只靠季词拦不住——
//     a. 发布时间早于本季播出窗口起点 → 旧季条目，拦；
//     b. 番剧仍在播出期内且条目是 BDrip → 旧季 BD 恰在新季播出期间上市，拦
//        （本季 BD 不可能在播出期内出现，制作周期远长于一个季度）。
//
// 时间兜底对两种编号体系都成立：接续编号组（S2 从 13 话起，无季词）的本季
// 条目发布在窗口内 → 放行；其 S1 旧条目（1~12 话）发布于窗口外 → 拦截。

// AnimeSeasonSet 番剧自身（标题/原名/别名）的显式季数集合。
func AnimeSeasonSet(a *model.Anime) map[int]bool {
	want := map[int]bool{}
	add := func(s string) {
		for n := range SeasonSet(s) {
			want[n] = true
		}
	}
	add(a.Title)
	add(a.TitleOrig)
	for _, al := range a.Aliases {
		add(al)
	}
	return want
}

// seasonConflict 条目是否应被 per-show 源跳过（见上注释）。
// pub 为条目发布时间（零值 = 未知，时间兜底不生效）；now 供播出期判断。
func seasonConflict(a *model.Anime, title string, pub, now time.Time) bool {
	got := SeasonSet(title)
	if len(got) > 1 {
		return true // 跨季合集包
	}
	want := AnimeSeasonSet(a)
	if len(got) == 1 {
		if len(want) != 1 {
			return false
		}
		var w, g int
		for n := range want {
			w = n
		}
		for n := range got {
			g = n
		}
		return g != w
	}
	// 条目无季词：仅当番剧显式为第 N 季（N>1）时启用时间兜底
	if len(want) != 1 {
		return false
	}
	var w int
	for n := range want {
		w = n
	}
	if w <= 1 || pub.IsZero() {
		return false
	}
	ws, we, ok := AiringWindow(a.Year, a.Quarter)
	if !ok {
		return false
	}
	if pub.Before(ws) {
		return true // 本季开播前发布 → 旧季条目
	}
	if isBDripTitle(title) && now.Before(we) {
		return true // 播出期内的无季词 BDrip → 旧季 BD
	}
	return false
}

// isBDripTitle 标题里是否含 BDrip/Blu-ray 类字样（入库侧；disc 侧另有含 Spec 的版本）。
func isBDripTitle(s string) bool {
	u := strings.ToUpper(s)
	return strings.Contains(u, "BDRIP") || strings.Contains(u, "BLU")
}
