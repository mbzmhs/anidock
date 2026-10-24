package rss

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Item 一条 RSS/Atom 条目（解析后归一化）。
type Item struct {
	Title       string
	Link        string
	Description string
	PubDate     string // 原始字符串（RFC1123 / RFC3339）
	Enclosure   string // <enclosure url>（种子/资源下载页，可空）
	EncType     string // <enclosure type>（MIME，如 application/zip / video/x-matroska）
	Guid        string // <guid>（部分 feed 无 enclosure：它是详情页，link 反而是资源直链；可空）
}

// Parsed 标题解析结果（DESIGN §15.3）。
type Parsed struct {
	Group      string   // 制作组（【...】前缀，无则回退 feed 名）
	Show       string   // 去掉组名/集数/规格后的番剧主干
	Episode    *float64 // 解析出的集号；合集区间时为区间起点
	EpisodeTo  *float64 // 合集区间终点（非空 = 多文件合集种子，如 01-12）
	EpisodeRaw string   // 原始集数文本（如 "第12集" / "01-12"）
	Quality    string   // 画质词（4K/1080P/...）
	Spec       string   // 全部规格词（"4K HEVC 简繁"）
}

var (
	reGroup = regexp.MustCompile(`^[\[【]\s*(?P<g>[^\]】]{1,40}?)\s*[\]】]`)
	// 各集数模式统一容忍版本后缀 v2/v3（重压版，如 "[01v2]" / "show - 01v2 [1080P]" / "EP01v2"）：
	// 只取基础集号（v2 与 v1 同集，(anime,episode,group) 唯一索引下新版本覆盖旧版本），
	// 版本标记进 Spec 供展示。
	reEp1 = regexp.MustCompile(`(?i)第\s*(?P<n>\d+(?:\.\d+)?)(?:\s*v\d+)?\s*[话集話](?:\s*v\d+)?`)
	reEp2 = regexp.MustCompile(`(?i)\bEP(?:ISODE)?\.?\s*(\d+(?:\.\d+)?)(?:v\d+)?\b`)
	// "S01E01 / S2E5" 型（S 号是季数、E 号是集数，常见于原放组）：
	// 只取 E 号作集数；季数由 seasonSet 另行提取。
	reEpSE = regexp.MustCompile(`(?i)\bS0?(\d{1,2})E(\d{1,3}(?:\.\d+)?)\b`)
	reEp3  = regexp.MustCompile(`(?i)\[\s*(?P<n>\d{1,3}(?:\.\d+)?)(?:v\d+)?\s*\]`)
	reEp4  = regexp.MustCompile(`(?i)\s-\s(?P<n>\d{1,3}(?:\.\d+)?)(?:v\d+)?\s-\s`)
	// "show - 02 [1080P]..." 型：破折号 + 集号 + 后随空格/行尾；
	// 版本后缀可紧贴（01v2）或空格分隔（01 v2）
	reEp6 = regexp.MustCompile(`(?i)\s-\s(\d{1,3}(?:\.\d+)?)(?:v\d+|\s+v\d+)?\s|\s-\s(\d{1,3}(?:\.\d+)?)(?:v\d+|\s+v\d+)?$`)
	reVer = regexp.MustCompile(`(?i)v\d+`)

	// ---------- 合集区间（多文件种子，如 "01-12" / "第1~12话" / "EP01-12" / "全12话"） ----------
	// 区间判定先于单值：否则 "EP01-12" 会被单值规则吃掉开头（EP01），
	// "[01-12]" 因括号内非纯数字而整体落空（旧版即因此把合集当成集数未知）。
	// 区间以起点/终点两个集号表达，不再折叠成中点（旧版 "01~12"→6.5 是假集号，
	// 会与真实单集在唯一键上冲突、污染候选集数统计）。
	epRangeSep = `[-—–~～〜－至]`

	// 第1-12话 / 第01~12集（含全角分隔与"至"）
	reEpR1 = regexp.MustCompile(`第\s*(\d{1,3}(?:\.\d+)?)\s*(?:v\d+)?\s*` + epRangeSep + `\s*(\d{1,3}(?:\.\d+)?)\s*(?:v\d+)?\s*[话集話]`)
	// EP01-12 / Episode 1-12
	reEpR2 = regexp.MustCompile(`(?i)\bEP(?:ISODE)?\.?\s*(\d{1,3}(?:\.\d+)?)\s*` + epRangeSep + `\s*(\d{1,3}(?:\.\d+)?)`)
	// [01-12]
	reEpR3 = regexp.MustCompile(`\[\s*(\d{1,3}(?:\.\d+)?)\s*(?:v\d+)?\s*` + epRangeSep + `\s*(\d{1,3}(?:\.\d+)?)\s*(?:v\d+)?\s*\]`)
	// 全12话 → 1~12（"全" 前缀的合集总数写法）
	reEpAll = regexp.MustCompile(`全\s*(\d{1,3})\s*[话集話]`)
	// 裸区间 "01-12"：前后必须是显式分隔（空格/开括号/顿逗等）——
	// 排除 "-" 和 "." 作前导（否则日期串 "2026-01-15" 里的 "01-15" 会误判成区间）、
	// 排除拉丁字母前导（"x265-10bit"、"Vol 1-12" 的左数字带字母前缀）；
	// 尾部排除 \w 与 "."（避免吃到更长数字/版本串），另排除 季/期/部（"第1-2季" 是季区间不是集区间）。
	reEpR4 = regexp.MustCompile(`(?:^|[\s（(【\[>,，、|·第])(\d{1,3}(?:\.\d+)?)\s*` + epRangeSep + `\s*(\d{1,3}(?:\.\d+)?)(?:$|[^\w.季期部])`)

	reQuality     = regexp.MustCompile(`(?i)\b(2160p|4k|1080p|720p|540p|480p|bluray|bd|web-?dl|webrip|hdtv)\b`)
	reSpecs       = regexp.MustCompile(`(?i)(2160p|4k|1080p|720p|540p|480p|x265|x264|hevc|h\.?265|h\.?264|av1|web-?dl|webrip|bluray|bd-?remux|hdtv|aac|flac|opus|简繁|简体|繁体|内嵌|外挂|简|繁|OP|ED|10bit)`)
	reBracket     = regexp.MustCompile(`\[[^\]]*\]`)
	reFullBracket = regexp.MustCompile(`【[^】]*】`)
)

