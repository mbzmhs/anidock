package store

import (
	"encoding/json"

	"anidock/backend/internal/model"
)

// SaveAnimeCast 替换保存某部番剧的角色/声优表（幂等，可反复重跑）；空切片 = 清空。
func (s *Store) SaveAnimeCast(animeID int64, rows []model.Cast) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM anime_cast WHERE anime_id=?", animeID); err != nil {
		return err
	}
	for i, r := range rows {
		if _, err := tx.Exec("INSERT INTO anime_cast (anime_id, name, lead, seiyuu, sort) VALUES (?,?,?,?,?)",
			animeID, r.Name, r.Lead, mustJSON(r.Seyuu), i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListAnimeCast 角色/声优列表（按归档中的出场顺序）。
func (s *Store) ListAnimeCast(animeID int64) ([]model.Cast, error) {
	rows, err := s.DB.Query(
		"SELECT name, COALESCE(lead,0), COALESCE(seiyuu,'[]') FROM anime_cast WHERE anime_id=? " +
		"ORDER BY CASE WHEN COALESCE(lead,0) BETWEEN 1 AND 3 THEN lead ELSE 9 END, sort, id", animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Cast
	for rows.Next() {
		var c model.Cast
		var sei string
		if err := rows.Scan(&c.Name, &c.Lead, &sei); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(sei), &c.Seyuu)
		if c.Seyuu == nil {
			c.Seyuu = []model.Seyuu{}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
