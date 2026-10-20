package importer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"anidock/backend/internal/model"
	"anidock/backend/internal/store"
)

// ---------- Bangumi Archive 归档批量导入 ----------
//
// 数据源：bangumi/Archive 的 jsonlines 导出（subject/subject-characters/character/
// person-characters/person）。角色+声优由本地关联产出（v0 API 已不再提供角色数据）。
// 归档里没有图片 URL，封面在导入后按 bangumi id 走 v0 API 拉取（lain.bgm.tv CDN）。
// 去重：库中已存在同一 bangumi_id 的条目一律跳过（可反复重跑；重跑会幂等回填角色/声优）。

// subjChar 某部番剧里的一个角色（cid + 定位 typ）。
type subjChar struct {
	cid int64
	typ int
}

// archiveCand 归档导入候选项（草稿 + 角色/声优，通道间传递）。
type archiveCand struct {
	bid   int64
	draft *model.AnimeDraft
	chars []subjChar // 上限 60（含将被 role 过滤的非角色条目）
	cast  []model.Cast // 角色+声优（本地关联产出，导入/回填 anime_cast 表）
}

// archiveSubject 归档 subject.jsonlines 实际字段。
type archiveSubject struct {
	ID       int64  `json:"id"`
	Type     int    `json:"type"` // 2 = 动画
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Infobox  string `json:"infobox"` // 原始 wiki 字符串（与 API 返回同一套语法）
	Platform int    `json:"platform"`
	Summary  string `json:"summary"`
	Date     string `json:"date"`
	Tags     []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"tags"`
	MetaTags []string `json:"meta_tags"`
}

type archiveSubjectChar struct {
	CharacterID int64 `json:"character_id"`
	SubjectID   int64 `json:"subject_id"`
	Type        int   `json:"type"` // 角色定位：1=主角 2=配角 3=客串（4-6 为扩展值，未标注徽章）
}

type archiveCharacter struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role int    `json:"role"` // 1=角色 2=机体 3=组织 4=地名/国家…（3/4 会混入"帝国"这类非角色条目）
}

// archivePersonChar person-characters.jsonlines：角色 ↔ 声优 关联（type=0 正片，非 0 其它版本）。
type archivePersonChar struct {
	CharacterID int64 `json:"character_id"`
	PersonID    int64 `json:"person_id"`
	Type        int   `json:"type"`
}

// archivePerson person.jsonlines：人物（声优等）。
type archivePerson struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

var platformLabels = map[int]string{1: "TV", 2: "电影", 3: "OVA", 4: "特别篇", 5: "Web"}

// ---------- 原始 wiki infobox 迷你解析器 ----------

var wikiFieldRe = regexp.MustCompile(`^\s*\|\s*([^=|{}]{1,60}?)\s*=\s*(.*)$`)

// parseWikiInfobox 解析 bangumi 原始 wiki infobox 字符串为 key/value 条目。
// 支持：|key= 标量（含多行延续）、|key={ [多值列表] }、|key={ 子项 }（块内行原样并入标量）。
func parseWikiInfobox(raw string) []bangumiInfobox {
	s := strings.ReplaceAll(raw, "\r", "")
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{{"); i >= 0 {
		s = s[i+2:]
	}
	if i := strings.LastIndex(s, "}}"); i >= 0 {
		s = s[:i]
	}
	lines := strings.Split(s, "\n")

	var out []bangumiInfobox
	i := 0
	for i < len(lines) {
		m := wikiFieldRe.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		key := strings.TrimSpace(m[1])
		val := strings.TrimSpace(m[2])
		i++
		scalar, list, ni := wikiCollectValue(lines, i, val)
		i = ni
		item := bangumiInfobox{Key: key}
		if len(list) > 0 {
			// 转成 parseInfobox 认的多值形式 [{v:"…"}]
			arr := make([]interface{}, 0, len(list))
			for _, v := range list {
				arr = append(arr, map[string]interface{}{"v": v})
			}
			item.Value = arr
		} else if scalar != "" {
			item.Value = scalar
		}
		if item.Value == nil {
			continue
		}
		out = append(out, item)
	}
	return out
}

// wikiCollectValue 从 i 开始收集一个字段的取值行，返回 (标量, 列表, 下一行号)。
func wikiCollectValue(lines []string, i int, startVal string) (string, []string, int) {
	var scalar, list []string
	// appendList 剥掉 wiki 列表项的 [ ] 包裹（闭合 [a] 与未闭合 [a 都兼容）
	appendList := func(v string) {
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "[") {
			v = v[1:]
		}
		if strings.HasSuffix(v, "]") {
			v = v[:len(v)-1]
		}
		if v = strings.TrimSpace(v); v != "" {
			list = append(list, v)
		}
	}
	if v := strings.TrimSpace(startVal); v != "" && v != "{" && v != "}" {
		if strings.HasPrefix(v, "[") {
			appendList(v)
		} else {
			scalar = append(scalar, v)
		}
	}
	block := startVal == "{"
	for i < len(lines) {
		tl := strings.TrimSpace(lines[i])
		if !block {
			if tl == "" || tl == "}" || wikiFieldRe.MatchString(lines[i]) {
				break
			}
			if strings.HasPrefix(tl, "{") {
				block = true
				tl = strings.TrimSpace(strings.TrimPrefix(tl, "{"))
				if tl == "}" {
					break
				}
			}
		} else {
			if tl == "}" {
				i++
				break
			}
			if strings.HasPrefix(tl, "{") {
				tl = strings.TrimSpace(strings.TrimPrefix(tl, "{"))
				if tl == "}" {
					i++
					break
				}
			}
		}
		if tl == "" {
			i++
			continue
		}
		if strings.HasPrefix(tl, "[") {
			appendList(tl)
			i++
			continue
		}
		scalar = append(scalar, tl)
		i++
	}
	return strings.TrimSpace(strings.Join(scalar, " ")), list, i
}

// ---------- 年份/季度与 Python repr 解析 ----------

// 兼容 "2008-04-06" / "2008年4月6日" / "2008/4" / "2008"
var archiveYmRe = regexp.MustCompile(`(\d{4})\s*[-/.年]?\s*(\d{1,2})?`)

var archiveDateKeyRes = func() []*regexp.Regexp {
	keys := []string{"放送开始", "放送開始", "播出时间", "播出", "上映", "发行", "发售"}
	out := make([]*regexp.Regexp, len(keys))
	for i, k := range keys {
		out[i] = regexp.MustCompile(`\|\s*` + regexp.QuoteMeta(k) + `\s*=\s*([^\n|{}]+)`)
	}
	return out
}()

// archiveYearQuarter 年份/季度推导：date 字段优先，其次 infobox 首播/发行类字段。
func archiveYearQuarter(date, rawInfobox string) (int, int) {
	if y, q, ok := ymFrom(date); ok {
		return y, q
	}
	for _, re := range archiveDateKeyRes {
		if m := re.FindStringSubmatch(rawInfobox); m != nil {
			if y, q, ok := ymFrom(m[1]); ok {
				return y, q
			}
		}
	}
	return 0, 0
}

func ymFrom(s string) (int, int, bool) {
	m := archiveYmRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, 0, false
	}
	y, _ := strconv.Atoi(m[1])
	if y < 1950 || y > 2100 {
		return 0, 0, false
	}
	q := 0
	if m[2] != "" {
		if mon, _ := strconv.Atoi(m[2]); mon >= 1 && mon <= 12 {
			q = (mon - 1)/3 + 1
		}
	}
	return y, q, true
}

// ---------- 草稿构建（与 API 快照导入 Resolve 的映射保持一致） ----------

// buildArchiveDraft 归档记录 → AnimeDraft（封面留空，导入后另行拉取）。
func buildArchiveDraft(rec *archiveSubject, chars []string) *model.AnimeDraft {
	infoItems, studio, aliasTags := parseInfobox(parseWikiInfobox(rec.Infobox))
	year, quarter := archiveYearQuarter(rec.Date, rec.Infobox)

	aliases := []string{}
	for _, s := range append(append([]string{}, aliasTags...), rec.NameCN, rec.Name) {
		if s == "" {
			continue
		}
		dup := false
		for _, x := range aliases {
			if x == s {
				dup = true
				break
			}
		}
		if !dup {
			aliases = append(aliases, s)
		}
	}

	tagNames := make([]string, 0, len(rec.Tags))
	for _, tg := range rec.Tags {
		if name := strings.TrimSpace(tg.Name); name != "" {
			tagNames = append(tagNames, name)
		}
	}
	if len(tagNames) == 0 {
		for _, m := range rec.MetaTags {
			if m = strings.TrimSpace(m); m != "" {
				tagNames = append(tagNames, m)
			}
		}
	}

	draft := &model.AnimeDraft{
		BangumiID:   &rec.ID,
		Title:       rec.NameCN,
		TitleOrig:   rec.Name,
		Synopsis:    strings.TrimSpace(rec.Summary),
		Year:        year,
		Quarter:     quarter,
		SeasonLabel: seasonLabel(year, quarter),
		Genres:      mergeGenres(tagNames, platformLabels[rec.Platform]),
		Aliases:     aliases,
		Studio:      studio,
		Info:        infoItems,
		Characters:  chars,
		Source:      "bangumi-archive",
	}
	if draft.Title == "" {
		draft.Title = rec.Name
	}
	if draft.Characters == nil {
		draft.Characters = []string{}
	}
	return draft
}

// ---------- 任务状态（单例：同一时间只允许一个导入任务） ----------

type ArchiveJob struct {
	mu      sync.Mutex
	running bool
	phase   string // idle | subjects | characters | insert | covers | done | error
	scanned int
	imported int
	skipped int
	failed  int
	coversTotal, coversDone, coversFailed int
	errMsg  string
	notes   []string
	started time.Time
}

var archiveJob = &ArchiveJob{}

// ArchiveStatusInfo 任务状态快照（前端轮询）。
type ArchiveStatusInfo struct {
	Running      bool     `json:"running"`
	Phase        string   `json:"phase"`
	Scanned      int      `json:"scanned"`
	Imported     int      `json:"imported"`
	Skipped      int      `json:"skipped"`
	Failed       int      `json:"failed"`
	CoversTotal  int      `json:"covers_total"`
	CoversDone   int      `json:"covers_done"`
	CoversFailed int      `json:"covers_failed"`
	Error        string   `json:"error,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	StartedAt    string   `json:"started_at,omitempty"`
}

