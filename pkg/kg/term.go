package kg

import (
	"sort"
	"strings"
)

// TermAssignment 是一条原始文本术语到内部概念的归属。
// Term 保留来源给出的原始写法（大小写、空白原样），解析时按 NormalizeTerm 归一。
type TermAssignment struct {
	// Term 是来源给出的原始文本术语。
	Term string
	// ConceptID 是该术语指向的图谱内部概念。
	ConceptID string
	// Source 是给出该归属的来源。
	Source string
}

// 术语条目的来源类别（该术语与概念的关联是从哪里得来的）。
const (
	// TermOriginConceptName 表示术语来自概念的规范名。
	TermOriginConceptName = "concept_name"
	// TermOriginSynonym 表示术语来自概念的同义词。
	TermOriginSynonym = "synonym"
	// TermOriginMapping 表示术语来自显式术语映射（Dataset.TermMappings）。
	TermOriginMapping = "term_mapping"
)

// termEntry 是术语索引中的一条记录：归一后术语 -> 概念的一次关联。
type termEntry struct {
	// Raw 是归一前的原始写法。
	Raw       string
	ConceptID string
	Source    string
	Origin    string
}

// TermCandidate 是术语解析结果中的一个候选概念及其来源依据。
type TermCandidate struct {
	ConceptID   string
	ConceptName string
	// Raw 是命中时归一前的原始写法（规范名/同义词/映射术语原样）。
	Raw string
	// Normalized 是归一后的术语（与 TermResolution.Normalized 一致）。
	Normalized string
	Source     string
	Origin     string
}

// TermResolution 是一次术语解析的完整结果：归一前后对应关系 + 命中的概念。
type TermResolution struct {
	// Raw 是调用方给出的原始文本。
	Raw string
	// Normalized 是按归一规则得到的规范形式。
	Normalized string
	// Candidates 是命中的全部概念候选（含来源依据），顺序确定。
	Candidates []TermCandidate
	// Conflicted 表示该术语处于冲突状态，候选不可作为确定结论。
	Conflicted bool
}

// TermConflict 描述一条术语到概念的冲突（只标记不拒绝，不替调用方选择）。
type TermConflict struct {
	Type ConflictType
	// Source 仅在同源冲突时非空，指出冲突来源。
	Source string
	// Term 是归一后的术语。
	Term string
	// Candidates 是冲突涉及的全部候选（同源冲突时为该来源的候选），顺序确定。
	Candidates []TermCandidate
}

// NormalizeTerm 是术语归一的唯一入口，规则固定且可预期：
//  1. 去掉首尾 Unicode 空白；
//  2. 中间任意连续 Unicode 空白（空格/制表/换行等）折叠为单个 ASCII 空格；
//  3. 全部字符转 Unicode 小写。
//
// 因此 "Cold"、"cold"、"  cold  "、"Common\tCold" 的归一结果分别为
// "cold"、"cold"、"cold"、"common cold"。归一后为空串的术语不命中任何概念。
func NormalizeTerm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// rebuildTermIndex 从概念（规范名/同义词）与显式术语映射全量重建术语索引，
// 保证索引与快照数据严格一致。结果按键与条目稳定排序。
func rebuildTermIndex(d *snapshotData) {
	d.termIndex = map[string][]termEntry{}

	add := func(raw, conceptID, source, origin string) {
		norm := NormalizeTerm(raw)
		if norm == "" {
			return // 归一后为空的术语不参与解析
		}
		e := termEntry{Raw: raw, ConceptID: conceptID, Source: source, Origin: origin}
		lst := d.termIndex[norm]
		for _, x := range lst {
			if x == e {
				return // 完全相同的条目去重
			}
		}
		d.termIndex[norm] = append(lst, e)
	}

	// 概念规范名与同义词按各自登记来源计入索引。
	ids := make([]string, 0, len(d.concepts))
	for id := range d.concepts {
		ids = append(ids, id)
	}
	sortStrings(ids)
	for _, id := range ids {
		rec := d.concepts[id]
		nameSources := rec.nameSources[rec.concept.Name]
		if len(nameSources) == 0 {
			// 防御：规范名未登记来源时退化为概念的全部来源。
			for s := range rec.sources {
				nameSources = append(nameSources, s)
			}
			sortStrings(nameSources)
		}
		for _, src := range nameSources {
			add(rec.concept.Name, id, src, TermOriginConceptName)
		}
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
			for _, src := range srcs {
				add(syn, id, src, TermOriginSynonym)
			}
		}
	}

	// 显式术语映射（保留原始写法）。
	for _, tm := range d.termMappings {
		add(tm.Term, tm.ConceptID, tm.Source, TermOriginMapping)
	}

	for norm := range d.termIndex {
		lst := d.termIndex[norm]
		sort.SliceStable(lst, func(i, j int) bool { return termEntryLess(lst[i], lst[j]) })
		d.termIndex[norm] = lst
	}
}

