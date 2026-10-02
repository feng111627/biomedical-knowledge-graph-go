package kg

import (
	"sort"
	"strings"
)

// NormalizeTerm 是术语解析使用的确定性归一规则：
//  1. 去除首尾空白；
//  2. 把内部连续的任意空白（空格/制表符/换行等）折叠为单个空格；
//  3. 统一转为小写。
//
// 因此 "Cold"、"cold"、"  Cold  "、"COLD" 归一结果相同；
// 该规则无外部依赖、无 locale 行为，相同输入永远得到相同输出。
func NormalizeTerm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// addTerm 在快照数据中登记一条术语归属。
// 匹配键为归一化后的术语；同一 (归一术语, 概念, 来源) 视为重复，只保留一条。
// 同一归属若以多种表面形式出现（如 "Cold" 与 " cold"），保留字典序最小的
// 原始文本，使登记结果与输入顺序无关。
func addTerm(d *snapshotData, t TermAssignment) {
	key := t.Normalized
	if key == "" {
		key = NormalizeTerm(t.Term)
	}
	if key == "" {
		return // 纯空白术语不构成有效登记项
	}
	t.Normalized = key

	upsert := func(xs []TermAssignment) []TermAssignment {
		for i, v := range xs {
			if v.ConceptID == t.ConceptID && v.Source == t.Source {
				if t.Term < v.Term {
					xs[i] = t
				}
				return xs
			}
		}
		return append(xs, t)
	}
	d.termsByKey[key] = upsert(d.termsByKey[key])
	d.conceptTerms[t.ConceptID] = upsert(d.conceptTerms[t.ConceptID])
}

// removeTerm 移除术语归属。匹配条件为 (归一术语, 概念)；
// Source 为空表示移除该术语-概念对的全部来源，否则只移除指定来源。
func removeTerm(d *snapshotData, t TermAssignment) {
	key := t.Normalized
	if key == "" {
		key = NormalizeTerm(t.Term)
	}
	match := func(v TermAssignment) bool {
		if v.Normalized != key || v.ConceptID != t.ConceptID {
			return false
		}
		return t.Source == "" || v.Source == t.Source
	}
	filter := func(xs []TermAssignment) []TermAssignment {
		out := xs[:0]
		for _, v := range xs {
			if !match(v) {
				out = append(out, v)
			}
		}
		return out
	}
	d.termsByKey[key] = filter(d.termsByKey[key])
	if len(d.termsByKey[key]) == 0 {
		delete(d.termsByKey, key)
		delete(d.conflictedTerms, key)
	}
	d.conceptTerms[t.ConceptID] = filter(d.conceptTerms[t.ConceptID])
	if len(d.conceptTerms[t.ConceptID]) == 0 {
		delete(d.conceptTerms, t.ConceptID)
	}
}

// removeTermMatch 从一个归属列表中移除匹配的 (归一术语, 概念[, 来源]) 归属。
func removeTermMatch(xs []TermAssignment, t TermAssignment) []TermAssignment {
	out := xs[:0]
	for _, v := range xs {
		if v.Normalized != t.Normalized || v.ConceptID != t.ConceptID {
			out = append(out, v)
			continue
		}
		if t.Source != "" && v.Source != t.Source {
			out = append(out, v)
		}
	}
	return out
}

// computeTermConflicts 重新计算全部术语的映射冲突，结果顺序确定。
// 规则与外部标识映射一致：
//   - 同一来源把同一个归一化术语指向多个概念 -> same_source_conflict；
//   - 不同来源把同一个归一化术语指向不同概念 -> cross_source_conflict。
//
// 冲突只做标记：全部候选原样保留，不替调用方挑选。
func computeTermConflicts(d *snapshotData) []TermConflict {
	// 先清空再重算，使删除/回滚后的状态与数据严格一致。
	for k := range d.conflictedTerms {
		delete(d.conflictedTerms, k)
	}

	keys := make([]string, 0, len(d.termsByKey))
	for k := range d.termsByKey {
		keys = append(keys, k)
	}
	sortStrings(keys)

	conflicts := make([]TermConflict, 0)
	for _, key := range keys {
		assignments := append([]TermAssignment(nil), d.termsByKey[key]...)
		sort.SliceStable(assignments, func(i, j int) bool {
			if assignments[i].Source != assignments[j].Source {
				return assignments[i].Source < assignments[j].Source
			}
			return assignments[i].ConceptID < assignments[j].ConceptID
		})

		bySource := map[string][]TermAssignment{}
		conceptSet := map[string]bool{}
		for _, a := range assignments {
			bySource[a.Source] = append(bySource[a.Source], a)
			conceptSet[a.ConceptID] = true
		}

		var conflict *TermConflict
		// 同源冲突优先于跨来源冲突报告。
		for _, src := range sortedTermSources(bySource) {
			uniq := map[string]bool{}
			for _, a := range bySource[src] {
				uniq[a.ConceptID] = true
			}
			if len(uniq) > 1 {
				conflict = &TermConflict{
					Type: ConflictSameSource, Source: src, Term: key, Targets: bySource[src],
				}
				break
			}
		}
		if conflict == nil && len(conceptSet) > 1 {
			conflict = &TermConflict{Type: ConflictCrossSource, Term: key, Targets: assignments}
		}
		if conflict != nil {
			d.conflictedTerms[key] = conflict.Type
			conflicts = append(conflicts, *conflict)
		}
	}
	return conflicts
}