func (j *ArchiveJob) lock()    { j.mu.Lock() }
func (j *ArchiveJob) unlock()  { j.mu.Unlock() }
func (j *ArchiveJob) setPhase(p string) {
	j.lock()
	j.phase = p
	j.unlock()
}
func (j *ArchiveJob) note(format string, args ...any) {
	j.lock()
	if len(j.notes) < 30 {
		j.notes = append(j.notes, fmt.Sprintf(format, args...))
	}
	j.unlock()
}
func (j *ArchiveJob) addImported(n int) {
	j.lock()
	j.imported += n
	j.unlock()
}
func (j *ArchiveJob) addSkipped(n int) {
	j.lock()
	j.skipped += n
	j.unlock()
}
func (j *ArchiveJob) addCoverDone(ok bool) {
	j.lock()
	j.coversDone++
	if !ok {
		j.coversFailed++
	}
	j.unlock()
}
func (j *ArchiveJob) fail(err error) {
	j.lock()
	j.running = false
	j.phase = "error"
	j.errMsg = err.Error()
	j.unlock()
}

// ArchiveStatus 返回当前导入任务状态快照（前端轮询）。
func ArchiveStatus() ArchiveStatusInfo {
	j := archiveJob
	j.lock()
	defer j.unlock()
	notes := append([]string{}, j.notes...)
	st := ArchiveStatusInfo{
		Running: j.running, Phase: j.phase,
		Scanned: j.scanned, Imported: j.imported, Skipped: j.skipped, Failed: j.failed,
		CoversTotal: j.coversTotal, CoversDone: j.coversDone, CoversFailed: j.coversFailed,
		Error: j.errMsg, Notes: notes,
	}
	if !j.started.IsZero() {
		st.StartedAt = j.started.Format("15:04:05")
	}
	return st
}

