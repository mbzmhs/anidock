package importer

import (
	"encoding/json"
	"fmt"
	"os"

	"anidock/backend/internal/model"
)

// LocalJSON 导入器：读取本地 JSON 文件（本站 canonical 格式，见 DESIGN §5.1），
// 用于手动/离线导入；文件名/路径即 query。
type LocalJSON struct{}

func NewLocalJSON() *LocalJSON { return &LocalJSON{} }

func (l *LocalJSON) Name() string { return "local-json" }

type localAnimeFile struct {
	BangumiID    *int64            `json:"bangumi_id"`
	Title        string            `json:"title"`
	TitleOrig    string            `json:"title_orig"`
	Synopsis     string            `json:"synopsis"`
	Year         int               `json:"year"`
	Quarter      int               `json:"quarter"`
	SeasonLabel  string            `json:"season_label"`
	Genres       []string          `json:"genres"`
	Cover        string            `json:"cover"`
	Poster       string            `json:"poster"`
	Images       []string          `json:"images"`
	Aliases      []string          `json:"aliases"`
	Studio       string            `json:"studio"`
	Info         []model.InfoboxItem `json:"info"`
	Characters   []string          `json:"characters"`
}

func (l *LocalJSON) Resolve(query string) (*model.AnimeDraft, error) {
	path := query
	if path == "" {
		return nil, fmt.Errorf("query 不能为空（本地 JSON 文件路径）")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	var f localAnimeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析 JSON: %w", err)
	}
	if f.Title == "" {
		return nil, fmt.Errorf("JSON 缺少 title 字段")
	}
	return &model.AnimeDraft{
		BangumiID:    f.BangumiID,
		Title:        f.Title,
		TitleOrig:    f.TitleOrig,
		Synopsis:     f.Synopsis,
		Year:         f.Year,
		Quarter:      f.Quarter,
		SeasonLabel:  f.SeasonLabel,
		Genres:       f.Genres,
		Cover:        f.Cover,
		Poster:       f.Poster,
		Images:       f.Images,
		Aliases:      f.Aliases,
		Studio:       f.Studio,
		Info:         f.Info,
		Characters:   f.Characters,
		Source:       "manual",
	}, nil
}
