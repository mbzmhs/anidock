package disc

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// reSeason 季词（用于生成去季词变体）：与 rss.SeasonSet 的识别面保持一致——
// 中文数字/阿拉伯数字的"第N季/期/部"、S/P/Season/Part、罗马数字 Ⅰ~Ⅹ、新篇/续篇。
var reSeason = regexp.MustCompile(`(?i)\s*(?:第\s*(?:[一二两三四五六七八九十]+|\d{1,2})\s*[季期部]|\bS\d{1,2}\b|\bP\d{1,2}\b|season\s?\d|part\s?\d|[ⅠⅡⅢⅣⅤⅥⅦⅧⅨⅩ]|新篇|续篇)\s*`)

// BuildQueries 从目标番剧派生宽搜关键词集（按特异性从高到低）。
//
// 资源组对番剧的命名往往和 Bangumi 不一致，所以同时生成：
//  1. 完整名（含季词，利于分季）；
//  2. 去季词名；
//  3. 长 CJK 串拆半（整串搜不到时的兜底）；
//  4. 中日/中英混合时的词组与拉丁短语。
//
// 站点关键词匹配较宽松时，多路并集能显著提高召回。
func BuildQueries(t Target) []string {
	names := []string{}
	if t.Title != "" {
		names = append(names, t.Title)
	}
	for _, a := range t.Aliases {
		if a = strings.TrimSpace(a); a != "" {
			names = append(names, a)
		}
	}
	if t.TitleOrig != "" {
		names = append(names, t.TitleOrig)
	}

	q := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		q = append(q, s)
	}

	// 1) 完整名
	for _, n := range names {
		add(n)
	}
	// 2) 去季词
	for _, n := range names {
		s := strings.TrimSpace(reSeason.ReplaceAllString(n, " "))
		s = strings.Join(strings.Fields(s), " ")
		if s != "" && s != n {
			add(s)
		}
	}
	// 3) 混合名的 CJK 词组 + 长 CJK 串拆半
	// 注意：不生成纯拉丁词组——实测会带出大量同前缀他番，全是噪声。
	for _, n := range names {
		var cjk []string
		for _, tk := range nameTokens(n) {
			if isCJKRun(tk) {
				cjk = append(cjk, tk)
			}
		}
		if len(cjk) >= 2 {
			add(strings.Join(cjk, " "))
		}
		// 拆半只对汉字串有效；日文假名串对半拆实测全是噪声（带出大量同假名音乐资源）。
		for _, tk := range cjk {
			if !isHanRun(tk) {
				continue
			}
			if utf8.RuneCountInString(tk) >= 8 {
				r := []rune(tk)
				half := len(r) / 2
				add(string(r[:half]))
				add(string(r[half:]))
			}
		}
	}
	return q
}

// nameTokens 一个名字的词序列：CJK/假名连续串按最长切分（整段视为一个 token），
// 拉丁按单词切。
func nameTokens(s string) []string {
	var out []string
	run := []rune(nil)
	flush := func() {
		if len(run) == 0 {
			return
		}
		w := strings.ToLower(string(run))
		if isCJKish(w) {
			out = append(out, string(run)) // CJK 串保持原样（含大小写无关）
		} else if utf8.RuneCountInString(w) >= 2 {
			out = append(out, w)
		}
		run = nil
	}
	for _, r := range s {
		if isCJKishRune(r) || unicode.IsSpace(r) {
			if !isCJKishRune(r) {
				flush()
				continue
			}
			run = append(run, r)
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func isCJKishRune(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF:  // CJK 统一表意
		return true
	case r >= 0x3400 && r <= 0x4DBF: // 扩展 A
		return true
	case r >= 0x3040 && r <= 0x30FF: // 平/片假名
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 韩文
		return true
	}
	return false
}

func isCJKish(s string) bool {
	for _, r := range s {
		if isCJKishRune(r) {
			return true
		}
	}
	return false
}

func isCJKRun(s string) bool { return isCJKish(s) && !strings.ContainsAny(s, " \t") }

// isHanRun 整串都是汉字（不含假名）。
func isHanRun(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isHanRune(r) {
			return false
		}
	}
	return true
}