// ---------- 导入主流程 ----------

// ArchiveOption 归档批量导入参数。
type ArchiveOption struct {
	Dir         string // Bangumi Archive 目录（含 *.jsonlines）
	YearFrom    int    // 起始年份（含；0 = 不限下界）
	YearTo      int    // 结束年份（含）
	Platforms   []int    // 动画类型多选（1=TV 2=电影 3=OVA 4=特别篇 5=Web）；空 = 不限类型
	Countries   []string // 国家/地区多选（Bangumi 标签名：日本/中国/美国/韩国/欧美…，命中任一即入选）；空 = 不限
	FetchCovers bool   // 导入后拉取封面
}

// platformAllowed 类型是否在允许列表中（allow 为空 = 不限）。
func platformAllowed(allow []int, p int) bool {
	if len(allow) == 0 {
		return true
	}
	for _, x := range allow {
		if x == p {
			return true
		}
	}
	return false
}

// platformNames 已选类型转展示名（notes 用），如 "TV+电影"。
func platformNames(allow []int) string {
	if len(allow) == 0 {
		return "全部类型"
	}
	out := make([]string, 0, len(allow))
	for _, x := range allow {
		out = append(out, platformLabels[x])
	}
	return strings.Join(out, "+")
}

// countryTag 条目的 tags/meta_tags 是否命中任一指定国家/地区标签（"或"关系）。
func countryTag(rec *archiveSubject, want map[string]bool) bool {
	for _, t := range rec.Tags {
		if want[t.Name] {
			return true
		}
	}
	for _, m := range rec.MetaTags {
		if want[m] {
			return true
		}
	}
	return false
}

