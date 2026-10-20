package rss

import (
	"context"
	"reflect"
	"testing"
	"time"

	"anidock/backend/internal/model"
)

// TestSeasonSet 标题季数提取（中/英/罗马数字、多季合集）。
func TestSeasonSet(t *testing.T) {
	cases := []struct {
		in   string
		want map[int]bool
	}{
		{"[字幕组B] 番剧D 第三季 - 03 [1080P]", map[int]bool{3: true}},
		{"[字幕组B] 番剧D 第二季 - 03 [1080P]", map[int]bool{2: true}},
		{"Show G S2 [01] [1080p]", map[int]bool{2: true}},
		{"Show D Season 3 - 01", map[int]bool{3: true}},
		{"番剧D 第三季 01", map[int]bool{3: true}},
		{"番剧名 第10话 1080p", map[int]bool{}}, // 集数不是季数
		{"番剧A 第二季 Episode 1", map[int]bool{2: true}},
		{"某番 S11 合集 第一季~第三季", map[int]bool{1: true, 3: true, 11: true}}, // 区间只含两端写法
	}
	for _, c := range cases {
		got := seasonSet(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("seasonSet(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestSeasonConflict per-show 源的旧季文件拦截。
func TestSeasonConflict(t *testing.T) {
	s3 := &model.Anime{
		Title:     "番剧D 第三季",
		TitleOrig: "番剧D 第三季",
		Aliases:   []string{"Show D Season 3"},
	}
	s1 := &model.Anime{Title: "番剧A"} // S1 无季词 → 不过滤
	cases := []struct {
		anime *model.Anime
		title string
		want  bool
	}{
		{s3, "[字幕组B] 番剧D 第二季 - 03 [1080P]", true},  // 旧季文件 → 拦
		{s3, "[字幕组B] 番剧D 第三季 - 03 [1080P]", false}, // 本季 → 放行
		{s3, "[字幕组B] 番剧D 第三季 - 12.5 [1080P]", false},
		{s1, "番剧A 第二季 - 01 [1080P]", false}, // S1 番剧无季词 → 不拦
		{s3, "[X] 他番 第二季 - 01", true}, // 季数冲突一律拦（组名不符的条目另有归属逻辑兜底）
	}
	for _, c := range cases {
		if got := seasonConflict(c.anime, c.title); got != c.want {
			t.Errorf("seasonConflict(%+v, %q) = %v, want %v", c.anime.Title, c.title, got, c.want)
		}
	}
}

// TestHostOf 订阅地址 → host（限流冷却键）。
func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://www.a.org/rss-x.xml": "www.a.org",
		"http://b.net:8080/p/rss.xml":  "b.net:8080",
		"https://c.org":               "c.org",
		"not-a-url":                   "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHostCooldown 403/429 限流冷却：设置后该 host 处于冷却，其它 host 不受影响。
func TestHostCooldown(t *testing.T) {
	f := NewFetcher(nil, 3*time.Minute)
	if _, ok := f.cooldownUntil("a.org"); ok {
		t.Fatal("初始不应有冷却")
	}
	f.setCooldown("a.org")
	if until, ok := f.cooldownUntil("a.org"); !ok || time.Until(until) < 14*time.Minute {
		t.Errorf("a.org 应处于 ~15 分钟冷却，got ok=%v until=%v", ok, until)
	}
	if _, ok := f.cooldownUntil("b.org"); ok {
		t.Error("冷却不应扩散到其它 host")
	}
}

// TestPaceWait 节流等待：关闭（0）时立即返回；开启时等待落在 [间隔, 2×间隔] 区间。
func TestPaceWait(t *testing.T) {
	ctx := context.Background()
	f := NewFetcher(nil, 3*time.Minute)
	f.PaceFunc = func() time.Duration { return 0 }
	if !sleepCtx(ctx, 20*time.Millisecond) {
		t.Fatal("sleepCtx 正常路径应返回 true")
	}
	f.PaceFunc = func() time.Duration { return 100 * time.Millisecond }
	start := time.Now()
	f.paceWait(ctx)
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("paceWait(100ms) 等待 %v，应落在 [100ms, 200ms+余量]", elapsed)
	}
}

// TestPreemptQueueJump 手动插队：后台持锁时手动 WaitBeginFetch 登记插队请求；
// 后台在检查点 preemptYield 让位并放锁后，手动方立刻拿到锁执行，队列随之清空。
func TestPreemptQueueJump(t *testing.T) {
	f := NewFetcher(nil, time.Minute)

	if !f.BeginFetch() {
		t.Fatal("background should begin fetch")
	}

	gotManual := make(chan bool, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		gotManual <- f.WaitBeginFetch(ctx)
	}()

	// 等手动侧把插队 ticket 登记进队列（确定性等待，不靠 sleep 猜）
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.preemptMu.Lock()
		n := len(f.preemptQ)
		f.preemptMu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual did not register a preempt ticket in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 后台检查点发现插队请求 → 让位（提前收尾本轮）并释放锁
	if !f.preemptYield() {
		t.Fatal("preemptYield should see the queued manual request")
	}
	f.EndFetch()

	select {
	case ok := <-gotManual:
		if !ok {
			t.Error("manual WaitBeginFetch should acquire after background yields")
		}
		f.EndFetch() // 手动侧用完释放
	case <-time.After(3 * time.Second):
		t.Fatal("manual WaitBeginFetch did not return after yield")
	}

	f.preemptMu.Lock()
	remain := len(f.preemptQ)
	f.preemptMu.Unlock()
	if remain != 0 {
		t.Errorf("preemptQ should be drained, got %d", remain)
	}
}

// TestPreemptWaitTimeout 持锁方一直不让位也不释放：手动等待超时返回 false，
// 且已登记的 ticket 被撤掉（不残留、不会误伤下一轮）。
func TestPreemptWaitTimeout(t *testing.T) {
	f := NewFetcher(nil, time.Minute)
	if !f.BeginFetch() {
		t.Fatal("background should begin fetch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if ok := f.WaitBeginFetch(ctx); ok {
		t.Error("should time out (false) when holder never yields")
	}
	f.preemptMu.Lock()
	n := len(f.preemptQ)
	f.preemptMu.Unlock()
	if n != 0 {
		t.Errorf("ticket should be dropped on timeout, got %d", n)
	}
	f.EndFetch()
}

// TestWaitBeginFetchImmediate 锁空闲时手动 WaitBeginFetch 立即获得，无需排队。
func TestWaitBeginFetchImmediate(t *testing.T) {
	f := NewFetcher(nil, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !f.WaitBeginFetch(ctx) {
		t.Fatal("should acquire immediately when lock is free")
	}
	f.EndFetch()
}
