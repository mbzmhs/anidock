// rssdiscover —— RSS 自动发现算法的命令行实验入口（本地实验用，非生产组件）。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"anidock/backend/internal/rss/disc"
)

func main() {
	var (
		title     = flag.String("title", "", "Bangumi 主标题（简体）")
		orig      = flag.String("orig", "", "原名（日/英）")
		aliases   = flag.String("aliases", "", "别名，逗号分隔")
		year      = flag.Int("year", 0, "播出年份")
		quarter   = flag.Int("quarter", 0, "季度 1=冬 2=春 3=夏 4=秋")
		label     = flag.String("label", "", "season_label（如 2026-秋）")
		queriesF  = flag.String("queries", "", "手动宽搜关键词，逗号/竖线分隔（覆盖自动查询集）")
		siteF     = flag.String("site", "", "站点池：URL 模板（{} = 关键词）或站点地址，逗号/换行分隔；必填（兄弟站同数据源，主站不可达时按序回退）")
		noVerify  = flag.Bool("no-verify", false, "跳过候选地址回验")
		maxGroups = flag.Int("max-groups", 12, "最多回验流派数")
		cands     = flag.Int("cands", 4, "每流派候选地址数")
		delay     = flag.Duration("delay", 1200*time.Millisecond, "请求间隔")
		queriesOnly = flag.Bool("queries-only", false, "只打印自动生成的宽搜关键词集")
	)
	flag.Parse()
	if *title == "" {
		fmt.Fprintln(os.Stderr, "需要 --title")
		flag.Usage()
		os.Exit(2)
	}
	t := disc.Target{
		Title:       *title,
		TitleOrig:   *orig,
		Aliases:     splitList(*aliases),
		Year:        *year,
		Quarter:     *quarter,
		SeasonLabel: *label,
	}
	queries := splitList(*queriesF)

	if *queriesOnly {
		for i, q := range disc.BuildQueries(t) {
			fmt.Printf("%d. %s\n", i+1, q)
		}
		return
	}

	sites := disc.PoolSites(*siteF)
	if len(sites) == 0 {
		fmt.Fprintln(os.Stderr, "错误: --site 必填（URL 模板（{} = 关键词）或站点地址，逗号/换行分隔）")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	start := time.Now()
	rep, err := disc.Discover(ctx, t, sites, disc.Options{
		Queries:     queries,
		MaxGroups:   *maxGroups,
		CandsPerGroup: *cands,
		Delay:       *delay,
		NoVerify:    *noVerify,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	printReport(rep, time.Since(start))
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '|' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func printReport(r *disc.Report, elapsed time.Duration) {
	fmt.Println("============================================================")
	fmt.Printf("目标: %s", r.Target.Title)
	if r.Target.TitleOrig != "" {
		fmt.Printf(" (原名: %s)", r.Target.TitleOrig)
	}
	fmt.Printf(" | %d年 Q%d", r.Target.Year, r.Target.Quarter)
	if r.Target.SeasonLabel != "" {
		fmt.Printf(" (%s)", r.Target.SeasonLabel)
	}
	fmt.Printf("\n站点: %s | 耗时: %s\n", r.Site, elapsed.Round(time.Second))

	fmt.Printf("\n宽搜关键词(%d): %s\n", len(r.Queries), strings.Join(r.Queries, " | "))
	fmt.Printf("扫描条目(去重): %d | 归属命中: %d | 未归属: %d\n", r.Scanned, r.Matched, len(r.Unmatched))
	for _, n := range r.Notes {
		fmt.Println("  ⚠", n)
	}

	fmt.Printf("\n—— 流派候选 (%d) ——\n", len(r.Candidates))
	for i, c := range r.Candidates {
		fmt.Printf("\n[%d] %s  %s", i+1, c.Group, c.Variant)
		fmt.Printf("   置信: %s", c.Confidence)
		fmt.Printf("\n    集数: %s | 更新: %s ~ %s (%s)\n",
			intList(c.Episodes), c.FirstPub, c.LastPub, c.Pace)
		if c.URL != "" {
			fmt.Printf("    地址: %s\n", c.URL)
			fmt.Printf("    纯度: %.0f%% | 覆盖: %.0f%%\n", c.Purity*100, c.Coverage*100)
			for _, u := range c.Tried {
				mark := "  "
				if u == c.URL {
					mark = "→ "
				}
				fmt.Printf("      %s%s\n", mark, u)
			}
		}
		if c.Note != "" {
			fmt.Println("    ⚠", c.Note)
		}
		fmt.Printf("    示例: %s\n", c.Sample)
	}

	if len(r.Unmatched) > 0 {
		fmt.Printf("\n—— 未归属/低置信 (%d) ——\n", len(r.Unmatched))
		un := append([]disc.Item(nil), r.Unmatched...)
		sort.SliceStable(un, func(i, j int) bool { return un[j].Score > un[i].Score })
		for _, it := range un[:min(len(un), 10)] {
			fmt.Printf("  (%.2f) [%s] %s\n", it.Score, it.Group, it.Title)
		}
		if len(un) > 10 {
			fmt.Printf("  … 其余 %d 条\n", len(un)-10)
		}
	}
}

func intList(eps []int) string {
	if len(eps) == 0 {
		return "未知"
	}
	if len(eps) > 15 {
		return fmt.Sprintf("%d~%d (共%d集)", eps[0], eps[len(eps)-1], len(eps))
	}
	s := make([]string, len(eps))
	for i, e := range eps {
		s[i] = fmt.Sprintf("%d", e)
	}
	return strings.Join(s, ",")
}