// StartArchiveImport 启动归档批量导入（后台执行，状态经 ArchiveStatus() 轮询）。
// st 为数据仓库，b 为 Bangumi 适配器（拉封面用，可为 nil 表示不拉封面）。
func StartArchiveImport(st *store.Store, b *Bangumi, opt ArchiveOption) error {
	j := archiveJob
	j.lock()
	if j.running {
		j.unlock()
		return errors.New("已有导入任务在进行")
	}
	// 逐字段重置（不能整体替换 struct，Mutex 归零是 UB）
	j.running = true
	j.phase = "subjects"
	j.scanned, j.imported, j.skipped, j.failed = 0, 0, 0, 0
	j.coversTotal, j.coversDone, j.coversFailed = 0, 0, 0
	j.errMsg = ""
	j.notes = nil
	j.started = time.Now()
	j.unlock()

	go runArchiveImport(st, b, opt)
	return nil
}

func runArchiveImport(st *store.Store, b *Bangumi, opt ArchiveOption) {
	j := archiveJob
	defer func() {
		if r := recover(); r != nil {
			j.fail(fmt.Errorf("导入panic: %v", r))
		}
	}()

	// ---- 通道1：subject.jsonlines 筛选候选动画 ----
	var cands []*archiveCand

	subjFile, err := os.Open(filepath.Join(opt.Dir, "subject.jsonlines"))
	if err != nil {
		j.fail(fmt.Errorf("读取 subject.jsonlines: %w", err))
		return
	}
	// 国家/地区标签过滤集合（为空 = 不限）
	var countryWant map[string]bool
	if len(opt.Countries) > 0 {
		countryWant = make(map[string]bool, len(opt.Countries))
		for _, c := range opt.Countries {
			if c != "" {
				countryWant[c] = true
			}
		}
		if len(countryWant) == 0 {
			countryWant = nil
		}
	}
	sc := bufio.NewScanner(subjFile)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		var rec archiveSubject
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type != 2 {
			continue
		}
		if !platformAllowed(opt.Platforms, rec.Platform) {
			continue
		}
		if countryWant != nil && !countryTag(&rec, countryWant) {
			continue
		}
		year, _ := archiveYearQuarter(rec.Date, rec.Infobox)
		if year < opt.YearFrom || year > opt.YearTo {
			continue
		}
		cands = append(cands, &archiveCand{bid: rec.ID, draft: buildArchiveDraft(&rec, nil)})
	}
	subjFile.Close()
	j.lock()
	j.scanned = len(cands)
	j.unlock()
	filters := "type=2，年份 " + strconv.Itoa(opt.YearFrom) + "-" + strconv.Itoa(opt.YearTo) + "，类型 " + platformNames(opt.Platforms)
	if len(opt.Countries) > 0 {
		filters += "，国家 " + strings.Join(opt.Countries, "+")
	}
	j.note("候选动画 %d 部（%s）", len(cands), filters)
	if len(cands) == 0 {
		j.lock()
		j.running = false
		j.phase = "done"
		j.unlock()
		return
	}

	// ---- 去重：库中已存在的 bangumi_id 跳过 ----
	bids := make([]int64, len(cands))
	for i, c := range cands {
		bids[i] = c.bid
	}
	existing, err := st.ExistingBangumiIDs(bids)
	if err != nil {
		j.fail(fmt.Errorf("查询已有条目: %w", err))
		return
	}
	var fresh []*archiveCand
	for _, c := range cands {
		if _, ok := existing[c.bid]; ok {
			j.addSkipped(1)
			continue
		}
		fresh = append(fresh, c)
	}
	j.note("跳过已存在 %d 部，待导入 %d 部", len(cands)-len(fresh), len(fresh))

	// ---- 通道2：subject-characters.jsonlines → 角色 ID（每部上限 40） ----
	// 对全部候选收集（含已存在的）：重跑导入时对老条目幂等回填角色/声优数据
	j.setPhase("characters")
	allSet := map[int64]*archiveCand{}
	for _, c := range cands {
		allSet[c.bid] = c
	}
	needChars := map[int64]struct{}{}
	// 每部番剧的角色定位表（bid→cid→typ；同一角色多记录时保留最优=主角优先）
	charType := map[int64]map[int64]int{}
	if f, err := os.Open(filepath.Join(opt.Dir, "subject-characters.jsonlines")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
		for sc.Scan() {
			var rc archiveSubjectChar
			if json.Unmarshal(sc.Bytes(), &rc) != nil {
				continue
			}
			c, ok := allSet[rc.SubjectID]
			if !ok {
				continue
			}
			seen := charType[c.bid]
			if t, dup := seen[rc.CharacterID]; dup {
				if rc.Type > 0 && (t <= 0 || rc.Type < t) {
					seen[rc.CharacterID] = rc.Type
				}
				continue
			}
			if len(c.chars) >= 60 { // 宽松上限：后续 role 过滤会剔除组织/地名等非角色条目
				continue
			}
			if seen == nil {
				seen = map[int64]int{}
				charType[c.bid] = seen
			}
			seen[rc.CharacterID] = rc.Type
			c.chars = append(c.chars, subjChar{cid: rc.CharacterID, typ: rc.Type})
			needChars[rc.CharacterID] = struct{}{}
		}
		f.Close()
	} else {
		j.note("警告: 无法读取 subject-characters.jsonlines，本次不导入角色: %v", err)
	}

	// ---- 通道3：character.jsonlines → 角色名（只解析需要的 ID） ----
	// 按 role 过滤：只保留 1=角色、2=机体（有声优的 EVA 类机体）；
	// 3=组织、4=地名/国家 等混在 subject-characters 里的非角色条目一律剔除
	charNames := map[int64]string{}
	if len(needChars) > 0 {
		remaining := len(needChars)
		if f, err := os.Open(filepath.Join(opt.Dir, "character.jsonlines")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
			for sc.Scan() {
				if remaining == 0 {
					break
				}
				var rc archiveCharacter
				if json.Unmarshal(sc.Bytes(), &rc) != nil {
					continue
				}
				if _, ok := needChars[rc.ID]; ok {
					// role=0 视为"字段缺失"保留，避免整表被误清空
					if rc.Role == 0 || rc.Role == 1 || rc.Role == 2 {
						charNames[rc.ID] = rc.Name
					}
					remaining--
				}
			}
			f.Close()
		} else {
			j.note("警告: 无法读取 character.jsonlines，角色名缺失: %v", err)
		}
	}

	// ---- 通道4：person-characters.jsonlines → 角色 → 声优（含版本 type）----
	// 注意：同一"角色↔声优"关系会按 type 记多条；type=0 为本作品（动画正片）配音，
	// 非 0 为其它版本（真人版/配音版等）。同一 person 多 type 时去重并优先保留 0。
	type charPerson struct {
		pid int64
		typ int
	}
	charPersons := map[int64][]charPerson{}
	charPersonSeen := map[int64]map[int64]*charPerson{}
	if len(needChars) > 0 {
		if f, err := os.Open(filepath.Join(opt.Dir, "person-characters.jsonlines")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
			for sc.Scan() {
				var rc archivePersonChar
				if json.Unmarshal(sc.Bytes(), &rc) != nil {
					continue
				}
				if _, ok := needChars[rc.CharacterID]; !ok {
					continue
				}
				seen := charPersonSeen[rc.CharacterID]
				if seen == nil {
					seen = map[int64]*charPerson{}
					charPersonSeen[rc.CharacterID] = seen
				}
				if e, dup := seen[rc.PersonID]; dup {
					if e.typ != 0 && rc.Type == 0 {
						e.typ = 0 // 同一声优同时有正片/其它版本记录 → 归入正片
					}
					continue
				}
				cp := charPerson{pid: rc.PersonID, typ: rc.Type}
				seen[rc.PersonID] = &cp
				charPersons[rc.CharacterID] = append(charPersons[rc.CharacterID], cp)
			}
			f.Close()
		} else {
			j.note("警告: 无法读取 person-characters.jsonlines，声优缺失: %v", err)
		}
	}

	// ---- 通道5：person.jsonlines → 声优名（只解析需要的 ID） ----
	needPersons := map[int64]struct{}{}
	for _, ps := range charPersons {
		for _, p := range ps {
			needPersons[p.pid] = struct{}{}
		}
	}
	personNames := map[int64]string{}
	if len(needPersons) > 0 {
		remaining := len(needPersons)
		if f, err := os.Open(filepath.Join(opt.Dir, "person.jsonlines")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
			for sc.Scan() {
				if remaining == 0 {
					break
				}
				var rc archivePerson
				if json.Unmarshal(sc.Bytes(), &rc) != nil {
					continue
				}
				if _, ok := needPersons[rc.ID]; ok {
					personNames[rc.ID] = rc.Name
					remaining--
				}
			}
			f.Close()
		} else {
			j.note("警告: 无法读取 person.jsonlines，声优名缺失: %v", err)
		}
	}

	// 组装每部的角色/声优，并同步角色名到 draft.characters（旧字段保留）
	for _, c := range cands {
		cast := []model.Cast{}
		names := []string{}
		leadMap := charType[c.bid]
		for _, sc := range c.chars {
			lead := sc.typ
			if t, ok := leadMap[sc.cid]; ok {
				lead = t // 以去重后的最优定位为准
			}
			n := strings.TrimSpace(charNames[sc.cid])
			if n == "" {
				continue
			}
			names = append(names, n)
			// 按名字再去重兜底（不同 person_id 同名）；正片（ver=0）排前，其它版本排后
			main, other := []model.Seyuu{}, []model.Seyuu{}
			seiSeen := map[string]bool{}
			add := func(bucket *[]model.Seyuu, s model.Seyuu) {
				if !seiSeen[s.Name] {
					seiSeen[s.Name] = true
					*bucket = append(*bucket, s)
				}
			}
			for _, cp := range charPersons[sc.cid] {
				pn := strings.TrimSpace(personNames[cp.pid])
				if pn == "" {
					continue
				}
				if cp.typ == 0 {
					add(&main, model.Seyuu{Name: pn, Ver: 0})
				} else {
					add(&other, model.Seyuu{Name: pn, Ver: cp.typ})
				}
			}
			cast = append(cast, model.Cast{Name: n, Lead: lead, Seyuu: append(main, other...)})
		}
		// 主角 → 配角 → 客串 → 未标注（稳定排序，同档内保留归档原始顺序）
		leadRank := func(l int) int {
			if l == 1 || l == 2 || l == 3 {
				return l
			}
			return 9
		}
		sort.SliceStable(cast, func(i, j int) bool { return leadRank(cast[i].Lead) < leadRank(cast[j].Lead) })
		c.cast = cast
		names = names[:0]
		for _, m := range cast {
			names = append(names, m.Name)
		}
		c.draft.Characters = names
	}

	// ---- 批量插入（单事务）----
	if len(fresh) > 0 {
		j.setPhase("insert")
		drafts := make([]*model.AnimeDraft, len(fresh))
		for i, c := range fresh {
			drafts[i] = c.draft
		}
		n, err := st.BulkInsertAnime(drafts)
		if err != nil {
			j.fail(fmt.Errorf("批量插入: %w", err))
			return
		}
		j.addImported(n)
		j.note("已插入 %d 部", n)
	}

	// ---- 回填角色/声优（幂等：新条目 + 重跑时已存在的旧条目都会补齐）----
	// 必须在批量插入提交之后执行：首次导入时新番剧此时才入库，
	// 提前查库会因查不到 bangumi_id 而整体静默跳过（曾导致首导无声优）。
	if idMap, err := st.AnimeIDsByBangumi(bidsOf(cands)); err == nil {
		var saved, total int
		for _, c := range cands {
			if len(c.cast) == 0 {
				continue
			}
			aid, ok := idMap[c.bid]
			if !ok {
				continue
			}
			if err := st.SaveAnimeCast(aid, c.cast); err != nil {
				j.note("警告: 写入角色表失败(anime %d): %v", aid, err)
				continue
			}
			saved++
			total += len(c.cast)
		}
		if saved > 0 {
			j.note("角色/声优：%d 部（共 %d 条角色，含旧条目回填）", saved, total)
		}
	} else {
		j.note("警告: 查番剧ID失败，角色/声优跳过: %v", err)
	}

	// ---- 可选：并发拉取封面（v0 API → large 图 → 落库）----
	// 对全部候选执行（而非仅新增）：SetAnimeCover 幂等，重跑可补上之前失败的封面。
	if opt.FetchCovers && b != nil {
		j.setPhase("covers")
		j.lock()
		j.coversTotal = len(cands)
		j.unlock()
		idMap, err := st.AnimeIDsByBangumi(bidsOf(cands))
		if err != nil {
			j.note("警告: 查番剧ID失败，封面跳过: %v", err)
		} else {
			fetchCovers(b, st, cands, idMap)
		}
	}

	j.lock()
	j.running = false
	j.phase = "done"
	j.unlock()
}