func sortedTermSources(m map[string][]TermAssignment) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// ResolveTerm 按原始文本术语解析概念（基于当前已提交快照）。
// 输入先经 NormalizeTerm 归一，再与登记术语的归一化键精确匹配：
// 因此 "Cold"、" cold "、"COLD"、"CoLd" 等写法命中同一条登记术语，
// 结果与使用规范写法完全一致。
// 无命中返回 nil；命中时返回归一前后对应、全部候选；存在冲突时 Conflict 非空，
// 全部候选均标记 Conflicted 且不替调用方挑选。候选与冲突的顺序完全确定。
func (r *Reasoner) ResolveTerm(raw string) *TermResolution {
	return ResolveTermIn(r.snap(), raw)
}

// ResolveTermIn 在指定的不可变快照上解析术语；并发读应先持有快照再调用，
// 以保证同一次判断内概念、术语等视图来自同一个完整版本。
func (r *Reasoner) ResolveTermIn(snap *Snapshot, raw string) *TermResolution {
	return ResolveTermIn(snap.data, raw)
}

// ResolveTermIn 在给定快照数据上执行术语解析。
func ResolveTermIn(d *snapshotData, raw string) *TermResolution {
	key := NormalizeTerm(raw)
	assignments, ok := d.termsByKey[key]
	if !ok || len(assignments) == 0 {
		return nil
	}
	sorted := append([]TermAssignment(nil), assignments...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ConceptID != sorted[j].ConceptID {
			return sorted[i].ConceptID < sorted[j].ConceptID
		}
		return sorted[i].Source < sorted[j].Source
	})
	_, conflicted := d.conflictedTerms[key]
	views := make([]TermView, 0, len(sorted))
	for _, a := range sorted {
		views = append(views, TermView{Assignment: a, Conflicted: conflicted})
	}
	res := &TermResolution{
		Query:           raw,
		NormalizedQuery: key,
		MatchedTerm:     key,
		Candidates:      views,
	}
	if conflicted {
		res.Conflict = termConflictFor(d, key)
	}
	return res
}

// termConflictFor 只读地判定单个归一化术语的冲突类型。
func termConflictFor(d *snapshotData, key string) *TermConflict {
	assignments := append([]TermAssignment(nil), d.termsByKey[key]...)
	sort.SliceStable(assignments, func(i, j int) bool {
		if assignments[i].Source != assignments[j].Source {
			return assignments[i].Source < assignments[j].Source
		}
		return assignments[i].ConceptID < assignments[j].ConceptID
	})
	bySource := map[string][]TermAssignment{}
	conceptSet := map[string]bool{}
	for _, a := range assignments {
		bySource[a.Source] = append(bySource[a.Source], a)
		conceptSet[a.ConceptID] = true
	}
	srcs := make([]string, 0, len(bySource))
	for s := range bySource {
		srcs = append(srcs, s)
	}
	sortStrings(srcs)
	// 同源冲突优先于跨来源冲突报告。
	for _, src := range srcs {
		uniq := map[string]bool{}
		for _, a := range bySource[src] {
			uniq[a.ConceptID] = true
		}
		if len(uniq) > 1 {
			return &TermConflict{Type: ConflictSameSource, Source: src, Term: key, Targets: bySource[src]}
		}
	}
	if len(conceptSet) > 1 {
		return &TermConflict{Type: ConflictCrossSource, Term: key, Targets: assignments}
	}
	return nil
}
