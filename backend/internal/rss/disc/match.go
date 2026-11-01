package disc

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"anidock/backend/internal/rss"
)

// ---------- 归一化 ----------

var reWS = regexp.MustCompile(`\s+`)

// Normalize 匹配用归一：繁→简（rss 包 OpenCC 全表）、全角→半角、小写、空白压缩。
func Normalize(s string) string {
	s = rss.Simplify(s)
	s = rss.HalfWidth(s)
	s = strings.ToLower(s)
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ---------- 名称相似度 ----------

// nameUnits 切匹配单元：CJK/假名连续串 → 字符 bigram；拉丁 → 单词(len>=2)。
// 返回 (CJK 单元集, 拉丁单元集)。
func nameUnits(s string) (map[string]struct{}, map[string]struct{}) {
	cjk := map[string]struct{}{}
	lat := map[string]struct{}{}
	run := []rune(nil)
	flush := func() {
		if len(run) == 0 {
			return
		}
		w := strings.ToLower(string(run))
		if isCJKish(w) {
			for i := 0; i+1 < len(run); i++ {
				cjk[string(run[i:i+2])] = struct{}{}
			}
			if len(run) == 1 {
				cjk[w] = struct{}{}
			}
		} else if len(run) >= 2 {
			lat[w] = struct{}{}
		}
		run = nil
	}
	for _, r := range s {
		if isCJKishRune(r) || unicode.IsLetter(r) || unicode.IsDigit(r) {
			if isCJKishRune(r) != (len(run) > 0 && isCJKishRune(run[len(run)-1])) && len(run) > 0 {
				flush()
			}
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	return cjk, lat
}

// overlap 两单元集重叠度 |A∩B| / min(|A|,|B|)（min 取向：短名被长名包含时得高分）。
func overlap(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, big := a, b
	if len(big) < len(small) {
		small, big = big, small
	}
	hit := 0
	for u := range small {
		if _, ok := big[u]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(small))
}

// NameScore 兼容旧接口的整体重叠度（内部先做归一）。
func NameScore(a, b string) float64 {
	ca, la := nameUnits(Normalize(a))
	cb, lb := nameUnits(Normalize(b))
	return max0(overlap(ca, cb), overlap(la, lb))
}

func max0(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// anchoredScore 归属判定的名称分（比 NameScore 更严）：
// 双方都含 CJK 时只认 CJK 重叠，防止同前缀拉丁名互相撞上；任一方纯拉丁才允许拉丁兜底。
func anchoredScore(a, b string) float64 {
	ca, la := nameUnits(Normalize(a))
	cb, lb := nameUnits(Normalize(b))
	if len(ca) > 0 && len(cb) > 0 {
		return overlap(ca, cb)
	}
	return max0(overlap(ca, cb), overlap(la, lb))
}

// ---------- 播出窗口 ----------
// AiringWindow 实现移至 rss 包（season.go），与入库侧旧季防线共用。

func windowScore(pub time.Time, ws, we time.Time) float64 {
	if pub.IsZero() {
		return 0
	}
	if pub.After(ws) && pub.Before(we) {
		return 1
	}
	if pub.After(ws.AddDate(0, 0, -21)) { // 播前预热（提前发布/预告资源）
		return 0.7
	}
	return 0
}

// ---------- 季词信号（番剧的"第 N 季"，与日历季度无关） ----------
//
// 注意：标题里的「第二季 / S02 / Part 2」指的是**该番剧的续作季数**
// （日历 Q4 但番剧 S2 是常见情况），
// 所以必须拿"目标标题隐含的季数"对比，而不是目标 quarter。
// 季数提取统一走 rss.SeasonSet（与入库侧一致，含罗马数字/部/两/Part）。

// showSeason 从名字中提取番剧季数（多个时取最大）。
// 显式出现季词（含"第一季"）时 found=true；未出现时返回 (1, false)：
// 默认按第一季处理，但不用该信号做否决。
// 名字内出现 ≥2 个不同季数（"第一季+第二季"）时 multi=true：跨季合集包，
// 不属于任何单季，由 seasonSignal 否决。
func showSeason(s string) (max int, found bool, multi bool) {
	set := rss.SeasonSet(s)
	if len(set) == 0 {
		return 1, false, false
	}
	max = 1
	for n := range set {
		if n > max {
			max = n
		}
	}
	return max, true, len(set) > 1
}

// targetShowSeason 目标隐含的番剧季数（标题+别名，缺省 1）。
func targetShowSeason(t Target) int {
	want := 1
	for _, s := range append([]string{t.Title}, t.Aliases...) {
		if n, ok, _ := showSeason(s); ok && n > want {
			want = n
		}
	}
	return want
}

// seasonWordFrom 番剧季数 → 中文季词（S1 无词）。
func seasonWordFrom(n int) string {
	if n <= 1 {
		return ""
	}
	return "第" + cnInvNum[n] + "季"
}

var cnInvNum = map[int]string{2: "二", 3: "三", 4: "四", 5: "五", 6: "六", 7: "七", 8: "八", 9: "九", 10: "十"}

// showSeasonWord 目标标题隐含季数 → 搜索关键词用季词（如 "第二季"）。
func showSeasonWord(t Target) string {
	return seasonWordFrom(targetShowSeason(t))
}

// seasonSignal 季词信号：
//   +1  条目季词与目标季数一致（强匹配）
//    0  条目无季词（不置否）
//   -1  条目季词与目标不同（判定为其它季，硬否决）；
//       含 ≥2 个不同季数的跨季合集包（"第一季+第二季"）同样否决——
//       取最大季数恰好等于目标季数时会被误判 +1，但它不属于任何单季
func seasonSignal(title string, t Target) int {
	is, iok, multi := showSeason(title)
	if multi {
		return -1
	}
	if !iok {
		return 0
	}
	if is == targetShowSeason(t) {
		return 1
	}
	return -1
}

// ---------- 归属打分 ----------

// targetNames 目标的名称集（归一化后）。
func targetNames(t Target) []string {
	var names []string
	push := func(s string) {
		s = Normalize(s)
		if s != "" {
			names = append(names, s)
		}
	}
	push(t.Title)
	push(t.TitleOrig)
	for _, a := range t.Aliases {
		push(a)
	}
	// 去季词变体（资源标题常不带季词）
	var noSeason []string
	for _, n := range names {
		s := strings.TrimSpace(reSeason.ReplaceAllString(n, " "))
		s = reWS.ReplaceAllString(s, " ")
		if s != "" && s != n {
			noSeason = append(noSeason, s)
		}
	}
	return append(names, noSeason...)
}

// consensus 跨组共识：同一番名被多少个不同组使用。
func consensusOf(items []Item) map[string]int {
	g := map[string]map[string]bool{}
	for _, it := range items {
		key := Normalize(it.Show)
		if key == "" || it.Group == "" {
			continue
		}
		if g[key] == nil {
			g[key] = map[string]bool{}
		}
		g[key][it.Group] = true
	}
	out := map[string]int{}
	for k, groups := range g {
		out[k] = len(groups)
	}
	return out
}

// attribute 对单条条目做归属打分并写回 it.Score / it.Level。
//
// 信号权重：名称重叠 0.45 + 播出窗口 0.40 + 季词 0.15；
// 跨组共识（≥2 组同名）可把 medium 提升为 high；
// 在播目标的窗口内 BDrip 视为旧季内容（窗口分清零 + 窗口外降权）。
func attribute(it *Item, t Target, names []string, consensus map[string]int, now time.Time) {
	if isMusicSingle(it.Title) { // 单曲/OST 直接排除（JMAX 主题歌之类）
		it.Score, it.Level = 0, "low"
		return
	}
	ws, we, hasWin := rss.AiringWindow(t.Year, t.Quarter)

	ns := 0.0
	for _, n := range names {
		if s := anchoredScore(it.Show, n); s > ns {
			ns = s
		}
	}
	if ns < 0.25 { // 名称完全对不上，不再看其它信号
		it.Score, it.Level = 0, "low"
		return
	}

	wsScore := 0.0
	if hasWin {
		wsScore = windowScore(it.PubTime, ws, we)
		// 目标仍在播出期内（now 早于窗口末端）时，窗口内出现的 BDrip/Blu-ray
		// 不可能是本季内容（BD 制作周期远长于一个季度）——典型场景：旧季 BD
		// 恰在新季播出期间上市（旧季 BD 逢新季播出期发行的常见场景）。完结目标则不降权。
		if wsScore > 0 && now.Before(we) && isBDrip(it.Title+" "+it.Spec) {
			wsScore = 0
		}
	}
	sig := seasonSignal(it.Title, t)
	if sig == -1 { // 明确的其它季 → 否决（名称已对上，标记为"仅季词冲突"，回验纯度据此豁免）
		it.Score, it.Level = 0, "low"
		it.SeasonReject = true
		return
	}

	score := 0.45*ns + 0.40*wsScore + 0.15*float64(sig)
	if hasWin && wsScore == 0 {
		score *= 0.5 // 播出窗口外（旧季/同名他番）显著降权
	}
	if consensus[Normalize(it.Show)] >= 2 {
		score += 0.10
	}
	it.Score = score

	switch {
	case score >= 0.60:
		it.Level = "high"
	case score >= 0.40:
		it.Level = "medium"
		if consensus[Normalize(it.Show)] >= 2 {
			it.Level = "high"
		}
	default:
		it.Level = "low"
	}
}

// isBDrip 标题/规格里是否含 BDrip/Blu-ray 类字样。
func isBDrip(s string) bool {
	u := strings.ToUpper(s)
	return strings.Contains(u, "BDRIP") || strings.Contains(u, "BLU")
}

// isMusicSingle 是否单曲/OST 条目（OP/ED 主题歌）——音乐资源不含集数，
// 不应成为订阅候选。要求"音频规格 + 主题字样"双条件，避免误伤正片。
func isMusicSingle(s string) bool {
	u := strings.ToUpper(s)
	audio := strings.Contains(u, "FLAC") || strings.Contains(u, "MP3") ||
		strings.Contains(u, "320K") || strings.Contains(u, "48KHZ") || strings.Contains(u, "24BIT")
	theme := strings.Contains(s, "主題歌") || strings.Contains(s, "主题曲") ||
		strings.Contains(s, "主题歌") || strings.Contains(s, "テーマ") ||
		strings.Contains(u, "THEME SONG") || strings.Contains(u, "OP THE") ||
		strings.Contains(u, "ED THE") || strings.Contains(u, "INSERT SONG")
	return audio && theme
}

// ---------- 流派标签 ----------

// variantTags 从标题/规格词提取流派标签（简繁 / 无修 / Raw / 规格）。
func variantTags(title, spec, quality string) string {
	var tags []string
	t := title + " " + spec
	switch {
	case strings.Contains(t, "简繁") || strings.Contains(t, "简／繁") || strings.Contains(t, "简/繁") ||
		strings.Contains(t, "[CHS][CHT]") || strings.Contains(t, "[CHT][CHS]"):
		tags = append(tags, "简繁")
	case strings.Contains(t, "简") || strings.Contains(t, "CHS"):
		tags = append(tags, "简")
	case strings.Contains(t, "繁") || strings.Contains(t, "CHT"):
		tags = append(tags, "繁")
	}
	switch {
	case strings.Contains(t, "无修") || strings.Contains(t, "无删减") || strings.Contains(strings.ToUpper(t), "UNCUT"):
		tags = append(tags, "无修")
	case strings.Contains(strings.ToUpper(t), " RAW") || strings.Contains(t, "[Raw]") || strings.Contains(t, "Raws"):
		tags = append(tags, "Raw")
	}
	if q := strings.ToLower(strings.TrimSpace(quality)); q != "" {
		tags = append(tags, q)
	}
	return strings.Join(tags, " · ")
}

// variantLine 流派标签取整条流派（组内全部条目）的并集——
// 同一组可能简繁双发/后期才加无修，只看最新一条会漏标（如标成"简"实际是"简繁"）。
func variantLine(items []Item) string {
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		for _, tag := range strings.Split(variantTags(it.Title, it.Spec, it.Quality), " · ") {
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return strings.Join(out, " · ")
}