// isCJKishRune 汉字/假名/韩文（方括号保留判定用）。
func isCJKishRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:
		return true
	case r >= 0x3400 && r <= 0x4DBF:
		return true
	case r >= 0x3040 && r <= 0x30FF:
		return true
	case r >= 0xAC00 && r <= 0xD7AF:
		return true
	}
	return false
}

// ParseTitle 按序解析：制作组 → 集数 → 规格词 → 剩余 = 番剧主干。
func ParseTitle(title string) Parsed {
	p := Parsed{}
	t := strings.TrimSpace(title)

	// 1) 制作组
	if m := reGroup.FindStringSubmatchIndex(t); m != nil {
		// m: [matchStart matchEnd groupStart groupEnd]
		p.Group = strings.TrimSpace(t[m[2]:m[3]])
		t = strings.TrimSpace(t[m[1]:])
	}

	// 2) 集数：先试合集区间（多文件种子），再按单值规则取第一个命中
	verTag := "" // 版本标记（v2/v3 重压版），随规格词一起展示
	setEp := func(n string, raw string) {
		v := parseNum(n)
		p.Episode = &v
		p.EpisodeRaw = raw
		// raw 是整段集数表达式（如 "[01v2]" / " - 01 v2 "），其中的 v\d+ 只可能是版本标记
		verTag = strings.ToLower(reVer.FindString(raw))
	}
	// setRange 合集区间：Episode=起点、EpisodeTo=终点；倒置区间（12-01）视为无效
	setRange := func(n1, n2, raw string) bool {
		v1, v2 := parseNum(n1), parseNum(n2)
		if v2 <= v1 {
			return false
		}
		p.Episode, p.EpisodeTo, p.EpisodeRaw = &v1, &v2, raw
		verTag = strings.ToLower(reVer.FindString(raw))
		return true
	}
	// volumePreceded：区间左端紧邻 Vol/卷 标记 → 是卷号区间不是集数区间（"Vol 1-12"）
	volumePreceded := func(t string, idx int) bool {
		pre := strings.TrimSpace(t[:idx])
		low := strings.ToLower(pre)
		return strings.HasSuffix(low, "vol") || strings.HasSuffix(low, "vol.") || strings.HasSuffix(pre, "卷")
	}
	if m := reEpR1.FindStringSubmatch(t); m != nil && setRange(m[1], m[2], m[0]) {
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEpR2.FindStringSubmatch(t); m != nil && setRange(m[1], m[2], m[0]) {
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEpR3.FindStringSubmatch(t); m != nil && setRange(m[1], m[2], m[0]) {
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEpAll.FindStringSubmatch(t); m != nil {
		if n, err := strconv.ParseFloat(m[1], 64); err == nil && n > 1 {
			one := 1.0
			p.Episode, p.EpisodeTo, p.EpisodeRaw = &one, &n, m[0]
		} else {
			setEp(m[1], m[0])
		}
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEpR4.FindStringSubmatchIndex(t); m != nil && !volumePreceded(t, m[0]) && setRange(t[m[2]:m[3]], t[m[4]:m[5]], t[m[0]:m[1]]) {
		t = strings.Replace(t, t[m[0]:m[1]], " ", 1)
	} else if m := reEp1.FindStringSubmatch(t); m != nil {
		setEp(m[1], m[0])
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEpSE.FindStringSubmatch(t); m != nil {
		setEp(m[2], m[0])
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEp2.FindStringSubmatch(t); m != nil {
		setEp(m[1], m[0])
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEp3.FindStringSubmatch(t); m != nil {
		setEp(m[1], m[0])
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEp4.FindStringSubmatch(t); m != nil {
		setEp(m[1], m[0])
		t = strings.Replace(t, m[0], " ", 1)
	} else if m := reEp6.FindStringSubmatch(t); m != nil {
		n := m[1]
		if n == "" {
			n = m[2]
		}
		setEp(n, m[0])
		t = strings.Replace(t, m[0], " ", 1)
	}

	// 3) 规格词
	specs := reSpecs.FindAllString(t, -1)
	if qm := reQuality.FindString(t); qm != "" {
		p.Quality = strings.ToLower(qm)
	}
	sort.Strings(specs)
	p.Spec = strings.Join(specs, " ")
	if verTag != "" { // "01v2" 型重压版：版本标记进规格词
		p.Spec = strings.TrimSpace(p.Spec + " " + strings.ToLower(verTag))
	}
	for _, sp := range specs {
		t = strings.Replace(t, sp, " ", 1)
	}
	// 去掉剩余的 [xxx] 标签：数字开头（集数/宣传词）或不含汉字/假名（纯规格词）的剔除；
	// 含汉字的方括号保留——不少组把番名写进方括号（"[字幕组E][番剧G 第二季 / 番剧H 第二季]"），
	// 无差别剔除会把番名主干吃掉，导致整组条目无法归属。
	t = reBracket.ReplaceAllStringFunc(t, func(s string) string {
		r := []rune(s)
		if len(r) < 3 {
			return " "
		}
		if r[1] >= '0' && r[1] <= '9' {
			return " "
		}
		for _, c := range r[1 : len(r)-1] {
			if isCJKishRune(c) {
				return s
			}
		}
		return " "
	})
	t = reFullBracket.ReplaceAllString(t, " ")
	// 保留括号内容（番名）后，清掉残留的括号边界符，避免 "] [" 碎片混入主干
	t = strings.ReplaceAll(t, "[", " ")
	t = strings.ReplaceAll(t, "]", " ")

	// 4) 剩余 = 番剧主干
	p.Show = squeeze(t)
	return p
}

// ParseLinks 从 link/description 提取详情页与磁力/直链。
func ParseLinks(link, description string) (detailURL, downloadURL string) {
	detailURL = strings.TrimSpace(link)
	if m := reMagnet.FindString(description); m != "" {
		downloadURL = strings.TrimSpace(m)
	} else if m := reDirect.FindString(description); m != "" {
		downloadURL = strings.TrimSpace(m)
	}
	return
}

var (
	reMagnet = regexp.MustCompile(`magnet:\?[^<>"\s\]]+`)
	reDirect  = regexp.MustCompile(`https?://[^\s<>"\]]+\.(?:mp4|mkv|torrent)(?:\?[^\s<>"\]]*)?`)
)

func parseNum(s string) float64 {
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

// squeeze 去多余空白与常见连接符。
func squeeze(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f)
	}
	return strings.Join(out, " ")
}
