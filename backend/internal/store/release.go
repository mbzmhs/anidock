package store

import (
	"database/sql"
	"fmt"

	"anidock/backend/internal/model"
)

const releaseCols = `r.id, r.feed_id, r.anime_id, r.title, r.episode, r.url, r.download_url, r.quality,
  r.spec, r.group_name, r.published_at, r.fetched_at,
  COALESCE(r.mirror_path,'') AS mirror_path, COALESCE(r.mirror_size,0) AS mirror_size,
  f.name, f.priority, COALESCE(f.color,'') AS color`

const releaseJoin = `FROM release r JOIN feed f ON f.id = r.feed_id`

func scanRelease(row interface{ Scan(...any) error }) (*model.Release, error) {
	var x model.Release
	var animeID, mirrorSize sql.NullInt64
	var episode sql.NullFloat64
	var url, dl, quality, spec, group, pub, fetched, mirrorPath sql.NullString
	if err := row.Scan(&x.ID, &x.FeedID, &animeID, &x.Title, &episode, &url, &dl, &quality, &spec,
		&group, &pub, &fetched, &mirrorPath, &mirrorSize, &x.FeedName, &x.FeedPriority, &x.FeedColor); err != nil {
		return nil, err
	}
	x.MirrorPath = mirrorPath.String
	x.MirrorSize = mirrorSize.Int64
	_ = fetched
	if animeID.Valid {
		v := animeID.Int64
		x.AnimeID = &v
	}
	if episode.Valid {
		v := episode.Float64
		x.Episode = &v
	}
	x.URL = url.String
	x.DownloadURL = dl.String
	x.Quality = quality.String
	x.Spec = spec.String
	x.GroupName = group.String
	x.PublishedAt = pub.String
	return &x, nil
}

// UpsertRelease 去重插入/更新。无冲突目标（覆盖全部唯一约束）：
//   - (feed_id, dedup)：同源同条目 → 刷新；
//   - (anime_id, episode, group_name)：兄弟站镜像 feed（同组同集，不同站点）
//     写入同一行。**行归属（feed_id）给优先级更优的源**（数值小者优先）——
//     前台"每集取最优"稳定展示高优先级源，不被镜像站的抓取时序带偏；
//     行内容（url/标题等）取最后成功写入者：镜像站内容相同，主站挂掉时
//     备站抓取持续刷新内容，前台无感接管（DESIGN §18.5）。
//
// anime_id：新值优先（COALESCE(excluded, 旧值)）——新一次匹配成功后覆盖旧的空值。
func (s *Store) UpsertRelease(r *model.Release, dedup string) error {
	_, err := s.DB.Exec(`
		INSERT INTO release (feed_id, anime_id, title, episode, url, download_url, quality, spec, group_name, published_at, fetched_at, dedup)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT DO UPDATE SET
			feed_id = CASE
				WHEN (SELECT priority FROM feed WHERE id = excluded.feed_id) <
					 (SELECT priority FROM feed WHERE id = release.feed_id)
				THEN excluded.feed_id
				ELSE release.feed_id
			END,
			anime_id = COALESCE(excluded.anime_id, release.anime_id),
			title = excluded.title,
			episode = excluded.episode,
			url = excluded.url,
			download_url = excluded.download_url,
			quality = excluded.quality,
			spec = excluded.spec,
			group_name = excluded.group_name,
			published_at = excluded.published_at,
			fetched_at = excluded.fetched_at`,
		r.FeedID, nullI64(r.AnimeID), r.Title, nullF64(r.Episode), nullStr(r.URL), nullStr(r.DownloadURL),
		nullStr(r.Quality), nullStr(r.Spec), nullStr(r.GroupName), nullStr(r.PublishedAt), NowStr(), dedup)
	return err
}

// SetReleaseMirror 记录本地镜像文件（校验通过、原子落盘后调用）。
func (s *Store) SetReleaseMirror(id int64, path string, size int64) error {
	_, err := s.DB.Exec("UPDATE release SET mirror_path=?, mirror_size=? WHERE id=?", path, size, id)
	return err
}

// GetReleaseByDedup 按 (feed_id, dedup) 查单条 release（镜像流程用）。
func (s *Store) GetReleaseByDedup(feedID int64, dedup string) (*model.Release, error) {
	row := s.DB.QueryRow(`SELECT id, mirror_path, COALESCE(mirror_size,0) FROM release WHERE feed_id=? AND dedup=?`, feedID, dedup)
	var x model.Release
	var mp sql.NullString
	var ms sql.NullInt64
	if err := row.Scan(&x.ID, &mp, &ms); err != nil {
		return nil, err
	}
	x.MirrorPath = mp.String
	x.MirrorSize = ms.Int64
	return &x, nil
}

