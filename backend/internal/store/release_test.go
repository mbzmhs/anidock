package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"anidock/backend/internal/model"

	_ "modernc.org/sqlite"
)

// newTestStore 临时目录建库（含全量 schema + 迁移）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func f64p(v float64) *float64 { return &v }

// seedFeed 建一个 feed 行（release 视图 INNER JOIN feed 需要）。
func seedFeed(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.CreateFeed("测试源", "https://example.org/rss-x.xml", "", nil, 1, 1, false, "", "", "")
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	return id
}

// TestBestEpisodesCollection 合集与真实单集互不混组：
//   - "01-12 合集" 与真实第 1 集同组同源 → 各自成行（旧唯一键会互相覆盖）；
//   - 集数未知条目归 -1 组；
//   - 同一合集重复抓取（镜像站）→ upsert 合并为一行。
func TestBestEpisodesCollection(t *testing.T) {
	s := newTestStore(t)
	fid := seedFeed(t, s)
	aid := int64(1)

	rel := func(ep, to *float64, dedup string) *model.Release {
		return &model.Release{FeedID: fid, AnimeID: &aid, Title: "t-" + dedup,
			Episode: ep, EpisodeTo: to, GroupName: "字幕组B", URL: "https://example.org/d/" + dedup}
	}
	mustUpsert(t, s, rel(f64p(1), nil, "ep1"))
	mustUpsert(t, s, rel(f64p(1), f64p(12), "col-1-12")) // 合集与真实第 1 集共存
	mustUpsert(t, s, rel(nil, nil, "unknown"))
	mustUpsert(t, s, rel(f64p(2), f64p(12), "col-2-12"))

	views, err := s.BestEpisodes(aid)
	if err != nil {
		t.Fatalf("BestEpisodes: %v", err)
	}
	var gotEp1, gotCol112, gotCol212, gotUnknown bool
	for _, v := range views {
		switch {
		case v.Episode == 1 && v.EpisodeTo == nil:
			gotEp1 = true
		case v.EpisodeTo != nil && *v.EpisodeTo == 12 && v.Best.Episode != nil && *v.Best.Episode == 1:
			gotCol112 = true
		case v.EpisodeTo != nil && *v.EpisodeTo == 12 && v.Best.Episode != nil && *v.Best.Episode == 2:
			gotCol212 = true
		case v.Episode == -1:
			gotUnknown = true
		}
	}
	if !gotEp1 || !gotCol112 || !gotCol212 || !gotUnknown {
		t.Fatalf("分组缺失: ep1=%v col112=%v col212=%v unknown=%v, views=%+v",
			gotEp1, gotCol112, gotCol212, gotUnknown, views)
	}

	// 同合集再抓一次（同组同区间，不同 dedup 模拟兄弟站镜像）→ 不应新增第 4 行合集
	r2 := rel(f64p(1), f64p(12), "col-1-12-mirror")
	r2.URL = "https://mirror.example.org/d/col-1-12"
	mustUpsert(t, s, r2)
	views, err = s.BestEpisodes(aid)
	if err != nil {
		t.Fatalf("BestEpisodes(2): %v", err)
	}
	colCount := 0
	for _, v := range views {
		if v.EpisodeTo != nil {
			colCount++
		}
	}
	if colCount != 2 {
		t.Fatalf("同区间合集应被唯一键合并（镜像站同组同区间），got %d 行合集", colCount)
	}
}

func mustUpsert(t *testing.T, s *Store, r *model.Release) {
	t.Helper()
	dedup := r.URL
	if err := s.UpsertRelease(r, dedup); err != nil {
		t.Fatalf("UpsertRelease(%s): %v", r.Title, err)
	}
}

// TestMigrateLegacyDB 旧库（无 episode_to 列、旧三列唯一索引）升级：
// 迁移应补列并重建为含合集维度的唯一索引，旧索引不再存在。
func TestMigrateLegacyDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.sqlite")
	// 手工建旧版 release 表结构（v0.2.x 之前无 episode_to）
	legacy := `
	CREATE TABLE feed (
	  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, rss_url TEXT NOT NULL,
	  base_url TEXT, anime_id INTEGER, priority INTEGER NOT NULL DEFAULT 100,
	  enabled INTEGER NOT NULL DEFAULT 1, mirror INTEGER NOT NULL DEFAULT 0,
	  site TEXT, keyword TEXT, color TEXT, last_fetched_at TEXT, last_error TEXT, created_at TEXT
	);
	CREATE TABLE release (
	  id INTEGER PRIMARY KEY AUTOINCREMENT, feed_id INTEGER NOT NULL, anime_id INTEGER,
	  title TEXT NOT NULL, episode REAL, url TEXT, download_url TEXT, quality TEXT,
	  spec TEXT, group_name TEXT, published_at TEXT, fetched_at TEXT, dedup TEXT NOT NULL,
	  mirror_path TEXT, mirror_size INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX uq_release_feed_dedup ON release(feed_id, dedup);
	CREATE UNIQUE INDEX uq_release_anime_ep_group ON release(anime_id, episode, group_name);`
	db, err := openRaw(path)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := db.Exec(legacy); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	db.Close()

	s, err := New(path) // 触发迁移
	if err != nil {
		t.Fatalf("New(legacy): %v", err)
	}
	defer s.Close()

	var idxSQL string
	err = s.DB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='uq_release_anime_ep_group'`).Scan(&idxSQL)
	if err != nil {
		t.Fatalf("迁移后唯一索引应存在: %v", err)
	}
	if !strings.Contains(idxSQL, "episode_to") {
		t.Fatalf("唯一索引应含 episode_to 维度, got: %s", idxSQL)
	}
	// 旧库数据可继续写入合集
	fid, err := s.CreateFeed("旧库源", "https://example.org/rss-y.xml", "", nil, 1, 1, false, "", "", "")
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	aid := int64(9)
	if err := s.UpsertRelease(&model.Release{FeedID: fid, AnimeID: &aid, Title: "合集",
		Episode: f64p(1), EpisodeTo: f64p(12), GroupName: "组X", URL: "https://example.org/d/x"}, "x"); err != nil {
		t.Fatalf("旧库写入合集: %v", err)
	}
}

func openRaw(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
}
