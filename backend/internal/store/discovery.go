package store

import (
	"database/sql"
)

// Discovery RSS 自动发现任务的一行（v0.2.0）。
// JSON 列（queries/candidates/unmatched/notes）保持原始字符串，
// 由 handler 层反序列化为具体类型（store 不依赖 disc 包，避免 import 环）。
type Discovery struct {
	ID         int64  `json:"id"`
	AnimeID    int64  `json:"anime_id"`
	Site       string `json:"site"`
	Status     string `json:"status"` // running / done / error
	Phase      string `json:"phase,omitempty"`
	Scanned    int    `json:"scanned"`
	Matched    int    `json:"matched"`
	Queries    string `json:"-"`
	Candidates string `json:"-"`
	Unmatched  string `json:"-"`
	Notes      string `json:"-"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

const discoveryCols = `id, anime_id, site, status, COALESCE(phase,'') AS phase, scanned, matched,
	COALESCE(queries,'') AS queries, COALESCE(candidates,'') AS candidates,
	COALESCE(unmatched,'') AS unmatched, COALESCE(notes,'') AS notes,
	COALESCE(error,'') AS error, started_at, COALESCE(finished_at,'') AS finished_at`

// discoveryKeepPerAnime 每部番剧保留的发现记录上限（更早的自动清理）。
const discoveryKeepPerAnime = 20

func scanDiscoveryRow(row interface{ Scan(...any) error }) (*Discovery, error) {
	var d Discovery
	if err := row.Scan(&d.ID, &d.AnimeID, &d.Site, &d.Status, &d.Phase, &d.Scanned, &d.Matched,
		&d.Queries, &d.Candidates, &d.Unmatched, &d.Notes, &d.Error, &d.StartedAt, &d.FinishedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

// CreateDiscovery 新建一条"进行中"任务记录，并顺手清掉该番剧超出保留上限的旧记录。
func (s *Store) CreateDiscovery(animeID int64, site string) (int64, error) {
	if _, err := s.DB.Exec(`DELETE FROM rss_discovery WHERE anime_id=? AND id NOT IN
		(SELECT id FROM rss_discovery WHERE anime_id=? ORDER BY id DESC LIMIT ?)`,
		animeID, animeID, discoveryKeepPerAnime); err != nil {
		return 0, err
	}
	res, err := s.DB.Exec(`INSERT INTO rss_discovery (anime_id, site, status, started_at) VALUES (?,?,?,?)`,
		animeID, site, "running", NowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateDiscoveryPhase 刷新进行中阶段（宽搜/种子扩展/候选回验），前端轮询展示。
func (s *Store) UpdateDiscoveryPhase(id int64, phase string) error {
	_, err := s.DB.Exec(`UPDATE rss_discovery SET phase=? WHERE id=?`, nullStr(phase), id)
	return err
}

// FinishDiscovery 收尾：status=done/error + 报告 JSON 落库。
func (s *Store) FinishDiscovery(id int64, ok bool, errMsg, queries, candidates, unmatched, notes string, scanned, matched int) error {
	status := "done"
	if !ok {
		status = "error"
	}
	_, err := s.DB.Exec(`UPDATE rss_discovery
		SET status=?, phase='', scanned=?, matched=?, queries=?, candidates=?, unmatched=?, notes=?, error=?, finished_at=?
		WHERE id=?`,
		status, scanned, matched, nullStr(queries), nullStr(candidates), nullStr(unmatched), nullStr(notes),
		nullStr(errMsg), NowStr(), id)
	return err
}

func (s *Store) GetDiscovery(id int64) (*Discovery, error) {
	d, err := scanDiscoveryRow(s.DB.QueryRow(`SELECT `+discoveryCols+` FROM rss_discovery WHERE id=?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// LatestDiscovery 某番剧最近一次发现记录（没有则返回 nil, nil）。
func (s *Store) LatestDiscovery(animeID int64) (*Discovery, error) {
	d, err := scanDiscoveryRow(s.DB.QueryRow(`SELECT `+discoveryCols+` FROM rss_discovery WHERE anime_id=? ORDER BY id DESC LIMIT 1`, animeID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// DeleteDiscoveriesOfAnime 删除番剧时级联清理（DeleteAnime 调用）。
func (s *Store) DeleteDiscoveriesOfAnime(animeID int64) error {
	_, err := s.DB.Exec(`DELETE FROM rss_discovery WHERE anime_id=?`, animeID)
	return err
}