// termEntryLess 给出术语条目的确定顺序：概念、来源、来源类别、原始写法。
func termEntryLess(a, b termEntry) bool {
	if a.ConceptID != b.ConceptID {
		return a.ConceptID < b.ConceptID
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	return a.Raw < b.Raw
}

// toCandidates 把索引条目转成候选视图（补规范名），顺序保持输入顺序。
func toCandidates(d *snapshotData, norm string, entries []termEntry) []TermCandidate {
	out := make([]TermCandidate, 0, len(entries))
	for _, e := range entries {
		name := ""
		if rec, ok := d.concepts[e.ConceptID]; ok {
			name = rec.concept.Name
		}
		out = append(out, TermCandidate{
			ConceptID:   e.ConceptID,
			ConceptName: name,
			Raw:         e.Raw,
			Normalized:  norm,
			Source:      e.Source,
			Origin:      e.Origin,
		})
	}
	return out
}

// computeTermConflicts 重新计算全部归一术语的冲突，结果顺序确定。
// 规则：
//   - 同一来源把同一归一术语指向多个概念 -> term_same_source_conflict；
//   - 不同来源把同一归一术语指向不同概念 -> term_cross_source_conflict。
func computeTermConflicts(d *snapshotData) []TermConflict {
	// 先清空再重算，使删除/回滚后的状态与数据严格一致。
	for t := range d.conflictedTerms {
		delete(d.conflictedTerms, t)
	}

	terms := make([]string, 0, len(d.termIndex))
	for t := range d.termIndex {
		terms = append(terms, t)
	}
	sortStrings(terms)

	conflicts := make([]TermConflict, 0)
	for _, t := range terms {
		if c := buildTermConflict(d, t); c != nil {
			d.conflictedTerms[t] = c.Type
			conflicts = append(conflicts, *c)
		}
	}
	return conflicts
}

// buildTermConflict 只读地判定单个归一术语的冲突类型；无冲突返回 nil。
// 同源冲突优先于跨来源冲突报告。
func buildTermConflict(d *snapshotData, norm string) *TermConflict {
	entries := d.termIndex[norm]
	if len(entries) == 0 {
		return nil
	}
	bySource := map[string][]termEntry{}
	conceptSet := map[string]bool{}
	for _, e := range entries {
		bySource[e.Source] = append(bySource[e.Source], e)
		conceptSet[e.ConceptID] = true
	}
	srcs := make([]string, 0, len(bySource))
	for s := range bySource {
		srcs = append(srcs, s)
	}
	sortStrings(srcs)
	for _, src := range srcs {
		uniq := map[string]bool{}
		for _, e := range bySource[src] {
			uniq[e.ConceptID] = true
		}
		if len(uniq) > 1 {
			return &TermConflict{
				Type:       ConflictTermSameSource,
				Source:     src,
				Term:       norm,
				Candidates: toCandidates(d, norm, bySource[src]),
			}
		}
	}
	if len(conceptSet) > 1 {
		return &TermConflict{
			Type:       ConflictTermCrossSource,
			Term:       norm,
			Candidates: toCandidates(d, norm, entries),
		}
	}
	return nil
}
