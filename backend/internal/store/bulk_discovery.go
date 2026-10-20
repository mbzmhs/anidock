package store

import (
	"database/sql"
)

// BulkDiscovery RSS 批量发现任务的一行（v0.3.0）。
// JSON 列（queue/items）保持原始字符串，由 handler 层反序列化（store 不依赖 handler）。
type BulkDiscovery struct {
	ID             int64  `json:"id"`
	Status         string `json:"status"` // running / done / stopped / error
	Total          int    `json:"total"`
	DoneCount      int    `json:"done_count"`
	CurrentAnimeID int64  `json:"current_anime_id,omitempty"`
	CurrentTitle   string `json:"current_title,omitempty"`
	Phase          string `json:"phase,omitempty"`
	SkipFeeded     bool   `json:"skip_feeded"`
	Mirror         bool   `json:"mirror"` // 本批自动建的源是否开启自动镜像
	Queue          string `json:"-"`
	Items          string `json:"-"`
	Error          string `json:"error,omitempty"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at,omitempty"`
}

const bulkCols = `id, status, total, done_count, COALESCE(current_anime_id,0) AS current_anime_id,
	COALESCE(current_title,'') AS current_title, COALESCE(phase,'') AS phase, skip_feeded,
	COALESCE(mirror,0) AS mirror,
	COALESCE(queue,'') AS queue, COALESCE(items,'') AS items,
	COALESCE(error,'') AS error, started_at, COALESCE(finished_at,'') AS finished_at`

// bulkKeep 批量任务记录保留上限（更早的自动清理）。
const bulkKeep = 20

func scanBulkRow(row interface{ Scan(...any) error }) (*BulkDiscovery, error) {
	var b BulkDiscovery
	var skip, mirror int
	if err := row.Scan(&b.ID, &b.Status, &b.Total, &b.DoneCount, &b.CurrentAnimeID, &b.CurrentTitle,
		&b.Phase, &skip, &mirror, &b.Queue, &b.Items, &b.Error, &b.StartedAt, &b.FinishedAt); err != nil {
		return nil, err
	}
	b.SkipFeeded = skip != 0
	b.Mirror = mirror != 0
	return &b, nil
}

// CreateBulkDiscovery 新建一条"进行中"批量任务记录（queue = 启动时的完整列表 JSON），
// 并顺手清掉超出保留上限的旧记录。mirror = 本批自动建的源是否开启自动镜像。
func (s *Store) CreateBulkDiscovery(total int, skipFeeded, mirror bool, queueJSON string) (int64, error) {
	if _, err := s.DB.Exec(`DELETE FROM rss_bulk_discovery WHERE id NOT IN
		(SELECT id FROM rss_bulk_discovery ORDER BY id DESC LIMIT ?)`, bulkKeep); err != nil {
		return 0, err
	}
	skip := 0
	if skipFeeded {
		skip = 1
	}
	m := 0
	if mirror {
		m = 1
	}
	res, err := s.DB.Exec(`INSERT INTO rss_bulk_discovery (status, total, skip_feeded, mirror, queue, started_at) VALUES ('running',?,?,?,?,?)`,
		total, skip, m, nullStr(queueJSON), NowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateBulkPhase 刷新进行中阶段（当前番剧/阶段），items 不动——
// 发现过程的 OnPhase 回调高频调用，不应覆盖增量结果。
func (s *Store) UpdateBulkPhase(id int64, currentAnimeID int64, currentTitle, phase string) error {
	_, err := s.DB.Exec(`UPDATE rss_bulk_discovery SET current_anime_id=?, current_title=?, phase=? WHERE id=?`,
		currentAnimeID, nullStr(currentTitle), nullStr(phase), id)
	return err
}

// UpdateBulkProgress 一部番剧处理完：已处理数 +1，写入最新逐剧结果（增量 JSON），
// 清空"当前番剧"标记。
func (s *Store) UpdateBulkProgress(id int64, done int, itemsJSON string) error {
	_, err := s.DB.Exec(`UPDATE rss_bulk_discovery SET done_count=?, current_anime_id=NULL, current_title='', phase='', items=? WHERE id=?`,
		done, nullStr(itemsJSON), id)
	return err
}

// FinishBulk 收尾：status=done/stopped/error + 逐剧结果 JSON 落库。
func (s *Store) FinishBulk(id int64, status string, done int, items, errMsg string) error {
	_, err := s.DB.Exec(`UPDATE rss_bulk_discovery
		SET status=?, done_count=?, current_anime_id=NULL, current_title='', phase='', items=?, error=?, finished_at=?
		WHERE id=?`,
		status, done, nullStr(items), nullStr(errMsg), NowStr(), id)
	return err
}

func (s *Store) GetBulk(id int64) (*BulkDiscovery, error) {
	b, err := scanBulkRow(s.DB.QueryRow(`SELECT `+bulkCols+` FROM rss_bulk_discovery WHERE id=?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// LatestBulk 最近一次批量发现记录（没有则返回 nil, nil）。
func (s *Store) LatestBulk() (*BulkDiscovery, error) {
	b, err := scanBulkRow(s.DB.QueryRow(`SELECT ` + bulkCols + ` FROM rss_bulk_discovery ORDER BY id DESC LIMIT 1`))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}