func bidsOf(cands []*archiveCand) []int64 {
	out := make([]int64, len(cands))
	for i, c := range cands {
		out[i] = c.bid
	}
	return out
}

// fetchCovers 并发（6 路）为已导入条目拉取封面，失败静默（封面留空可日后单条补齐）。
func fetchCovers(b *Bangumi, st *store.Store, cands []*archiveCand, idMap map[int64]int64) {
	j := archiveJob
	queue := make(chan int64)
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for bid := range queue {
				aid, ok := idMap[bid]
				if !ok {
					j.addCoverDone(false)
					continue
				}
				if !fetchOneCover(b, st, bid, aid) {
					j.addCoverDone(false)
				} else {
					j.addCoverDone(true)
				}
			}
		}()
	}
	for i := range cands {
		queue <- cands[i].bid
	}
	close(queue)
	wg.Wait()
}

// fetchOneCover v0 API 取 images.large → 下载落地 → 回写封面。
func fetchOneCover(b *Bangumi, st *store.Store, bid, aid int64) bool {
	_, base := b.cred()
	u := fmt.Sprintf("%s/v0/subjects/%d", base, bid)
	resp, err := b.client.Get(u)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return false
	}
	var subj bangumiSubject
	if json.Unmarshal(body, &subj) != nil {
		return false
	}
	coverURL := subj.Images["large"]
	if coverURL == "" {
		coverURL = subj.Images["grande"]
	}
	if coverURL == "" {
		coverURL = subj.Images["medium"]
	}
	if coverURL == "" {
		return false
	}
	local, err := b.download(coverURL)
	if err != nil {
		return false
	}
	return st.SetAnimeCover(aid, local) == nil
}
