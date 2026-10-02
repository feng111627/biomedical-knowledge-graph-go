package kg

import "sort"

// Reasoner 在已提交的不可变快照上提供查询与层级传递推导。
// 所有读操作都先取当前快照再计算；写入进行期间读到的也只能是
// 某个完整的旧版本或完整的新版本，绝不会读到半更新状态。
type Reasoner struct {
	store *Store
}

// NewReasoner 创建推理器。
func NewReasoner(store *Store) *Reasoner { return &Reasoner{store: store} }

func (r *Reasoner) snap() *snapshotData {
	return r.store.Current().data
}

// GetConcept 返回概念视图（含同义词与证据）。
func (r *Reasoner) GetConcept(id string) (ConceptView, bool) {
	return ConceptIn(r.snap(), id)
}

// GetConceptIn 在指定快照上返回概念视图。
func (r *Reasoner) GetConceptIn(snap *Snapshot, id string) (ConceptView, bool) {
	return ConceptIn(snap.data, id)
}

// ConceptIn 在给定快照数据上构建概念视图。
func ConceptIn(d *snapshotData, id string) (ConceptView, bool) {
	rec, ok := d.concepts[id]
	if !ok {
		return ConceptView{}, false
	}
	syns := make([]string, 0, len(rec.synonyms))
	for s := range rec.synonyms {
		syns = append(syns, s)
	}
	sortStrings(syns)
	return ConceptView{
		Concept:   rec.concept,
		Synonyms:  syns,
		Evidences: append([]Evidence(nil), rec.evidences...),
	}, true
}

// SearchConcepts 按规范名或同义词（子串匹配）检索概念，按 ID 稳定排序。
func (r *Reasoner) SearchConcepts(text string) []ConceptView {
	d := r.snap()
	ids := make([]string, 0)
	for id, rec := range d.concepts {
		match := containsText(rec.concept.Name, text)
		if !match {
			for syn := range rec.synonyms {
				if containsText(syn, text) {
					match = true
					break
				}
			}
		}
		if match {
			ids = append(ids, id)
		}
	}
	sortStrings(ids)
	out := make([]ConceptView, 0, len(ids))
	for _, id := range ids {
		v, _ := ConceptIn(d, id)
		out = append(out, v)
	}
	return out
}

// Children 返回直接子概念，按 ID 稳定排序。
func (r *Reasoner) Children(id string) []ConceptView {
	d := r.snap()
	return edgesToViews(d, d.childrenOf[id])
}

// Parents 返回直接父概念，按 ID 稳定排序。
func (r *Reasoner) Parents(id string) []ConceptView {
	d := r.snap()
	return edgesToViews(d, d.parentsOf[id])
}

func edgesToViews(d *snapshotData, m map[string]map[string]bool) []ConceptView {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortStrings(ids)
	out := make([]ConceptView, 0, len(ids))
	for _, id := range ids {
		if v, ok := ConceptIn(d, id); ok {
			out = append(out, v)
		}
	}
	return out
}

// Ancestors 返回概念的全部祖先（沿 child->parent 做传递推导）。
// 每个祖先附带一条推导路径；祖先按 ID 排序，路径取字典序最小的最短路径。
func (r *Reasoner) Ancestors(id string) ([]AncestorPath, bool) {
	return AncestorsIn(r.snap(), id)
}

// AncestorsIn 在指定快照上计算祖先。
func AncestorsIn(d *snapshotData, id string) ([]AncestorPath, bool) {
	return traverse(d, id, true)
}

// Descendants 返回概念的全部后代（沿 parent->children 传递推导）。
func (r *Reasoner) Descendants(id string) ([]AncestorPath, bool) {
	return DescendantsIn(r.snap(), id)
}

// DescendantsIn 在指定快照上计算后代。
func DescendantsIn(d *snapshotData, id string) ([]AncestorPath, bool) {
	return traverse(d, id, false)
}

