package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store 全部 SQLite 访问的入口。
type Store struct {
	DB *sql.DB
}

// New 打开（不存在则创建）数据库并执行 schema（幂等）。
func New(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建 data 目录: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite 单写者，避免并发写冲突
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	sqlText, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("读取 schema: %w", err)
	}
	if _, err := s.DB.Exec(string(sqlText)); err != nil {
		return fmt.Errorf("执行 schema: %w", err)
	}
	// 存量库兼容：CREATE TABLE IF NOT EXISTS 不会给旧表补新列，逐个 ALTER（忽略"列已存在"）
	migrations := []struct{ table, col, def string }{
		{"anime", "studio", "TEXT"},
		{"anime", "info", "TEXT"},
		{"anime", "characters", "TEXT"},
		{"feed", "mirror", "INTEGER NOT NULL DEFAULT 0"},
		{"release", "mirror_path", "TEXT"},
		{"release", "mirror_size", "INTEGER NOT NULL DEFAULT 0"},
		{"admin_token", "permanent", "INTEGER NOT NULL DEFAULT 0"},
		{"anime_cast", "lead", "INTEGER NOT NULL DEFAULT 0"},
		{"feed", "site", "TEXT"},
		{"feed", "keyword", "TEXT"},
		{"rss_bulk_discovery", "mirror", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, m := range migrations {
		if _, err := s.DB.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", m.table, m.col, m.def)); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("迁移 %s.%s: %w", m.table, m.col, err)
			}
		}
	}
	if err := s.mergeMirrorFeeds(); err != nil {
		return fmt.Errorf("合并镜像源: %w", err)
	}
	if err := s.purgeOrphanReleases(); err != nil {
		return fmt.Errorf("清理孤儿 release: %w", err)
	}
	return nil
}

// purgeOrphanReleases 清理源已被删除的 release（feed_id 指向不存在的 feed）。
// 孤儿行会让列表"有剧集"过滤（查 release 单表）放行，而剧集视图（INNER JOIN feed）
// 显示为空，两个视图互相矛盾。删除源时应级联删 release，此处兜底清理历史残留。
func (s *Store) purgeOrphanReleases() error {
	_, err := s.DB.Exec("DELETE FROM release WHERE feed_id NOT IN (SELECT id FROM feed)")
	return err
}

// mergeMirrorFeeds 把"同一逻辑订阅"的多条镜像 feed 行合并成一条（启动时幂等迁移）：
// 旧版自动建源按站点池逐站物化（同番同关键词每站一行），v0.2.1 起改为一行逻辑源、
// 抓取时按全局站点池展开。合并规则：保留优先级更优（数值小，同值取 id 小）的行，
// 其余行的 release 重定向过来（dedup 键与保留行冲突的跳过，防违反唯一索引），
// 剩余行删除。
func (s *Store) mergeMirrorFeeds() error {
	rows, err := s.DB.Query(`SELECT anime_id, lower(keyword) FROM feed
		WHERE keyword IS NOT NULL AND keyword != ''
		GROUP BY anime_id, lower(keyword) HAVING COUNT(*) > 1`)
	if err != nil {
		return err
	}
	type pair struct{ animeID int64; kw string }
	var pairs []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.animeID, &p.kw); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, p)
	}
	rows.Close()
	for _, p := range pairs {
		ids, err := feedIDsByKeyword(s.DB, p.animeID, p.kw)
		if err != nil {
			return err
		}
		if len(ids) < 2 {
			continue
		}
		kept, others := ids[0], ids[1:]
		for _, oid := range others {
			if _, err := s.DB.Exec(`UPDATE release SET feed_id=? WHERE feed_id=?
				AND NOT EXISTS (SELECT 1 FROM release r2
					WHERE r2.feed_id=? AND r2.dedup=release.dedup AND r2.id<>release.id)`, kept, oid, kept); err != nil {
				return err
			}
		}
		ph := make([]string, len(others))
		args := make([]any, len(others))
		for i, oid := range others {
			ph[i] = "?"
			args[i] = oid
		}
		if _, err := s.DB.Exec(`DELETE FROM feed WHERE id IN (`+strings.Join(ph, ",")+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// feedIDsByKeyword 同番同（小写）关键词的 feed id，按 priority 升序、id 升序。
func feedIDsByKeyword(db *sql.DB, animeID int64, kw string) ([]int64, error) {
	rows, err := db.Query(`SELECT id FROM feed WHERE anime_id=? AND lower(keyword)=? ORDER BY priority ASC, id ASC`, animeID, kw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) Close() error { return s.DB.Close() }

// NowStr 统一时间格式（UTC RFC3339）。
func NowStr() string { return time.Now().UTC().Format(time.RFC3339) }
