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

// conceptTermEntry 是一条由概念自身定义（当前规范名或同义词）派生的术语归属。
// 与显式登记的 TermAssignment 走同一归一化键空间，但 Derivation 标明派生来源，
// 便于证据链说明该结论来自概念定义而非独立登记。
type conceptTermEntry struct {
	TermAssignment
	// Derivation 取值 "concept_name"（当前规范名）或 "synonym"（同义词）。
	Derivation string
}

// computeConceptNameTerms 从概念记录整体重算派生术语索引，结果顺序确定。
// 收录范围：每个概念的**当前**规范名（rec.concept.Name）与全部同义词；
// 历史曾用名（改名前的旧名仍留在 nameSources 中）不参与解析——
// 改名后旧名不再是该概念的规范写法，其派生结论随改名一并失效。
// 来源依据：规范名取 nameSources[当前名]（为空时退化为该概念全部来源），
// 同义词取登记该同义词的来源集合。同一 (归一键, 概念, 来源) 只保留一条，
// 表面形式保留字典序最小者；概念名优先于同义词。
func computeConceptNameTerms(d *snapshotData) {
	out := map[string][]conceptTermEntry{}

	ids := make([]string, 0, len(d.concepts))
	for id := range d.concepts {
		ids = append(ids, id)
	}
	sortStrings(ids)

	add := func(surface, conceptID, derivation string, sources []string) {
		key := NormalizeTerm(surface)
		if key == "" {
			return // 纯空白名称不构成有效匹配键
		}
		for _, src := range sources {
			out[key] = append(out[key], conceptTermEntry{
				TermAssignment: TermAssignment{
					Term: surface, Normalized: key, ConceptID: conceptID, Source: src,
				},
				Derivation: derivation,
			})
		}
	}

	for _, id := range ids {
		rec := d.concepts[id]
		nameSources := append([]string(nil), rec.nameSources[rec.concept.Name]...)
		if len(nameSources) == 0 {
			// 改名未给出来源等情形下，用概念的全部来源作为依据。
			for s := range rec.sources {
				nameSources = append(nameSources, s)
			}
		}
		sortStrings(nameSources)
		add(rec.concept.Name, id, "concept_name", nameSources)

		syns := make([]string, 0, len(rec.synonyms))
		for syn := range rec.synonyms {
			syns = append(syns, syn)
		}
		sortStrings(syns)
		for _, syn := range syns {
			srcs := make([]string, 0, len(rec.synonyms[syn]))
			for s := range rec.synonyms[syn] {
				srcs = append(srcs, s)
			}
			sortStrings(srcs)
			add(syn, id, "synonym", srcs)
		}
	}

	// 去重与排序：同一 (概念, 来源) 只保留一条，概念名优先于同义词，
	// 表面形式保留字典序最小者，保证索引与输入顺序无关。
	for key, entries := range out {
		sort.SliceStable(entries, func(i, j int) bool {
			a, b := entries[i], entries[j]
			if a.ConceptID != b.ConceptID {
				return a.ConceptID < b.ConceptID
			}
			if a.Source != b.Source {
				return a.Source < b.Source
			}
			if a.Derivation != b.Derivation {
				return a.Derivation < b.Derivation // "concept_name" < "synonym"
			}
			return a.Term < b.Term
		})
		dedup := entries[:0]
		for _, e := range entries {
			if n := len(dedup); n > 0 && dedup[n-1].ConceptID == e.ConceptID &&
				dedup[n-1].Source == e.Source {
				continue
			}
			dedup = append(dedup, e)
		}
		out[key] = dedup
	}
	d.conceptNameTerms = out
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
//
// 匹配分两层，键都是经过 NormalizeTerm 归一的：
//  1. 显式登记的术语归属（termsByKey）：存在命中时优先使用，行为与既往完全一致；
//  2. 概念自身定义派生的归属（conceptNameTerms）：仅当没有任何登记术语命中时
//     作为回退，使概念的当前规范名与同义词（及其大小写/空白变体）无需重复登记
//     即可解析。派生命中的候选同样按 (概念, 来源) 稳定排序、附全部来源依据，
//     并用与登记术语完全相同的规则判定冲突。
//
// 因此用规范名的任意大小写/空白变体查询，结果与用规范写法逐字节一致。
func ResolveTermIn(d *snapshotData, raw string) *TermResolution {
	key := NormalizeTerm(raw)
	if assignments, ok := d.termsByKey[key]; ok && len(assignments) > 0 {
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

	entries, ok := d.conceptNameTerms[key]
	if !ok || len(entries) == 0 {
		return nil
	}
	sorted := append([]conceptTermEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].ConceptID != sorted[j].ConceptID {
			return sorted[i].ConceptID < sorted[j].ConceptID
		}
		return sorted[i].Source < sorted[j].Source
	})
	derived := make([]TermAssignment, 0, len(sorted))
	views := make([]TermView, 0, len(sorted))
	for _, e := range sorted {
		derived = append(derived, e.TermAssignment)
		views = append(views, TermView{Assignment: e.TermAssignment})
	}
	res := &TermResolution{
		Query:           raw,
		NormalizedQuery: key,
		MatchedTerm:     key,
		Candidates:      views,
	}
	if c := termConflictFromAssignments(key, derived); c != nil {
		res.Conflict = c
		for i := range res.Candidates {
			res.Candidates[i].Conflicted = true
		}
	}
	return res
}

// termConflictFor 只读地判定单个归一化术语的冲突类型。
func termConflictFor(d *snapshotData, key string) *TermConflict {
	return termConflictFromAssignments(key, d.termsByKey[key])
}

// termConflictFromAssignments 对给定归属列表按统一规则判定冲突：
// 同源冲突（同一来源指向多个概念）优先于跨来源冲突报告；候选全部保留。
func termConflictFromAssignments(key string, assignments []TermAssignment) *TermConflict {
	assignments = append([]TermAssignment(nil), assignments...)
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