// traverse 执行确定性的 BFS 传递推导。up=true 求祖先，否则求后代。
func traverse(d *snapshotData, start string, up bool) ([]AncestorPath, bool) {
	if _, ok := d.concepts[start]; !ok {
		if _, ok := d.parentsOf[start]; !ok {
			if _, ok2 := d.childrenOf[start]; !ok2 {
				return nil, false
			}
		}
	}

	neighbors := func(node string) []string {
		ns := []string{}
		if up {
			for p := range d.parentsOf[node] {
				ns = append(ns, p)
			}
		} else {
			for c := range d.childrenOf[node] {
				ns = append(ns, c)
			}
		}
		sortStrings(ns)
		return ns
	}

	best := map[string][]string{} // 已确定的最短（且字典序最小）路径
	queue := [][]string{{start}}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		node := path[len(path)-1]
		for _, nb := range neighbors(node) {
			if _, onPath := findInPath(path, nb); onPath {
				continue // 防御性：提交后无环，这里保证推导永不卡死
			}
			next := append(append([]string{}, path...), nb)
			if prev, ok := best[nb]; !ok || pathLess(next, prev) {
				best[nb] = next
				queue = append(queue, next)
			}
		}
	}

	ids := make([]string, 0, len(best))
	for a := range best {
		ids = append(ids, a)
	}
	sortStrings(ids)
	out := make([]AncestorPath, 0, len(ids))
	for _, a := range ids {
		out = append(out, AncestorPath{ConceptID: a, Path: best[a]})
	}
	return out, true
}

// Relations 返回与概念相关的实体关系；relation 为空表示不过滤关系类型。
func (r *Reasoner) Relations(id, relation string) []RelationView {
	d := r.snap()
	seen := map[EntityRelation]bool{}
	merged := make([]EntityRelation, 0)
	for _, x := range d.relationsBySubject[id] {
		if (relation == "" || x.Relation == relation) && !seen[x] {
			seen[x] = true
			merged = append(merged, x)
		}
	}
	for _, x := range d.relationsByObject[id] {
		if (relation == "" || x.Relation == relation) && !seen[x] {
			seen[x] = true
			merged = append(merged, x)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool { return relationLess(merged[i], merged[j]) })
	out := make([]RelationView, 0, len(merged))
	for _, x := range merged {
		out = append(out, RelationView{
			Relation: x,
			Evidences: []Evidence{{Source: x.Source, Kind: "entity_relation",
				Detail: x.Subject + " " + x.Relation + " " + x.Object}},
		})
	}
	return out
}

// ResolveMapping 把外部标识归一到内部概念。
// 无冲突时返回 1 个 MappingView；处于冲突状态时返回全部候选并附带
// MappingConflict（same_source_conflict / cross_source_conflict），不替调用方选择。
func (r *Reasoner) ResolveMapping(targetID string) ([]MappingView, *MappingConflict) {
	d := r.snap()
	assignments, ok := d.mappingsByTarget[targetID]
	if !ok || len(assignments) == 0 {
		return nil, nil
	}
	sorted := append([]MappingAssignment(nil), assignments...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ConceptID != sorted[j].ConceptID {
			return sorted[i].ConceptID < sorted[j].ConceptID
		}
		return sorted[i].Source < sorted[j].Source
	})
	_, conflicted := d.conflictedTargets[targetID]
	views := make([]MappingView, 0, len(sorted))
	for _, m := range sorted {
		views = append(views, MappingView{Assignment: m, Conflicted: conflicted})
	}
	if conflicted {
		if c := mappingConflictFor(d, targetID); c != nil {
			return views, c
		}
	}
	return views, nil
}

// mappingConflictFor 只读地判定单个外部标识的冲突类型。
func mappingConflictFor(d *snapshotData, targetID string) *MappingConflict {
	assignments := append([]MappingAssignment(nil), d.mappingsByTarget[targetID]...)
	sort.SliceStable(assignments, func(i, j int) bool {
		if assignments[i].Source != assignments[j].Source {
			return assignments[i].Source < assignments[j].Source
		}
		return assignments[i].ConceptID < assignments[j].ConceptID
	})
	bySource := map[string][]MappingAssignment{}
	conceptSet := map[string]bool{}
	for _, m := range assignments {
		bySource[m.Source] = append(bySource[m.Source], m)
		conceptSet[m.ConceptID] = true
	}
	srcs := make([]string, 0, len(bySource))
	for s := range bySource {
		srcs = append(srcs, s)
	}
	sortStrings(srcs)
	// 同源冲突优先于跨来源冲突报告。
	for _, src := range srcs {
		uniq := map[string]bool{}
		for _, m := range bySource[src] {
			uniq[m.ConceptID] = true
		}
		if len(uniq) > 1 {
			return &MappingConflict{Type: ConflictSameSource, Source: src, TargetID: targetID, Targets: bySource[src]}
		}
	}
	if len(conceptSet) > 1 {
		return &MappingConflict{Type: ConflictCrossSource, TargetID: targetID, Targets: assignments}
	}
	return nil
}

