package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"anidock/backend/internal/model"
)

// animeCols 可空 TEXT 列统一 COALESCE，避免 NULL→string Scan 失败。
const animeCols = `id, bangumi_id, title, COALESCE(title_orig,'') AS title_orig,
  COALESCE(synopsis,'') AS synopsis, year, quarter, COALESCE(season_label,'') AS season_label,
  genres, COALESCE(cover,'') AS cover, COALESCE(poster,'') AS poster, images, aliases,
  COALESCE(studio,'') AS studio, info, characters,
  COALESCE(source,'') AS source, COALESCE(imported_at,'') AS imported_at, COALESCE(updated_at,'') AS updated_at`

func scanAnime(row interface{ Scan(...any) error }) (*model.Anime, error) {
	var a model.Anime
	var bangumiID sql.NullInt64
	var genres, images, aliases, info, chars sql.NullString
	var year, quarter sql.NullInt64
	err := row.Scan(&a.ID, &bangumiID, &a.Title, &a.TitleOrig, &a.Synopsis, &year, &quarter,
		&a.SeasonLabel, &genres, &a.Cover, &a.Poster, &images, &aliases, &a.Studio, &info, &chars,
		&a.Source, &a.ImportedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if bangumiID.Valid {
		v := bangumiID.Int64
		a.BangumiID = &v
	}
	a.Year = int(year.Int64)
	a.Quarter = int(quarter.Int64)
	_ = json.Unmarshal([]byte(genres.String), &a.Genres)
	_ = json.Unmarshal([]byte(images.String), &a.Images)
	_ = json.Unmarshal([]byte(aliases.String), &a.Aliases)
	_ = json.Unmarshal([]byte(info.String), &a.Info)
	_ = json.Unmarshal([]byte(chars.String), &a.Characters)
	if a.Genres == nil {
		a.Genres = []string{}
	}
	if a.Images == nil {
		a.Images = []string{}
	}
	if a.Aliases == nil {
		a.Aliases = []string{}
	}
	if a.Info == nil {
		a.Info = []model.InfoboxItem{}
	}
	if a.Characters == nil {
		a.Characters = []string{}
	}
	return &a, nil
}

// mergeUnique 去重合并（保序），用于"重导入时保留人工补充的别名"。
func mergeUnique(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// animeFilterConds 筛选条件（ListAnime / CountAnime 共用）。
func animeFilterConds(f model.AnimeFilter) ([]string, []any) {
	var conds []string
	var args []any
	if f.Year > 0 {
		conds = append(conds, "year = ?")
		args = append(args, f.Year)
	}
	if f.Quarter > 0 {
		conds = append(conds, "quarter = ?")
		args = append(args, f.Quarter)
	}
	if f.Q != "" {
		like := "%" + f.Q + "%"
		conds = append(conds, "(title LIKE ? OR title_orig LIKE ? OR instr(aliases, ?) > 0 OR instr(genres, ?) > 0)")
		args = append(args, like, like, f.Q, f.Q)
	}
	if f.HasEps {
		// join feed：孤儿 release（源已删除）不应算"有剧集"，与剧集视图口径一致
		conds = append(conds, "EXISTS (SELECT 1 FROM release r JOIN feed f ON f.id = r.feed_id WHERE r.anime_id = anime.id)")
	}
	if f.HasFeeds {
		// "有 RSS 源"口径与 ListFeedsForAnime 一致：feed.anime_id 直接关联，
		// 或该源的更新已归属到本番剧（anime_id 为空的 per-show 源靠 release 关联）
		conds = append(conds, "EXISTS (SELECT 1 FROM feed f WHERE f.anime_id = anime.id) OR EXISTS (SELECT 1 FROM release r WHERE r.anime_id = anime.id)")
	}
	if len(f.Ids) > 0 {
		// 按 id 批量筛选（外部客户端）：IN 占位符按数量生成
		ph := make([]string, len(f.Ids))
		for i, id := range f.Ids {
			ph[i] = "?"
			args = append(args, id)
		}
		conds = append(conds, "anime.id IN ("+strings.Join(ph, ",")+")")
	}
	return conds, args
}

// ListAnime 番剧列表 + 每部"当前展示组/最新集/更新时间"摘要（动态来自 release）。
// limit > 0 时启用分页（offset 为跳过条数）。
func (s *Store) ListAnime(f model.AnimeFilter, limit, offset int) ([]*model.Anime, error) {
	conds, args := animeFilterConds(f)
	q := "SELECT " + animeCols + " FROM anime"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	// 排序在服务端做（分页后客户端只能拿到当前页，不能再本地排序）
	if f.Sort == "latest" {
		q += " ORDER BY COALESCE((SELECT MAX(r.published_at) FROM release r WHERE r.anime_id = anime.id), '') DESC, id DESC"
	} else {
		q += " ORDER BY year DESC, quarter DESC, id DESC"
	}
	if limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.Anime
	for rows.Next() {
		a, err := scanAnime(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.fillAnimeSummary(list)
	return list, nil
}

// CountAnime 匹配筛选条件的番剧总数（分页用）。
func (s *Store) CountAnime(f model.AnimeFilter) (int, error) {
	conds, args := animeFilterConds(f)
	q := "SELECT COUNT(*) FROM anime"
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// fillAnimeSummary 为每部番剧聚合：最新集、展示的字幕组（最新集上按优先级最优）、更新时间、集数。
func (s *Store) fillAnimeSummary(list []*model.Anime) {
	if len(list) == 0 {
		return
	}
	type rel struct {
		animeID   int64
		feedID    int64
		priority  int
		episode   float64
		hasEp     bool
		group     string
		color     string
		published string
	}
	// 展示名统一用手动维护的 feed.name（不用 release.group_name——那是从 RSS 标题
	// 【...】前缀自动解析的，会与用户填的源名不一致）
	rows, err := s.DB.Query(`
		SELECT r.anime_id, r.feed_id, f.priority, COALESCE(r.episode, -1),
			COALESCE(f.name,''), COALESCE(f.color,''), COALESCE(r.published_at,'')
		FROM release r JOIN feed f ON f.id = r.feed_id
		WHERE r.anime_id IS NOT NULL`)
	if err != nil {
		return
	}
	defer rows.Close()
	rels := map[int64][]rel{}
	for rows.Next() {
		var x rel
		if err := rows.Scan(&x.animeID, &x.feedID, &x.priority, &x.episode, &x.group, &x.color, &x.published); err != nil {
			continue
		}
		rels[x.animeID] = append(rels[x.animeID], x)
	}
	rows.Close()

	// 每部番剧名下的 RSS 源数量（per-show feed）
	feedRows, err := s.DB.Query("SELECT anime_id, COUNT(*) FROM feed WHERE anime_id IS NOT NULL GROUP BY anime_id")
	if err == nil {
		defer feedRows.Close()
		feedCnt := map[int64]int{}
		for feedRows.Next() {
			var aid int64
			var n int
			if feedRows.Scan(&aid, &n) == nil {
				feedCnt[aid] = n
			}
		}
		for _, a := range list {
			a.FeedCount = feedCnt[a.ID]
		}
	}

	for _, a := range list {
		seenEp := map[float64]bool{} // 每集只算一次（多源同集不重复计数，与剧集页"每集取最优"口径一致）
		for _, r := range rels[a.ID] {
			if r.episode >= 0 {
				if !seenEp[r.episode] {
					seenEp[r.episode] = true
					a.EpisodeCount++
				}
				if r.episode > a.LatestEpisode {
					a.LatestEpisode = r.episode
				}
			}
			if r.published > a.LatestUpdatedAt {
				a.LatestUpdatedAt = r.published
				a.LatestGroup = r.group
				a.LatestGroupColor = r.color
			}
		}
	}
}

// GetAnime 单部番剧详情（含摘要 + 角色/声优）。
func (s *Store) GetAnime(id int64) (*model.Anime, error) {
	a, err := s.QueryAnimeByID(id)
	if err != nil {
		return nil, err
	}
	s.fillAnimeSummary([]*model.Anime{a})
	if cast, err := s.ListAnimeCast(id); err == nil {
		a.Cast = cast
	}
	return a, nil
}

func (s *Store) QueryAnimeByID(id int64) (*model.Anime, error) {
	row := s.DB.QueryRow("SELECT "+animeCols+" FROM anime WHERE id = ?", id)
	return scanAnime(row)
}

// CreateAnime 插入（draft 来自导入器或手动）。若 bangumi_id 已存在则更新为快照刷新。
func (s *Store) CreateAnime(d *model.AnimeDraft) (int64, error) {
	if d.Title == "" {
		return 0, fmt.Errorf("title 不能为空")
	}
	if d.BangumiID != nil {
		if a, err := s.ByBangumiID(*d.BangumiID); err == nil {
			a.Title, a.TitleOrig, a.Synopsis = d.Title, d.TitleOrig, d.Synopsis
			a.Year, a.Quarter, a.SeasonLabel = d.Year, d.Quarter, d.SeasonLabel
			a.Genres, a.Cover, a.Poster, a.Images = d.Genres, d.Cover, d.Poster, d.Images
			if d.Studio != "" {
				a.Studio = d.Studio
			}
			if len(d.Info) > 0 {
				a.Info = d.Info
			}
			if len(d.Characters) > 0 {
				a.Characters = d.Characters
			}
			a.Aliases = mergeUnique(a.Aliases, d.Aliases) // 保留人工补充的别名
			a.UpdatedAt = NowStr()
			if err := s.UpdateAnime(a); err != nil {
				return 0, err
			}
			return a.ID, nil
		}
	}
	now := NowStr()
	res, err := s.DB.Exec(`INSERT INTO anime
		(bangumi_id, title, title_orig, synopsis, year, quarter, season_label, genres, cover, poster, images, aliases, studio, info, characters, source, imported_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nullI64(d.BangumiID), d.Title, nullStr(d.TitleOrig), nullStr(d.Synopsis), d.Year, d.Quarter, nullStr(d.SeasonLabel),
		mustJSON(d.Genres), nullStr(d.Cover), nullStr(d.Poster), mustJSON(d.Images), mustJSON(d.Aliases),
		nullStr(d.Studio), mustJSON(d.Info), mustJSON(d.Characters),
		d.Source, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAnime 更新可编辑字段（含别名/制作信息）。
func (s *Store) UpdateAnime(a *model.Anime) error {
	_, err := s.DB.Exec(`UPDATE anime SET
		bangumi_id=?, title=?, title_orig=?, synopsis=?, year=?, quarter=?, season_label=?,
		genres=?, cover=?, poster=?, images=?, aliases=?, studio=?, info=?, characters=?, source=? WHERE id=?`,
		nullI64(a.BangumiID), a.Title, nullStr(a.TitleOrig), nullStr(a.Synopsis), a.Year, a.Quarter, nullStr(a.SeasonLabel),
		mustJSON(a.Genres), nullStr(a.Cover), nullStr(a.Poster), mustJSON(a.Images), mustJSON(a.Aliases),
		nullStr(a.Studio), mustJSON(a.Info), mustJSON(a.Characters), a.Source, a.ID)
	if err != nil {
		return err
	}
	a.UpdatedAt = NowStr()
	_, err = s.DB.Exec("UPDATE anime SET updated_at=? WHERE id=?", a.UpdatedAt, a.ID)
	return err
}

// CountImageRefs 统计除 exceptID 外还有多少番剧引用了该图片路径（共享图保护）。
func (s *Store) CountImageRefs(path string, exceptID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM anime WHERE id <> ? AND (cover = ? OR poster = ? OR instr(images, ?) > 0)`,
		exceptID, path, path, path).Scan(&n)
	return n, err
}

// DeleteAnime 删除番剧，连同其名下的 RSS 源及全部更新记录
// （per-show feed：番剧删了，源也没有意义）。
func (s *Store) DeleteAnime(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM release WHERE anime_id=? OR feed_id IN (SELECT id FROM feed WHERE anime_id=?)", id, id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM anime_feed WHERE anime_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM feed WHERE anime_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM rss_discovery WHERE anime_id=?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM anime WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ByBangumiID(bangumiID int64) (*model.Anime, error) {
	row := s.DB.QueryRow("SELECT "+animeCols+" FROM anime WHERE bangumi_id=?", bangumiID)
	return scanAnime(row)
}

// ExistingBangumiIDs 已存在于库中的 Bangumi ID 集合（归档批量导入去重用）。
func (s *Store) ExistingBangumiIDs(ids []int64) (map[int64]struct{}, error) {
	out := map[int64]struct{}{}
	for i := 0; i < len(ids); i += 500 {
		end := min(i+500, len(ids))
		chunk := ids[i:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for j, id := range chunk {
			args[j] = id
		}
		rows, err := s.DB.Query("SELECT bangumi_id FROM anime WHERE bangumi_id IN ("+ph+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err == nil {
				out[id] = struct{}{}
			}
		}
		rows.Close()
	}
	return out, nil
}

// AnimeIDsByBangumi 按 Bangumi ID 批量取本地番剧 ID（导入后回写封面用）。
func (s *Store) AnimeIDsByBangumi(ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for i := 0; i < len(ids); i += 500 {
		end := min(i+500, len(ids))
		chunk := ids[i:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for j, id := range chunk {
			args[j] = id
		}
		rows, err := s.DB.Query("SELECT bangumi_id, id FROM anime WHERE bangumi_id IN ("+ph+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var bid, aid int64
			if err := rows.Scan(&bid, &aid); err == nil {
				out[bid] = aid
			}
		}
		rows.Close()
	}
	return out, nil
}

// BulkInsertAnime 批量插入（归档批量导入），单事务，列与 CreateAnime 完全一致。
func (s *Store) BulkInsertAnime(d []*model.AnimeDraft) (int, error) {
	if len(d) == 0 {
		return 0, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := NowStr()
	const ins = `INSERT INTO anime
		(bangumi_id, title, title_orig, synopsis, year, quarter, season_label, genres, cover, poster, images, aliases, studio, info, characters, source, imported_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	n := 0
	for _, x := range d {
		if _, err := tx.Exec(ins,
			nullI64(x.BangumiID), x.Title, nullStr(x.TitleOrig), nullStr(x.Synopsis), x.Year, x.Quarter, nullStr(x.SeasonLabel),
			mustJSON(x.Genres), nullStr(x.Cover), nullStr(x.Poster), mustJSON(x.Images), mustJSON(x.Aliases),
			nullStr(x.Studio), mustJSON(x.Info), mustJSON(x.Characters),
			x.Source, now, now); err != nil {
			return 0, err
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// SetAnimeCover 回写封面（批量导入拉取封面后落库）。
// 只写 cover，不动 images：两者同值时详情页会把同一张图渲染两遍。
func (s *Store) SetAnimeCover(id int64, cover string) error {
	_, err := s.DB.Exec("UPDATE anime SET cover=? WHERE id=?", cover, id)
	return err
}

// MissingCover 缺封面但带 Bangumi ID 的番剧（可走 API 补拉）。
type MissingCover struct {
	ID        int64
	BangumiID int64
}

// AnimeMissingCovers 返回所有"有 bangumi_id 但封面为空"的番剧。
func (s *Store) AnimeMissingCovers() ([]MissingCover, error) {
	rows, err := s.DB.Query(
		"SELECT id, bangumi_id FROM anime WHERE bangumi_id IS NOT NULL AND (cover IS NULL OR cover = '')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MissingCover
	for rows.Next() {
		var m MissingCover
		if err := rows.Scan(&m.ID, &m.BangumiID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AllAnime 全量（RSS 匹配用）。
func (s *Store) AllAnime() ([]*model.Anime, error) {
	rows, err := s.DB.Query("SELECT " + animeCols + " FROM anime")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.Anime
	for rows.Next() {
		a, err := scanAnime(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullI64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
