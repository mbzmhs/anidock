package disc

import (
	"net/url"
	"strings"
	"unicode"
)

// 站点池配置解析（设置页多行文本 → 站点列表）。
// 抓取器（rss 包）与接口层（handler 包）共用，保证两处展开口径一致。

// SplitPool 站点池配置的条目切分：换行/逗号/分号/竖线/空白皆可作分隔符。
func SplitPool(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || unicode.IsSpace(r)
	})
}

// SiteFromEntry 把一条用户输入的站点条目转成 Site：
// URL 模板（含 {} = 关键词占位符，支持任意动态订阅形态）或裸站点地址
// （按主流形态 <站点根>/rss-<关键词>.xml 处理，兼容旧配置）。
// Name = 真实域名（小写、去 www 前缀）。不是合法的 http/https 地址时返回 nil。
func SiteFromEntry(raw string) *Site {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil
	}
	if n := strings.Count(raw, "{}"); n > 1 {
		return nil // 多个占位符非法
	}
	if i := strings.Index(raw, "{}"); i >= 0 {
		// 占位符替换后必须是合法 URL
		probe := raw[:i] + "probe" + raw[i+len("{}"):]
		if _, err := url.ParseRequestURI(probe); err != nil {
			return nil
		}
	}
	return &Site{
		Name:     strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."),
		Template: raw,
	}
}

// PoolSites 把站点池配置文本解析为去重后的站点列表（非法条目静默跳过，
// 保存时已 400 拦截；运行时忽略避免拖垮抓取/发现）。
func PoolSites(raw string) []Site {
	var out []Site
	for _, part := range SplitPool(raw) {
		s := SiteFromEntry(part)
		if s == nil {
			continue
		}
		dup := false
		for _, x := range out {
			if x.Name == s.Name {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, *s)
		}
	}
	return out
}
