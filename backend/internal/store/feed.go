package store

import (
	"database/sql"
	"fmt"

	"anidock/backend/internal/model"
)

const feedCols = `id, name, rss_url, COALESCE(base_url,'') AS base_url, anime_id, priority, enabled,
	COALESCE(mirror,0) AS mirror, COALESCE(site,'') AS site, COALESCE(keyword,'') AS keyword,
	color, last_fetched_at, last_error, created_at`

func (s *Store) ListFeeds() ([]*model.Feed, error) {
	rows, err := s.DB.Query(`SELECT ` + feedCols + ` FROM feed ORDER BY priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.Feed
	for rows.Next() {
		f, err := scanFeedRow(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

func (s *Store) GetFeed(id int64) (*model.Feed, error) {
	row := s.DB.QueryRow(`SELECT `+feedCols+` FROM feed WHERE id=?`, id)
	return scanFeedRow(row)
}

func scanFeedRow(row interface{ Scan(...any) error }) (*model.Feed, error) {
	var f model.Feed
	var animeID, enabled, mirror sql.NullInt64
	var base, site, keyword, color, lastFetched, lastErr sql.NullString
	if err := row.Scan(&f.ID, &f.Name, &f.RSSURL, &base, &animeID, &f.Priority, &enabled, &mirror,
		&site, &keyword, &color, &lastFetched, &lastErr, &f.CreatedAt); err != nil {
		return nil, err
	}
	f.BaseURL = base.String
	f.Site = site.String
	f.Keyword = keyword.String
	f.Color = color.String
	if animeID.Valid {
		v := animeID.Int64
		f.AnimeID = &v
	}
	f.Enabled = enabled.Int64 == 1
	f.Mirror = mirror.Int64 == 1
	if lastFetched.Valid {
		v := lastFetched.String
		f.LastFetchedAt = &v
	}
	if lastErr.Valid && lastErr.String != "" {
		f.LastError = lastErr.String
	}
	return &f, nil
}

// CreateFeed 新建 RSS 源。animeID 可空（之后可经 feed→anime 关联或待归类绑定）。
// site/keyword：订阅地址所属站点与站点无关关键词（手动源可空）。
func (s *Store) CreateFeed(name, rssURL, baseURL string, animeID *int64, priority, enabled int, mirror bool, color, site, keyword string) (int64, error) {
	if name == "" || rssURL == "" {
		return 0, fmt.Errorf("name / rss_url 不能为空")
	}
	if priority <= 0 {
		priority = 100
	}
	res, err := s.DB.Exec(`INSERT INTO feed (name, rss_url, base_url, anime_id, priority, enabled, mirror, site, keyword, color, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		name, rssURL, nullStr(baseURL), nullI64(animeID), priority, enabled, boolInt(mirror),
		nullStr(site), nullStr(keyword), nullStr(color), NowStr())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateFeed 更新 RSS 源（anime_id 传 nil 表示清除关联）。
func (s *Store) UpdateFeed(f *model.Feed) error {
	_, err := s.DB.Exec(`UPDATE feed SET name=?, rss_url=?, base_url=?, anime_id=?, priority=?, enabled=?, mirror=?, site=?, keyword=?, color=?
		WHERE id=?`,
		f.Name, f.RSSURL, nullStr(f.BaseURL), nullI64(f.AnimeID), f.Priority, boolInt(f.Enabled), boolInt(f.Mirror),
		nullStr(f.Site), nullStr(f.Keyword), nullStr(f.Color), f.ID)
	return err
}

// DeleteFeed 删除 RSS 源及其全部更新记录（UI 承诺：历史更新一并删除）。
func (s *Store) DeleteFeed(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM release WHERE feed_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM anime_feed WHERE feed_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM feed WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// ListFeedsForAnime 番剧"隶属"的 RSS 源：feed.anime_id 直接关联，
// 或该源的 release 已匹配到该番剧（per-show 源抓取后自动归入）。
func (s *Store) ListFeedsForAnime(animeID int64) ([]*model.Feed, error) {
	all, err := s.ListFeeds()
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query("SELECT DISTINCT feed_id FROM release WHERE anime_id=?", animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hasReleases := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			hasReleases[id] = true
		}
	}
	var out []*model.Feed
	seen := map[int64]bool{}
	for _, f := range all {
		if seen[f.ID] {
			continue
		}
		linked := f.AnimeID != nil && *f.AnimeID == animeID
		if linked || hasReleases[f.ID] {
			out = append(out, f)
			seen[f.ID] = true
		}
	}
	return out, nil
}

// FeedByURL 同番剧下已存在的同地址源（自动发现批量建源时去重用；没有则返回 nil, nil）。
func (s *Store) FeedByURL(animeID int64, rssURL string) (*model.Feed, error) {
	f, err := scanFeedRow(s.DB.QueryRow(`SELECT `+feedCols+` FROM feed WHERE rss_url=? AND anime_id=? LIMIT 1`, rssURL, animeID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// FeedByKeyword 同番剧下已存在的同关键词逻辑源（自动发现幂等用；没有则返回 nil, nil）。
func (s *Store) FeedByKeyword(animeID int64, keyword string) (*model.Feed, error) {
	f, err := scanFeedRow(s.DB.QueryRow(`SELECT `+feedCols+` FROM feed WHERE anime_id=? AND lower(keyword)=lower(?) LIMIT 1`, animeID, keyword))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// FeedByName 同番剧下已存在的同名（字幕组）源，大小写不敏感（自动添加重复组跳过用；没有则返回 nil, nil）。
func (s *Store) FeedByName(animeID int64, name string) (*model.Feed, error) {
	f, err := scanFeedRow(s.DB.QueryRow(`SELECT `+feedCols+` FROM feed WHERE anime_id=? AND lower(name)=lower(?) LIMIT 1`, animeID, name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// SetFeedStatus 记录最近抓取时间与错误（空串表示成功）。
func (s *Store) SetFeedStatus(id int64, fetchedAt, errMsg string) error {
	_, err := s.DB.Exec("UPDATE feed SET last_fetched_at=?, last_error=? WHERE id=?", fetchedAt, nullStr(errMsg), id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