// CountMirrorRefs 还有多少 release 引用该镜像文件（删文件前防误删共享文件）。
func (s *Store) CountMirrorRefs(path string) (int, error) {
	var n int
	err := s.DB.QueryRow("SELECT COUNT(*) FROM release WHERE mirror_path=?", path).Scan(&n)
	return n, err
}

// MirrorPathsForAnime 某番剧相关的全部镜像文件名（含其名下源产出的未归类记录）。
func (s *Store) MirrorPathsForAnime(animeID int64) ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT mirror_path FROM release
		WHERE mirror_path IS NOT NULL AND mirror_path != ''
		  AND (anime_id=? OR feed_id IN (SELECT id FROM feed WHERE anime_id=?))`, animeID, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// MirrorPathsForFeed 某源的全部镜像文件名。
func (s *Store) MirrorPathsForFeed(feedID int64) ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT mirror_path FROM release
		WHERE mirror_path IS NOT NULL AND mirror_path != '' AND feed_id=?`, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// BestEpisodes 某番剧"每集取最优"列表（DESIGN §8）：
// 每集展示 feed.priority 最小（同级取 published_at 最新）的一条 release，其余进 alternatives。
func (s *Store) BestEpisodes(animeID int64) ([]*model.EpisodeView, error) {
	rows, err := s.DB.Query(`
		SELECT `+releaseCols+` `+releaseJoin+`
		WHERE r.anime_id = ?
		ORDER BY r.episode ASC, f.priority ASC, r.published_at DESC`, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type group struct {
		episode float64
		haveEp  bool
		best    *model.Release
		alt     []*model.Release
	}
	groups := map[float64]*group{}
	order := []float64{}
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		key := float64(-1) // -1 = 集数未知（哨兵值，避免与真实的第 0 集前篇混组）
		haveEp := false
		if rel.Episode != nil {
			key = *rel.Episode
			haveEp = true
		}
		g, ok := groups[key]
		if !ok {
			g = &group{episode: key, haveEp: haveEp}
			groups[key] = g
			order = append(order, key)
		}
		if g.best == nil {
			g.best = rel
			continue
		}
		// 同集：按 (priority asc, published_at desc) 第一条即最优；这里按传入顺序（已排序）第一条就是最优，
		// 其余都进 alternatives。
		g.alt = append(g.alt, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 无集数的条目按出现顺序排在最后
	var views []*model.EpisodeView
	for _, k := range order {
		views = append(views, &model.EpisodeView{Episode: k, Best: *groups[k].best, Alternatives: toRelSlice(groups[k].alt)})
	}
	return views, nil
}

func toRelSlice(alt []*model.Release) []model.Release {
	out := make([]model.Release, len(alt))
	for i, r := range alt {
		out[i] = *r
	}
	return out
}

// ListLatest 全站最新更新流（按 published_at desc），聚合 feed 信息。
func (s *Store) ListLatest(limit int) ([]*model.Release, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(`SELECT `+releaseCols+` `+releaseJoin+`
		ORDER BY r.published_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.Release
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// ListPending 待归类 release（anime_id 为空），按时间倒序。
func (s *Store) ListPending(limit int) ([]*model.Release, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.Query(`SELECT `+releaseCols+` `+releaseJoin+`
		WHERE r.anime_id IS NULL ORDER BY r.published_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.Release
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// GetRelease 单条 release。
func (s *Store) GetRelease(id int64) (*model.Release, error) {
	row := s.DB.QueryRow(`SELECT `+releaseCols+` `+releaseJoin+` WHERE r.id=?`, id)
	return scanRelease(row)
}

// BindRelease 把 release 绑定到番剧（若带 group+feed，同组同源的其它集一并绑定——"历史 release 一并归属"）。
func (s *Store) BindRelease(releaseID int64, animeID int64, bindSiblings bool) error {
	rel, err := s.GetRelease(releaseID)
	if err != nil {
		return fmt.Errorf("release 不存在: %w", err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE release SET anime_id=? WHERE id=?", animeID, releaseID); err != nil {
		return err
	}
	if bindSiblings && rel.GroupName != "" {
		if _, err := tx.Exec(`UPDATE release SET anime_id=? WHERE feed_id=? AND group_name=? AND anime_id IS NULL`,
			animeID, rel.FeedID, rel.GroupName); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func nullF64(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