// Explain 返回一条结论的来源依据链。
// kind 取值：
//   - "concept"  id = 概念ID
//   - "ancestor" id = "child:ancestor"
//   - "descendant" id = "parent:descendant"
//   - "mapping"  id = target_id
//   - "term"     id = 原始或归一化术语（先按原始键，再按归一化键查找）
func (r *Reasoner) Explain(kind, id string) []Evidence {
	d := r.snap()
	switch kind {
	case "concept":
		if rec, ok := d.concepts[id]; ok {
			return append([]Evidence(nil), rec.evidences...)
		}
	case "ancestor":
		child, ancestor := splitPair(id)
		if paths, ok := AncestorsIn(d, child); ok {
			for _, p := range paths {
				if p.ConceptID == ancestor {
					return pathEvidence(d, p.Path)
				}
			}
		}
	case "descendant":
		parent, descendant := splitPair(id)
		if paths, ok := DescendantsIn(d, parent); ok {
			for _, p := range paths {
				if p.ConceptID == descendant {
					return pathEvidence(d, p.Path)
				}
			}
		}
	case "mapping":
		assignments := d.mappingsByTarget[id]
		out := make([]Evidence, 0, len(assignments))
		for _, m := range assignments {
			out = append(out, Evidence{Source: m.Source, Kind: "mapping",
				Detail: m.TargetID + " -> " + m.ConceptID})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Source != out[j].Source {
				return out[i].Source < out[j].Source
			}
			return out[i].Detail < out[j].Detail
		})
		return out
	case "term":
		assignments := d.termsByKey[id]
		if assignments == nil {
			assignments = d.termsByKey[NormalizeTerm(id)]
		}
		out := make([]Evidence, 0, len(assignments))
		for _, t := range assignments {
			out = append(out, Evidence{Source: t.Source, Kind: "term",
				Detail: "'" + t.Normalized + "' -> " + t.ConceptID})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Source != out[j].Source {
				return out[i].Source < out[j].Source
			}
			return out[i].Detail < out[j].Detail
		})
		return out
	}
	return nil
}

func pathEvidence(d *snapshotData, path []string) []Evidence {
	out := make([]Evidence, 0)
	for i := 0; i+1 < len(path); i++ {
		child, parent := path[i], path[i+1]
		sources := make([]string, 0)
		if set := d.parentsOf[child][parent]; set != nil {
			for s := range set {
				sources = append(sources, s)
			}
		}
		sortStrings(sources)
		for _, s := range sources {
			out = append(out, Evidence{Source: s, Kind: "hierarchy_edge",
				Detail: child + " is-a " + parent})
		}
	}
	return out
}

// StaleConclusions 返回当前版本记录的、因概念删除/改名而失效的旧结论。
// 这些结论已不会再出现在正常查询结果中，仅用于审计与向旧快照持有者说明失效原因。
func (r *Reasoner) StaleConclusions() []StalenessInfo {
	d := r.snap()
	out := append([]StalenessInfo(nil), d.invalidations...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].InvalidatedAt != out[j].InvalidatedAt {
			return out[i].InvalidatedAt < out[j].InvalidatedAt
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

// StalenessInfo 描述一条失效结论。
type StalenessInfo struct {
	Kind          string
	SubjectID     string
	Detail        string
	InvalidatedAt int64
	Reason        string
}

func containsText(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func pathLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func findInPath(path []string, node string) (int, bool) {
	for i, x := range path {
		if x == node {
			return i, true
		}
	}
	return -1, false
}

// splitPair 解析 "a:b" 形式的复合标识（约定 ID 内部不含冒号）。
func splitPair(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
