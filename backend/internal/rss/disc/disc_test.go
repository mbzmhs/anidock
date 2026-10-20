package disc

import (
	"testing"
	"time"
)

func TestSimplify(t *testing.T) {
	cases := map[string]string{
		"番劇B":       "番剧B",
		"番劇A 第二季": "番剧A 第二季",
		"番剧C":       "番剧C", // 简体原样
	}
	for in, want := range cases {
		if got := Simplify(in); got != want {
			t.Errorf("Simplify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize("ＴＶ版　番劇A  第二季")
	if got != "tv版 番剧a 第二季" {
		t.Errorf("Normalize = %q", got)
	}
}

func TestBuildQueries(t *testing.T) {
	qs := BuildQueries(Target{Title: "番剧A 第二季", Year: 2026, Quarter: 4})
	joined := ""
	for _, q := range qs {
		joined += q + "\n"
	}
	for _, want := range []string{"番剧A 第二季", "番剧A"} {
		if !containsStr(qs, want) {
			t.Errorf("BuildQueries 缺少 %q:\n%s", want, joined)
		}
	}
}

func TestNameScore(t *testing.T) {
	// 相同名
	if s := NameScore("番剧A", "番剧A"); s < 0.99 {
		t.Errorf("同名 NameScore = %v", s)
	}
	// 繁简差异应被归一消除
	if s := NameScore("番劇B", "番剧B"); s < 0.9 {
		t.Errorf("繁简对 NameScore = %v (应≈1)", s)
	}
}

func TestAiringWindow(t *testing.T) {
	// 2026 Q4（秋，9月开播）
	ws, we, ok := AiringWindow(2026, 4)
	if !ok {
		t.Fatal("Q4 窗口应可用")
	}
	sep := time.Date(2026, time.September, 15, 0, 0, 0, 0, time.UTC)
	dec := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	old := time.Date(2022, time.December, 1, 0, 0, 0, 0, time.UTC)
	if !sep.After(ws) || !sep.Before(we) {
		t.Errorf("2026-09-15 应在 Q4 窗口内: [%s, %s]", ws, we)
	}
	if !dec.After(ws) || !dec.Before(we) {
		t.Errorf("2026-12-01 应在 Q4 窗口内")
	}
	if windowScore(old, ws, we) != 0 {
		t.Errorf("2022 年条目应被窗口判 0")
	}
	// 冬番跨年：2026 Q1 → 2025 年 12 月开播
	ws1, _, ok1 := AiringWindow(2026, 1)
	if !ok1 || ws1.Year() != 2025 {
		t.Errorf("2026 Q1 窗口起点应在 2025 年: %v", ws1)
	}
}

func TestSeasonSignal(t *testing.T) {
	tt := Target{Title: "番剧A 第二季", Year: 2026, Quarter: 4}
	if g := seasonSignal("Show A S02 / 番剧A 第二季 - 03", tt); g != 1 {
		t.Errorf("S02 对 目标S2 = %d, want 1", g)
	}
	if g := seasonSignal("Show A - 番剧A - 12", tt); g != 0 {
		t.Errorf("无季词 = %d, want 0", g)
	}
	if g := seasonSignal("番剧A 第一季 - 05", tt); g != -1 {
		t.Errorf("第一季 对 目标S2 应否决 = %d", g)
	}
	// S1 目标（无季词）遇到 S2 条目 → 否决
	tt1 := Target{Title: "番剧C 最终章"}
	if g := seasonSignal("番剧C 第二季 - 01", tt1); g != -1 {
		t.Errorf("S1 目标遇 S2 应否决 = %d", g)
	}
	if g := seasonSignal("番剧C 最终章 第5话", tt1); g != 0 {
		t.Errorf("无季词 = %d, want 0", g)
	}
}

func TestShowSeasonWord(t *testing.T) {
	if w := showSeasonWord(Target{Title: "番剧A 第二季"}); w != "第二季" {
		t.Errorf("showSeasonWord = %q", w)
	}
	if w := showSeasonWord(Target{Title: "番剧B"}); w != "" {
		t.Errorf("S1 应无季词: %q", w)
	}
}

func TestVariantTags(t *testing.T) {
	if v := variantTags("[TV版] 番剧B - 02 [简／繁] (1080p H.264 AAC SRTx2)", "简繁 简 繁 1080p", "1080p"); v == "" {
		t.Error("应提取到变体标签")
	}
	if v := variantTags("[字幕组A] 番剧B(无修版) (UNCUT)", "", ""); v == "" {
		t.Error("无修版应提取到")
	}
}

func TestAnchoredScore(t *testing.T) {
	// 纯拉丁条目 vs 纯拉丁原名：允许拉丁兜底
	if s := anchoredScore("Show A S02", "Show A"); s < 0.99 {
		t.Errorf("同原名拉丁条对 = %v, 应≈1", s)
	}
	// 纯拉丁条目 vs 中文名：无重叠即 0（已知局限，靠混合标题条目兜住）
	if s := anchoredScore("Show A S02", "番剧A"); s > 0.1 {
		t.Errorf("纯拉丁条目对中文名 = %v, 应≈0", s)
	}
	// 繁简归一后仍走 CJK 锚定
	if s := anchoredScore("番劇B", "番剧B"); s < 0.9 {
		t.Errorf("繁简对 anchoredScore = %v", s)
	}
}

func TestBuildKeywordsAliases(t *testing.T) {
	// 多别名 feed 名：每个别名段都应有候选
	kws := BuildKeywords("番剧A 第二季 / Show A S2", "第二季", "字幕组F")
	for _, want := range []string{"番剧A 第二季 字幕组F"} {
		if !containsStr(kws, want) {
			t.Errorf("BuildKeywords 缺少 %q: %v", want, kws)
		}
	}
}

func TestBuildKeywords(t *testing.T) {
	kws := BuildKeywords("番剧A 第二季", seasonWordFrom(2), "字幕组B")
	found := false
	for _, k := range kws {
		t.Logf("keyword: %s", k)
		if k == "番剧A 第二季 字幕组B" {
			found = true
		}
	}
	if !found {
		t.Errorf("应优先尝试 组自己写法+季词+组名: %v", kws)
	}
}

func TestAttributeBaseline(t *testing.T) {
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	// 基准场景 1：跨写法 + 繁简 + 2026Q4
	tt := Target{Title: "番剧B", Year: 2026, Quarter: 4}
	names := targetNames(tt)
	it := Item{
		Title:   "[字幕组A] 番剧B(无修版) - 02 (UNCUT 1080p)",
		Show:    "番剧B 无修版",
		Group:   "字幕组A",
		PubTime: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
	}
	attribute(&it, tt, names, map[string]int{Normalize("番剧B"): 3}, now)
	if it.Level == "low" {
		t.Errorf("跨写法同番应归属成功: score=%.2f level=%s", it.Score, it.Level)
	}

	// 基准场景 2：混季否决——S1 2022 年条目对 Q4 目标
	it2 := Item{
		Title:   "[字幕组B] Show A - 番剧A - 12 [1080P]",
		Show:    "Show A 番剧A",
		Group:   "字幕组B",
		PubTime: time.Date(2022, 12, 14, 0, 0, 0, 0, time.UTC),
	}
	tt2 := Target{Title: "番剧A 第二季", TitleOrig: "Show A", Year: 2026, Quarter: 4}
	attribute(&it2, tt2, targetNames(tt2), nil, now)
	if it2.Level != "low" {
		t.Errorf("S1 2022 条目应被判 low: score=%.2f level=%s", it2.Score, it2.Level)
	}

	// 基准场景 3：S2 条目应 high
	it3 := Item{
		Title:   "[字幕组B] Show A S02 /  番剧A 第二季 - 03 [1080P]",
		Show:    "Show A S02 番剧A 第二季",
		Group:   "字幕组B",
		PubTime: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
	}
	attribute(&it3, tt2, targetNames(tt2), nil, now)
	if it3.Level != "high" {
		t.Errorf("S2 条目应 high: score=%.2f level=%s", it3.Score, it3.Level)
	}

	// 基准场景 4：在播 S2 目标遇到 S1 的 BDrip（2026-09 上市）→ 应判 low
	it4 := Item{
		Title:   "[H-Enc] 番剧A / Show A (BDRip 1080p HEVC FLAC)",
		Show:    "番剧A Show A",
		Group:   "H-Enc",
		Spec:    "bdrip",
		PubTime: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
	}
	attribute(&it4, tt2, targetNames(tt2), nil, now)
	if it4.Level != "low" {
		t.Errorf("在播目标的 BDrip 应判 low（旧季 BD）: score=%.2f level=%s", it4.Score, it4.Level)
	}
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestNormGroupEq 组名匹配：完全一致 + 联名署名（&/和/×/、/ 拼接段命中）。
func TestNormGroupEq(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"字幕组A", "字幕组A", true},
		{"字幕组A", "字幕组A ", true},
		{"字幕组a", "字幕组A", true}, // 大小写
		{"字幕组A", "字幕组C&字幕组A", true}, // 联名署名
		{"字幕组A", "字幕组A和字幕组C", true},
		{"字幕组C", "字幕组C&字幕组A", true},
		{"字幕组A", "字幕组A2", false}, // 段精确匹配，不认子串
		{"字幕组B", "字幕组A", false},
	}
	for _, c := range cases {
		if got := normGroupEq(c.a, c.b); got != c.want {
			t.Errorf("normGroupEq(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestSiteTemplate 站点池模板：{} 占位符替换 / 裸地址兼容旧形态 / 非法条目返回 nil。
func TestSiteTemplate(t *testing.T) {
	// 模板形态
	s := SiteFromEntry("https://example.org/rss-{}.xml")
	if s == nil || s.Name != "example.org" {
		t.Fatalf("SiteFromEntry(模板) = %+v, want name example.org", s)
	}
	if got, want := s.URL("番剧D 字幕组B"), "https://example.org/rss-%E7%95%AA%E5%89%A7D%20%E5%AD%97%E5%B9%95%E7%BB%84B.xml"; got != want {
		t.Errorf("Site.URL = %q, want %q", got, want)
	}
	// 裸地址（兼容旧配置）：按 <根>/rss-<关键词>.xml 形态
	s2 := SiteFromEntry("https://www.example.com/")
	if s2 == nil || s2.Name != "example.com" {
		t.Fatalf("SiteFromEntry(裸地址) = %+v, want name example.com（去 www）", s2)
	}
	if got, want := s2.URL("字幕组B"), "https://www.example.com/rss-%E5%AD%97%E5%B9%95%E7%BB%84B.xml"; got != want {
		t.Errorf("裸地址 Site.URL = %q, want %q", got, want)
	}
	// 查询串形态模板
	s3 := SiteFromEntry("https://example.net/search?q={}&type=anime")
	if s3 == nil {
		t.Fatal("查询串形态模板应为合法条目")
	}
	// 非法条目
	for _, bad := range []string{
		"ftp://example.org/x",      // 非 http(s)
		"example.org",             // 无 scheme
		"https://a.org/{}-b/{}",   // 多个占位符
		"https://a.org/{} {}-b",   // 多个占位符（跨段）
	} {
		if s := SiteFromEntry(bad); s != nil {
			t.Errorf("SiteFromEntry(%q) 应为 nil，得到 %+v", bad, s)
		}
	}
	// PoolSites 去重（同名不同写法只留一条）
	out := PoolSites("https://a.org/rss-{}.xml\nhttps://www.a.org/rss-{}.xml, https://b.org/")
	if len(out) != 2 {
		t.Fatalf("PoolSites 去重后 = %d 条, want 2", len(out))
	}
	if out[0].Name != "a.org" || out[1].Name != "b.org" {
		t.Errorf("PoolSites 名称 = %v / %v, want a.org / b.org", out[0].Name, out[1].Name)
	}
	// HostOf 展示名
	if got := HostOf("https://www.a.org/rss-x.xml"); got != "a.org" {
		t.Errorf("HostOf = %q, want a.org", got)
	}
	if got := HostOf("not a url"); got != "" {
		t.Errorf("HostOf(非法) = %q, want 空", got)
	}
}

// TestRank 自动添加组合排序：偏好层（位置）→ 集数↓ → 首发↑。
func TestRank(t *testing.T) {
	c := func(group, kw string, eps int, first string) Candidate {
		c := Candidate{Group: group, Keyword: kw, FirstPub: first}
		for i := 1; i <= eps; i++ {
			c.Episodes = append(c.Episodes, i)
		}
		return c
	}
	// 无偏好：集数多者优先，同集数看首发早者
	r := Rank([]Candidate{
		c("A", "k-a", 5, "2026-10-01"),
		c("B", "k-b", 9, "2026-10-05"),
		c("C", "k-c", 5, "2026-09-20"),
	}, nil)
	if r[0].Group != "B" || r[1].Group != "C" || r[2].Group != "A" {
		t.Fatalf("无偏好排序 = %v, want B C A", names(r))
	}
	// 有偏好：命中偏好词的按偏好位置分层靠前；同层内仍按集数/首发
	r = Rank([]Candidate{
		c("A", "k-a", 5, "2026-10-01"),   // 偏好 #1
		c("B", "k-b", 9, "2026-10-05"),   // 无偏好
		c("字幕组B组", "k-ani", 2, "2026-09-01"), // 偏好 #0（集数少仍排最前）
		c("C", "k-c", 12, "2026-09-20"), // 偏好 #1（集数多 → 同层第一）
	}, []string{"字幕组B", "C"})
	if r[0].Group != "字幕组B组" || r[1].Group != "C" || r[2].Group != "B" || r[3].Group != "A" {
		t.Fatalf("偏好排序 = %v, want 字幕组B组 C B A", names(r))
	}
	// 偏好词大小写不敏感
	r = Rank([]Candidate{c("字幕组a", "", 1, "2026-01-01"), c("X", "", 9, "2026-01-01")}, []string{"字幕组A"})
	if r[0].Group != "字幕组a" {
		t.Errorf("大小写不敏感匹配失败: %v", names(r))
	}
}

func names(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Group
	}
	return out
}

// TestBlackHit 黑名单命中：组名/关键词包含任一词（大小写不敏感）→ 排除。
func TestBlackHit(t *testing.T) {
	c := Candidate{Group: "字幕组A", Keyword: "番剧D 字幕组A"}
	if !BlackHit(c, []string{"字幕组a"}) {
		t.Error("应命中（大小写不敏感）")
	}
	if !BlackHit(c, []string{"番剧D"}) {
		t.Error("应命中关键词")
	}
	if BlackHit(c, []string{"字幕组B", "字幕组N"}) {
		t.Error("不应误伤未命中的黑名单词")
	}
	if BlackHit(c, nil) {
		t.Error("空黑名单不应排除任何候选")
	}
}
