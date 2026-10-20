// Package disc —— RSS 自动发现：给一部番剧，自动在订阅站里模糊搜索、
// 把条目按「字幕组 × 流派(简繁/无修/规格)」分组，并为每个流派搜索并回验
// 一个纯净的订阅地址（纯度 100%：该地址返回的条目只有该组该番，无混季无混组）。
//
// 纯代码路线；标题解读抽象为可替换点（将来可接 AI 小模型实现）。
package disc

import (
	"net/url"
	"strings"
	"time"
)

// Site 订阅站点（站点池的一条配置）。
// Name = 站点真实域名（小写、去 www 前缀），仅作展示与去重；
// Template = 订阅 URL 模板：
//   - 含 {} 占位符：在 {} 处替换转义后的关键词（支持任意动态订阅形态）；
//   - 裸站点地址（无占位符）：按主流形态 <站点根>/rss-<关键词>.xml 生成。
type Site struct {
	Name     string
	Template string
}

// URL 按关键词物化订阅地址。
func (s Site) URL(kw string) string {
	if i := strings.Index(s.Template, "{}"); i >= 0 {
		return s.Template[:i] + url.PathEscape(kw) + s.Template[i+len("{}"):]
	}
	return strings.TrimRight(s.Template, "/") + "/rss-" + url.PathEscape(kw) + ".xml"
}

// HostOf 从订阅地址推导展示名（真实域名，小写、去 www 前缀；解析失败返回 ""）。
// 手动添加的源据此自动标记所属站点。
func HostOf(rssURL string) string {
	u, err := url.Parse(strings.TrimSpace(rssURL))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// Target 待发现的目标番剧（来自本站库）。
type Target struct {
	Title       string   // Bangumi 主标题（一般简体）
	TitleOrig   string   // 原名（日文/英文…，常出现在资源标题里）
	Aliases     []string // 别名
	Year        int      // 播出年份
	Quarter     int      // 季度：1=冬 2=春 3=夏 4=秋（与库内 quarter 一致）
	SeasonLabel string   // 如 "2026-秋"
}

// Item 一条宽搜命中并解析后的条目。
type Item struct {
	Title   string
	Link    string
	PubTime time.Time
	Query   string // 命中它的宽搜关键词

	Group   string // 制作组（标题首部 [X]）
	Show    string // 组自己写法的番剧主干（ParseTitle 产物，保留原始繁简/大小写）
	Episode *float64
	Quality string
	Spec    string

	// 归属判定结果
	Score        float64
	Level        string // high / medium / low
	SeasonReject bool   // 名称匹配但被季词硬否决（同番旧季条目；回验纯度视为无害）
}

// Candidate 一个流派（字幕组 × 变体）→ 一个纯净订阅地址。
// JSON 标签 = API 契约（前端按 snake_case/短名消费，报告也以此格式落库）。
type Candidate struct {
	Group     string   `json:"group"`
	Keyword   string   `json:"keyword"` // 站点无关的订阅关键词（候选的身份；兄弟站凭它各自物化 URL）
	Variant   string   `json:"variant"` // 流派标签：简繁/无修/Raw/规格（如 "简繁 · 无修 · 1080p"）
	URL       string   `json:"url"` // 回验所用站点的订阅地址（物化，展示参考）
	Purity     float64  `json:"purity"` // 回验 feed 中「该组 ∧ 归属该番」的条目占比
	Coverage   float64  `json:"coverage"` // 已知集数的覆盖率
	Episodes   []int    `json:"episodes"`
	FirstPub   string   `json:"first_pub"`
	LastPub    string   `json:"last_pub"`
	Pace       string   `json:"pace"` // 更新节奏（周更/日更/停更 N 天）
	Sample     string   `json:"sample"` // 示例条目标题
	Confidence string   `json:"confidence"` // high / medium / low
	Tried      []string `json:"tried,omitempty"`
	Note       string   `json:"note,omitempty"` // 警告（如纯度不足 100%）
}

// Report 一次发现的结果。
type Report struct {
	Target     Target
	Site       string
	Queries    []string
	Scanned    int // 去重后条目数
	Matched    int
	Items      []Item // 全部归属过打分的条目（按流派聚合前的原始结果）
	Candidates []Candidate
	Unmatched  []Item // 低置信条目（人工确认区）
	Notes      []string
}

// Options 预算与限速控制。
type Options struct {
	Queries       []string      // 手动指定关键词（非空则覆盖自动查询集）
	MaxQueries    int           // 宽搜关键词上限（默认 8）
	PerQueryCap   int           // 单个宽搜最多采用多少条（默认 40）
	MaxGroups     int           // 最多回验多少流派（默认 12；high 档优先占用预算）
	CandsPerGroup int           // 每流派最多尝试几个候选地址（默认 4）
	Delay         time.Duration // 相邻请求间隔（默认 1.2s）
	Timeout       time.Duration // 单请求超时（默认 30s）
	NoVerify      bool          // 跳过回验（只分组，更快）
	OnPhase       func(phase string) // 进入各阶段时回调（后台任务进度展示；可为 nil）
}

// DefaultOptions 填充缺省值。
func (o *Options) fillDefaults() {
	if o.MaxQueries <= 0 {
		o.MaxQueries = 8
	}
	if o.PerQueryCap <= 0 {
		o.PerQueryCap = 40
	}
	if o.MaxGroups <= 0 {
		o.MaxGroups = 12
	}
	if o.CandsPerGroup <= 0 {
		o.CandsPerGroup = 4
	}
	if o.Delay <= 0 {
		o.Delay = 1200 * time.Millisecond
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
}
