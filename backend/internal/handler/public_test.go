package handler

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseAnimeListParamsIds ids 批量筛选参数：解析/失败跳过/去重/上限/与其他条件 AND。
func TestParseAnimeListParamsIds(t *testing.T) {
	big := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf("%d", i+1)
		}
		return strings.Join(parts, ",")
	}
	cases := []struct {
		name string
		qs   string
		want []int64
	}{
		{"无 ids 参数不筛选", "", nil},
		{"空值不筛选", "ids=", nil},
		{"正常解析", "ids=1,2,3", []int64{1, 2, 3}},
		{"解析失败片段跳过", "ids=1,abc,2,,3", []int64{1, 2, 3}},
		{"去重", "ids=2,1,2,3,1", []int64{2, 1, 3}},
		{"超过 1000 只取前 1000", "ids=" + big(1001), nil}, // nil = 用断言函数处理
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _, _ := parseAnimeListParams(httptest.NewRequest("GET", "/api/anime?"+c.qs, nil))
			if c.name == "超过 1000 只取前 1000" {
				if len(f.Ids) != 1000 {
					t.Fatalf("期望截断到 1000，实际 %d", len(f.Ids))
				}
				if f.Ids[0] != 1 || f.Ids[999] != 1000 {
					t.Fatalf("截断应保留前 1000 个（1..1000），实际首尾 = %d..%d", f.Ids[0], f.Ids[999])
				}
				return
			}
			if len(f.Ids) != len(c.want) {
				t.Fatalf("ids = %v，期望 %v", f.Ids, c.want)
			}
			for i := range c.want {
				if f.Ids[i] != c.want[i] {
					t.Fatalf("ids = %v，期望 %v", f.Ids, c.want)
				}
			}
		})
	}
}

// TestParseAnimeListParamsIdsAndOther ids 与 year 等其他条件保持 AND（互不覆盖）。
func TestParseAnimeListParamsIdsAndOther(t *testing.T) {
	f, limit, offset := parseAnimeListParams(httptest.NewRequest("GET", "/api/anime?ids=7,8&year=2026&quarter=1&limit=5&offset=10", nil))
	if f.Year != 2026 || f.Quarter != 1 {
		t.Fatalf("year/quarter 应照常解析: %d/%d", f.Year, f.Quarter)
	}
	if len(f.Ids) != 2 || f.Ids[0] != 7 || f.Ids[1] != 8 {
		t.Fatalf("ids 解析错误: %v", f.Ids)
	}
	if limit != 5 || offset != 10 {
		t.Fatalf("limit/offset 解析错误: %d/%d", limit, offset)
	}
}
