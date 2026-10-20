package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"anidock/backend/internal/model"
	"anidock/backend/internal/rss/disc"
	"anidock/backend/internal/store"
)

// ---------- RSS 批量发现（v0.3.0） ----------
//
// 对一批番剧串行执行"自动发现（含回验）→ 组合逻辑自动导入"，
// 复用与单番剧发现同一套算法、组合逻辑设置与全局发现互斥（与单发现串行，
// 防请求并发过度轰源站）。全局互斥：同一时刻仅一个批量任务；
// 可随时停止（POST /rss-bulk/stop，优雅：当前番剧处理完后停）；
// 进度（当前番剧/阶段/逐剧结果）落库，关页不中断。

// bulkQueueItem 启动时的完整列表项（落 queue JSON 列；前端据此渲染全部番剧的实时状态）。
type bulkQueueItem struct {
	AnimeID int64  `json:"anime_id"`
	Title   string `json:"title"`
}

// bulkItem 批量任务的逐剧结果行（落 items JSON 列，增量写入）。
type bulkItem struct {
	AnimeID int64  `json:"anime_id"`
	Title   string `json:"title"`
	Result  string `json:"result"` // "创建 N 个 (...)"/"跳过（已有 RSS 源）"/"失败: ..."/"中断（已停止）"
}

// bulkOut 批量任务的 API 响应（queue/items JSON 列已反序列化）。
type bulkOut struct {
	ID             int64           `json:"id"`
	Status         string          `json:"status"`
	Total          int             `json:"total"`
	DoneCount      int             `json:"done_count"`
	CurrentAnimeID int64           `json:"current_anime_id,omitempty"`
	CurrentTitle   string          `json:"current_title,omitempty"`
	Phase          string          `json:"phase,omitempty"`
	SkipFeeded     bool            `json:"skip_feeded"`
	Mirror         bool            `json:"mirror"` // 本批自动建的源开启了自动镜像
	Queue          []bulkQueueItem `json:"queue"`
	Items          []bulkItem      `json:"items"`
	Error          string          `json:"error,omitempty"`
	StartedAt      string          `json:"started_at"`
	FinishedAt     string          `json:"finished_at,omitempty"`
}

func toBulkOut(b *store.BulkDiscovery) bulkOut {
	out := bulkOut{ID: b.ID, Status: b.Status, Total: b.Total, DoneCount: b.DoneCount,
		CurrentAnimeID: b.CurrentAnimeID, CurrentTitle: b.CurrentTitle, Phase: b.Phase,
		SkipFeeded: b.SkipFeeded, Mirror: b.Mirror, Error: b.Error, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt}
	if b.Queue != "" {
		_ = json.Unmarshal([]byte(b.Queue), &out.Queue)
	}
	if out.Queue == nil {
		out.Queue = []bulkQueueItem{}
	}
	if b.Items != "" {
		_ = json.Unmarshal([]byte(b.Items), &out.Items)
	}
	if out.Items == nil {
		out.Items = []bulkItem{}
	}
	return out
}

