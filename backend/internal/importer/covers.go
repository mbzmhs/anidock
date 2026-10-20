package importer

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"anidock/backend/internal/store"
)

// ---------- 批量补拉缺失封面 ----------
//
// 归档导入不含图片，封面靠 v0 API 拉取；网络抖动/API 限流会导致部分失败。
// 这里提供独立的后台任务：对所有"有 bangumi_id 但封面为空"的番剧 6 路并发补拉，
// 可反复执行（已拉到封面的自动排除，幂等）。

type CoverJob struct {
	mu      sync.Mutex
	running bool
	total   int
	done    int
	failed  int
	errMsg  string
	notes   []string
	started time.Time
}

var coverJob = &CoverJob{}

// CoverStatusInfo 任务状态快照（前端轮询）。
type CoverStatusInfo struct {
	Running   bool     `json:"running"`
	Total     int      `json:"total"`
	Done      int      `json:"done"`
	Failed    int      `json:"failed"`
	Error     string   `json:"error,omitempty"`
	Notes     []string `json:"notes,omitempty"`
	StartedAt string   `json:"started_at,omitempty"`
}

func (j *CoverJob) lock()   { j.mu.Lock() }
func (j *CoverJob) unlock() { j.mu.Unlock() }
func (j *CoverJob) note(format string, args ...any) {
	j.lock()
	if len(j.notes) < 30 {
		j.notes = append(j.notes, fmt.Sprintf(format, args...))
	}
	j.unlock()
}
func (j *CoverJob) addDone(ok bool) {
	j.lock()
	j.done++
	if !ok {
		j.failed++
	}
	j.unlock()
}
func (j *CoverJob) fail(err error) {
	j.lock()
	j.running = false
	j.errMsg = err.Error()
	j.unlock()
}

// CoverStatus 返回当前补封面任务状态快照。
func CoverStatus() CoverStatusInfo {
	j := coverJob
	j.lock()
	defer j.unlock()
	notes := append([]string{}, j.notes...)
	st := CoverStatusInfo{
		Running: j.running, Total: j.total, Done: j.done,
		Failed: j.failed, Error: j.errMsg, Notes: notes,
	}
	if !j.started.IsZero() {
		st.StartedAt = j.started.Format("15:04:05")
	}
	return st
}

// StartCoverFetch 启动批量补封面任务（后台执行，状态经 CoverStatus() 轮询）。
func StartCoverFetch(st *store.Store, b *Bangumi) error {
	j := coverJob
	j.lock()
	if j.running {
		j.unlock()
		return errors.New("已有补封面任务在进行")
	}
	// 逐字段重置（不能整体替换 struct，Mutex 归零是 UB）
	j.running = true
	j.total, j.done, j.failed = 0, 0, 0
	j.errMsg = ""
	j.notes = j.notes[:0]
	j.started = time.Now()
	j.unlock()
	go runCoverFetch(st, b)
	return nil
}

func runCoverFetch(st *store.Store, b *Bangumi) {
	j := coverJob
	missing, err := st.AnimeMissingCovers()
	if err != nil {
		j.fail(fmt.Errorf("查询缺封面番剧: %w", err))
		return
	}
	j.lock()
	j.total = len(missing)
	j.unlock()
	if len(missing) == 0 {
		j.note("没有缺封面的番剧")
	} else {
		j.note("发现 %d 部缺封面，开始拉取（6 路并发）", len(missing))
	}

	queue := make(chan int, len(missing))
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				m := missing[i]
				if !fetchOneCover(b, st, m.BangumiID, m.ID) {
					j.addDone(false)
				} else {
					j.addDone(true)
				}
			}
		}()
	}
	for i := range missing {
		queue <- i
	}
	close(queue)
	wg.Wait()

	j.lock()
	j.running = false
	if j.failed > 0 {
		j.notes = append(j.notes, fmt.Sprintf("完成：%d 部成功，%d 部失败（Bangumi 无图或网络失败，可稍后重跑）", j.done-j.failed, j.failed))
	} else {
		j.notes = append(j.notes, fmt.Sprintf("完成：%d 部全部成功", j.done))
	}
	j.unlock()
}
