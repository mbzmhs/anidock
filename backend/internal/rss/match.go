package rss

import (
	"strings"
	"unicode"

	"anidock/backend/internal/model"
)

// normalize 归一化：繁→简（OpenCC 全表，与 disc 发现侧同一份 zhconv）、
// 全角→半角、小写、去空白与标点。旧版只有 40 字手工繁简表，
// 含表外字（櫻/聽/緣…）的繁体标题会匹配不到简体番名。
func normalize(s string) string {
	s = Simplify(HalfWidth(s))
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Matcher 番剧匹配器（DESIGN §7.3）：精确/别名 → 包含 → feed 关联提示 → 模糊。
type Matcher struct{}

func NewMatcher() *Matcher { return &Matcher{} }

// Match 在全部番剧中寻找与 showName 最匹配的番剧。
// hintAnimeID 非空时优先（per-show feed 已手动/自动关联到某番剧）。
func (m *Matcher) Match(showName string, animes []*model.Anime, hintAnimeID *int64) *model.Anime {
	show := normalize(showName)
	if show == "" {
		return nil
	}
	type cand struct {
		a  *model.Anime
		ok bool
	}

	// 1) 精确（title / title_orig / 别名）
	for _, a := range animes {
		for _, name := range candidateNames(a) {
			n := normalize(name)
			if n != "" && n == show {
				return a
			}
		}
	}

	// 2) 双向包含（长度>=3，防"剑"这种单字误配）
	if hintAnimeID != nil {
		for _, a := range animes {
			if a.ID == *hintAnimeID {
				for _, name := range candidateNames(a) {
					n := normalize(name)
					if len([]rune(n)) >= 2 && (strings.Contains(show, n) || strings.Contains(n, show)) {
						return a
					}
				}
			}
		}
	}
	var best *model.Anime
	bestScore := 0
	for _, a := range animes {
		for _, name := range candidateNames(a) {
			n := normalize(name)
			if len([]rune(n)) < 2 || len([]rune(show)) < 2 {
				continue
			}
			if strings.Contains(show, n) || strings.Contains(n, show) {
				return a // 包含即认为命中（常见：show 名带副标题/季度后缀）
			}
			if sc := overlapScore(n, show); sc > bestScore {
				bestScore = sc
				best = a
			}
		}
	}
	// 3) 模糊阈值：共同字符占比 >= 60%
	if best != nil && bestScore >= 60 {
		return best
	}
	return nil
}

// candidateNames 番剧的可匹配名：title、title_orig、aliases。
func candidateNames(a *model.Anime) []string {
	var out []string
	if a.Title != "" {
		out = append(out, a.Title)
	}
	if a.TitleOrig != "" && a.TitleOrig != a.Title {
		out = append(out, a.TitleOrig)
	}
	out = append(out, a.Aliases...)
	return out
}

// overlapScore 字符级 Jaccard 相似度（0-100），对中日混合文本足够稳健。
func overlapScore(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	ca := charSet(a)
	cb := charSet(b)
	inter, union := 0, 0
	for c := range ca {
		union++
		if _, ok := cb[c]; ok {
			inter++
		}
	}
	if union == 0 {
		return 0
	}
	return inter * 100 / union
}

func charSet(s string) map[rune]bool {
	m := map[rune]bool{}
	for _, r := range s {
		m[r] = true
	}
	return m
}