// BulkDiscoverRss POST /api/admin/rss-bulk
// 请求体 {"anime_ids": [1,2,3], "skip_feeded": true, "mirror": false}
// （skip_feeded = 跳过已有 RSS 源的番剧；mirror = 本批自动建的源开启自动镜像）。
// 启动串行后台批量任务，立即返回任务 id；进度经 GET /rss-bulk 轮询，
// POST /rss-bulk/stop 随时停止。同一时刻仅允许一个批量任务（否则 409）。
func (a *Admin) BulkDiscoverRss(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AnimeIDs   []int64 `json:"anime_ids"`
		SkipFeeded bool    `json:"skip_feeded"`
		Mirror     bool    `json:"mirror"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, errors.New("请求体格式错误"))
		return
	}
	if len(req.AnimeIDs) == 0 {
		writeErr(w, 400, errors.New("番剧列表不能为空"))
		return
	}
	if len(req.AnimeIDs) > 500 {
		writeErr(w, 400, errors.New("单批番剧过多（上限 500）"))
		return
	}
	sites := EffectiveSitePoolList(a.Store, a.Cfg)
	if len(sites) == 0 {
		writeErr(w, 400, errors.New("未配置订阅站点池：请先在「设置 → 抓取任务 → 订阅站点池」填写站点地址（默认空 = 关闭自动 RSS）"))
		return
	}
	if !a.bulkBusy.CompareAndSwap(0, 1) {
		writeErr(w, 409, errors.New("已有批量发现任务在进行，请等它结束或先停止"))
		return
	}
	// 去重 + 确认番剧均存在（任一缺失 → 400，不启动任务）
	seen := map[int64]bool{}
	animes := make([]*model.Anime, 0, len(req.AnimeIDs))
	for _, id := range req.AnimeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		an, err := a.Store.QueryAnimeByID(id)
		if err != nil {
			a.bulkBusy.Store(0)
			writeErr(w, 400, fmt.Errorf("番剧 %d 不存在", id))
			return
		}
		animes = append(animes, an)
	}
	queue := make([]bulkQueueItem, len(animes))
	for i, an := range animes {
		queue[i] = bulkQueueItem{AnimeID: an.ID, Title: an.Title}
	}
	rowID, err := a.Store.CreateBulkDiscovery(len(animes), req.SkipFeeded, req.Mirror, mustJSONStr(queue))
	if err != nil {
		a.bulkBusy.Store(0)
		writeErr(w, 500, err)
		return
	}
	go a.runBulk(rowID, animes, sites, req.SkipFeeded, req.Mirror)
	writeJSON(w, 200, map[string]int64{"task_id": rowID})
}

// BulkDiscoverStatus GET /api/admin/rss-bulk —— 最近一次批量任务快照（前端轮询端点）。
// 尚无记录时 404（前端按"未跑过批量"处理）。
func (a *Admin) BulkDiscoverStatus(w http.ResponseWriter, r *http.Request) {
	b, err := a.Store.LatestBulk()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if b == nil {
		writeErr(w, 404, errors.New("尚无批量发现记录"))
		return
	}
	writeJSON(w, 200, toBulkOut(b))
}

// BulkDiscoverStop POST /api/admin/rss-bulk/stop —— 停止进行中的批量任务（优雅：当前番剧处理完后停）。
func (a *Admin) BulkDiscoverStop(w http.ResponseWriter, r *http.Request) {
	if !a.stopBulk() {
		writeErr(w, 409, errors.New("没有进行中的批量任务"))
		return
	}
	writeJSON(w, 200, map[string]bool{"stopping": true})
}

// stopBulk 触发停止（无任务返回 false）。
func (a *Admin) stopBulk() bool {
	a.bulkMu.Lock()
	defer a.bulkMu.Unlock()
	if a.bulkCancel == nil {
		return false
	}
	a.bulkCancel()
	return true
}

// runBulk 后台串行执行：每部番剧跑"自动发现（含回验）→ 组合逻辑自动导入"。
// 复用全局发现互斥（与单番剧发现串行；有单发现任务在跑则每 10s 重试等待）；
// context 可取消（用户停止），关页不中断；跑完把逐剧结果落库。
func (a *Admin) runBulk(rowID int64, animes []*model.Anime, sites []disc.Site, skipFeeded, mirror bool) {
	defer a.bulkBusy.Store(0)
	ctx, cancel := context.WithCancel(context.Background())
	a.bulkMu.Lock()
	a.bulkCancel = cancel
	a.bulkMu.Unlock()
	defer func() {
		cancel()
		a.bulkMu.Lock()
		a.bulkCancel = nil
		a.bulkMu.Unlock()
	}()

	items := make([]bulkItem, 0, len(animes))
	// recordResult 追加逐剧结果并增量落库（前端轮询即可实时看到已完成的剧）
	recordResult := func(i int, it bulkItem) {
		items = append(items, it)
		_ = a.Store.UpdateBulkProgress(rowID, i+1, mustJSONStr(items))
	}
	stopped := false
	for i, an := range animes {
		if stopped {
			break
		}
		_ = a.Store.UpdateBulkPhase(rowID, an.ID, an.Title, "准备")
		// 跳过已有 RSS 源的番剧（减少重复导入浪费时间）
		if skipFeeded {
			if feeds, err := a.Store.ListFeedsForAnime(an.ID); err == nil && len(feeds) > 0 {
				recordResult(i, bulkItem{AnimeID: an.ID, Title: an.Title, Result: "跳过（已有 RSS 源）"})
				continue
			}
		}
		// 等全局发现槽位（与单发现串行；用户停止时立即退出等待）
		for !a.discBusy.CompareAndSwap(0, 1) {
			select {
			case <-ctx.Done():
				stopped = true
			case <-time.After(10 * time.Second):
			}
			if stopped {
				break
			}
		}
		if stopped {
			break
		}
		perCtx, pcancel := context.WithTimeout(ctx, 10*time.Minute)
		rep, dErr := disc.Discover(perCtx, discoveryTarget(an), sites, disc.Options{
			OnPhase: func(p string) { _ = a.Store.UpdateBulkPhase(rowID, an.ID, an.Title, p) },
		})
		a.discBusy.Store(0)
		pcancel()
		switch {
		case dErr != nil && ctx.Err() != nil:
			// 用户停止：当前剧记"中断"，其余不再处理
			recordResult(i, bulkItem{AnimeID: an.ID, Title: an.Title, Result: "中断（已停止）"})
			stopped = true
		case dErr != nil:
			recordResult(i, bulkItem{AnimeID: an.ID, Title: an.Title, Result: "失败: " + dErr.Error()})
			log.Printf("[bulk] %s 发现失败: %v", an.Title, dErr)
		default:
			summary := a.autoAddCandidates(an.ID, rep.Candidates, mirror)
			recordResult(i, bulkItem{AnimeID: an.ID, Title: an.Title, Result: summary})
		}
		if ctx.Err() != nil {
			stopped = true
		}
	}
	status := "done"
	if stopped {
		status = "stopped"
	}
	_ = a.Store.FinishBulk(rowID, status, len(items), mustJSONStr(items), "")
	log.Printf("[bulk] 任务 %d 结束: %s（%d/%d 部）", rowID, status, len(items), len(animes))
}
