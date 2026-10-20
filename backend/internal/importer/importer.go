package importer

import "anidock/backend/internal/model"

// MetadataImporter 可插拔元数据导入接口（DESIGN §5.2）。
// 新增来源 = 实现该接口 + 注册到 Registry，核心模型与站点逻辑零改动。
type MetadataImporter interface {
	// Name 来源标识："bangumi" | "local-json" | ...
	Name() string
	// Resolve 按 query 解析出番剧草稿。
	// bangumi: query = bangumi_id；local-json: query = 本地 JSON 文件路径（本站 canonical 格式）。
	Resolve(query string) (*model.AnimeDraft, error)
}

// Registry 导入器注册表。
type Registry struct {
	importers map[string]MetadataImporter
}

func NewRegistry(list ...MetadataImporter) *Registry {
	r := &Registry{importers: map[string]MetadataImporter{}}
	for _, imp := range list {
		r.importers[imp.Name()] = imp
	}
	return r
}

func (r *Registry) Get(name string) (MetadataImporter, bool) {
	imp, ok := r.importers[name]
	return imp, ok
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.importers))
	for n := range r.importers {
		names = append(names, n)
	}
	return names
}
