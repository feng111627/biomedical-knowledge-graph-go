package kg

import "sort"

// buildFromDataset 以 base 为起点导入一个完整数据集（导入即全量替换概念/边/关系/映射）。
// 返回的快照数据仍需经过 validateData 校验。
func buildFromDataset(base *snapshotData, version int64, ds Dataset) (*snapshotData, []*ValidationError, []MappingConflict, []TermConflict) {
	d := newSnapshotData(version)
	verrs := make([]*ValidationError, 0)

	// 概念：同来源重复定义且名称不一致 -> duplicate_conflict；
	// 跨来源同名合并为一个概念，同义词与证据取并集。
	// 跨来源同 ID 不同名 -> duplicate_conflict（无法自行判定为同义词）。
	sort.SliceStable(ds.Concepts, func(i, j int) bool {
		a, b := ds.Concepts[i], ds.Concepts[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Name < b.Name
	})
	for _, c := range ds.Concepts {
		if err := upsertConcept(d, c); err != nil {
			verrs = append(verrs, err)
		}
	}

	for _, h := range ds.Hierarchy {
		addHierarchy(d, h)
	}
	for _, r := range ds.Relations {
		addRelation(d, r)
	}
	for _, m := range ds.Mappings {
		addMapping(d, m)
	}
	for _, tm := range ds.TermMappings {
		addTermMapping(d, tm)
	}

	conflicts := computeMappingConflicts(d)
	rebuildTermIndex(d)
	termConflicts := computeTermConflicts(d)
	return d, verrs, conflicts, termConflicts
}

// buildFromRevision 以 base 为起点应用增量修订。
func buildFromRevision(base *snapshotData, version int64, rev Revision) (*snapshotData, []*ValidationError, []MappingConflict, []TermConflict) {
	d := base.clone(version)
	verrs := make([]*ValidationError, 0)

	for _, id := range rev.DeleteConcepts {
		deleteConcept(d, id)
	}
	for _, rn := range rev.RenameConcepts {
		if rec, ok := d.concepts[rn.ID]; ok {
			d.invalidations = append(d.invalidations, StalenessInfo{
				Kind: "rename", SubjectID: rn.ID,
				Detail:        "concept " + rn.ID + " renamed: '" + rec.concept.Name + "' -> '" + rn.NewName + "'",
				InvalidatedAt: version,
				Reason:        "concept_renamed",
			})
			// 旧规范名对应的术语结论随之失效（同义词/显式映射不受影响）。
			d.invalidations = append(d.invalidations, StalenessInfo{
				Kind: "term", SubjectID: rn.ID,
				Detail: "term '" + rec.concept.Name + "' -> " + rn.ID +
					" invalidated because concept renamed to '" + rn.NewName + "'",
				InvalidatedAt: version,
				Reason:        "concept_renamed",
			})
			rec.concept.Name = rn.NewName
			if rn.Source != "" {
				rec.sources[rn.Source] = true
				rec.nameSources[rn.NewName] = appendUnique(rec.nameSources[rn.NewName], rn.Source)
			}
		} else {
			verrs = append(verrs, &ValidationError{
				Reason: RejectDangling, Message: "rename target concept does not exist",
				Entities: []string{rn.ID},
			})
		}
	}
	for _, c := range rev.UpsertConcepts {
		if err := upsertConcept(d, c); err != nil {
			verrs = append(verrs, err)
		}
	}
	for _, h := range rev.DeleteHierarchy {
		removeHierarchy(d, h)
	}
	for _, h := range rev.UpsertHierarchy {
		addHierarchy(d, h)
	}
	for _, r := range rev.DeleteRelations {
		removeRelation(d, r)
	}
	for _, r := range rev.UpsertRelations {
		addRelation(d, r)
	}
	for _, m := range rev.DeleteMappings {
		removeMapping(d, m)
	}
	for _, m := range rev.UpsertMappings {
		addMapping(d, m)
	}
	for _, tm := range rev.DeleteTermMappings {
		removeTermMapping(d, tm)
	}
	for _, tm := range rev.UpsertTermMappings {
		addTermMapping(d, tm)
	}

	conflicts := computeMappingConflicts(d)
	rebuildTermIndex(d)
	termConflicts := computeTermConflicts(d)
	return d, verrs, conflicts, termConflicts
}

func upsertConcept(d *snapshotData, c Concept) *ValidationError {
	rec, exists := d.concepts[c.ID]
	if !exists {
		rec = &conceptRecord{
			concept:     Concept{ID: c.ID, Name: c.Name},
			nameSources: map[string][]string{},
			synonyms:    map[string]map[string]bool{},
			sources:     map[string]bool{},
		}
		d.concepts[c.ID] = rec
	}
	// 同来源重复定义：名称必须一致。
	if prev := rec.nameSources; len(prev) > 0 {
		if !containsString(prev[c.Name], c.Source) {
			for name, sources := range prev {
				if name == c.Name || !containsString(sources, c.Source) {
					continue
				}
				return &ValidationError{
					Reason: RejectDuplicateConflict,
					Message: "concept " + c.ID + " is defined with conflicting names by source " +
						c.Source + ": '" + name + "' vs '" + c.Name + "'",
					Entities: []string{c.ID},
				}
			}
		}
	}
	// 跨来源同 ID 不同名：视为互斥定义。
	if exists && rec.concept.Name != c.Name {
		allowed := rec.synonyms[c.Name] != nil // 新名称是已知同义词时允许合并
		if !allowed {
			return &ValidationError{
				Reason: RejectDuplicateConflict,
				Message: "concept " + c.ID + " has conflicting cross-source names: '" +
					rec.concept.Name + "' vs '" + c.Name + "' (source " + c.Source + ")",
				Entities: []string{c.ID},
			}
		}
	}
	if !exists {
		rec.concept.Name = c.Name
	}
	rec.sources[c.Source] = true
	rec.nameSources[c.Name] = appendUnique(rec.nameSources[c.Name], c.Source)
	rec.evidences = appendUniqueEvidence(rec.evidences, Evidence{
		Source: c.Source, Kind: "concept_definition", Detail: "name=" + c.Name,
	})
	for _, syn := range c.Synonyms {
		if rec.synonyms[syn] == nil {
			rec.synonyms[syn] = map[string]bool{}
		}
		rec.synonyms[syn][c.Source] = true
		rec.evidences = appendUniqueEvidence(rec.evidences, Evidence{
			Source: c.Source, Kind: "synonym", Detail: "synonym=" + syn,
		})
	}
	return nil
}

func addHierarchy(d *snapshotData, h HierarchyEdge) {
	if d.childrenOf[h.Parent] == nil {
		d.childrenOf[h.Parent] = map[string]map[string]bool{}
	}
	if d.childrenOf[h.Parent][h.Child] == nil {
		d.childrenOf[h.Parent][h.Child] = map[string]bool{}
	}
	d.childrenOf[h.Parent][h.Child][h.Source] = true
	if d.parentsOf[h.Child] == nil {
		d.parentsOf[h.Child] = map[string]map[string]bool{}
	}
	if d.parentsOf[h.Child][h.Parent] == nil {
		d.parentsOf[h.Child][h.Parent] = map[string]bool{}
	}
	d.parentsOf[h.Child][h.Parent][h.Source] = true
}

func removeHierarchy(d *snapshotData, h HierarchyEdge) {
	removeEdgeSource(d.childrenOf, h.Parent, h.Child, h.Source)
	removeEdgeSource(d.parentsOf, h.Child, h.Parent, h.Source)
}

func removeEdgeSource(m map[string]map[string]map[string]bool, a, b, source string) {
	inner, ok := m[a]
	if !ok {
		return
	}
	set, ok := inner[b]
	if !ok {
		return
	}
	if source == "" {
		delete(inner, b)
	} else {
		delete(set, source)
		if len(set) == 0 {
			delete(inner, b)
		}
	}
	if len(inner) == 0 {
		delete(m, a)
	}
}

func addRelation(d *snapshotData, r EntityRelation) {
	if !containsRelation(d.relationsBySubject[r.Subject], r) {
		d.relationsBySubject[r.Subject] = append(d.relationsBySubject[r.Subject], r)
		sort.SliceStable(d.relationsBySubject[r.Subject], func(i, j int) bool {
			return relationLess(d.relationsBySubject[r.Subject][i], d.relationsBySubject[r.Subject][j])
		})
	}
	if !containsRelation(d.relationsByObject[r.Object], r) {
		d.relationsByObject[r.Object] = append(d.relationsByObject[r.Object], r)
		sort.SliceStable(d.relationsByObject[r.Object], func(i, j int) bool {
			return relationLess(d.relationsByObject[r.Object][i], d.relationsByObject[r.Object][j])
		})
	}
}

func removeRelation(d *snapshotData, r EntityRelation) {
	d.relationsBySubject[r.Subject] = removeRelationMatch(d.relationsBySubject[r.Subject], r)
	d.relationsByObject[r.Object] = removeRelationMatch(d.relationsByObject[r.Object], r)
}

func addMapping(d *snapshotData, m MappingAssignment) {
	if !containsMapping(d.mappingsByTarget[m.TargetID], m) {
		d.mappingsByTarget[m.TargetID] = append(d.mappingsByTarget[m.TargetID], m)
	}
	if !containsMapping(d.conceptMappings[m.ConceptID], m) {
		d.conceptMappings[m.ConceptID] = append(d.conceptMappings[m.ConceptID], m)
	}
}

func removeMapping(d *snapshotData, m MappingAssignment) {
	d.mappingsByTarget[m.TargetID] = removeMappingMatch(d.mappingsByTarget[m.TargetID], m)
	d.conceptMappings[m.ConceptID] = removeMappingMatch(d.conceptMappings[m.ConceptID], m)
}

// addTermMapping 添加一条显式术语映射（按原始写法去重，保持确定顺序）。
func addTermMapping(d *snapshotData, tm TermAssignment) {
	for _, x := range d.termMappings {
		if x == tm {
			return
		}
	}
	d.termMappings = append(d.termMappings, tm)
	sort.SliceStable(d.termMappings, func(i, j int) bool {
		a, b := d.termMappings[i], d.termMappings[j]
		if na, nb := NormalizeTerm(a.Term), NormalizeTerm(b.Term); na != nb {
			return na < nb
		}
		if a.Term != b.Term {
			return a.Term < b.Term
		}
		if a.ConceptID != b.ConceptID {
			return a.ConceptID < b.ConceptID
		}
		return a.Source < b.Source
	})
}

// removeTermMapping 删除术语映射；Source 为空表示不限来源。
func removeTermMapping(d *snapshotData, tm TermAssignment) {
	out := d.termMappings[:0]
	for _, x := range d.termMappings {
		if x.Term == tm.Term && x.ConceptID == tm.ConceptID && (tm.Source == "" || x.Source == tm.Source) {
			continue
		}
		out = append(out, x)
	}
	d.termMappings = out
}

func deleteConcept(d *snapshotData, id string) {
	rec, ok := d.concepts[id]
	if !ok {
		return
	}
	deletedName := rec.concept.Name
	delete(d.concepts, id)

	// 规范名对应的术语结论失效（同义词随概念记录一起消失）。
	d.invalidations = append(d.invalidations, StalenessInfo{
		Kind: "term", SubjectID: id,
		Detail:        "term '" + deletedName + "' -> " + id + " invalidated because concept was deleted",
		InvalidatedAt: d.version, Reason: "concept_deleted",
	})

	// 级联删除层级边，并记录失效结论。
	if parents := d.parentsOf[id]; parents != nil {
		ps := make([]string, 0, len(parents))
		for p := range parents {
			ps = append(ps, p)
		}
		sortStrings(ps)
		for _, p := range ps {
			d.invalidations = append(d.invalidations, StalenessInfo{
				Kind: "hierarchy", SubjectID: id,
				Detail:        id + " is-a " + p + " removed because concept " + id + " was deleted",
				InvalidatedAt: d.version, Reason: "concept_deleted",
			})
		}
	}
	if children := d.childrenOf[id]; children != nil {
		cs := make([]string, 0, len(children))
		for c := range children {
			cs = append(cs, c)
		}
		sortStrings(cs)
		for _, c := range cs {
			d.invalidations = append(d.invalidations, StalenessInfo{
				Kind: "hierarchy", SubjectID: c,
				Detail:        c + " is-a " + id + " removed because concept " + id + " was deleted",
				InvalidatedAt: d.version, Reason: "concept_deleted",
			})
		}
	}
	delete(d.childrenOf, id)
	delete(d.parentsOf, id)
	for p, children := range d.childrenOf {
		delete(children, id)
		if len(children) == 0 {
			delete(d.childrenOf, p)
		}
	}
	for c, parents := range d.parentsOf {
		delete(parents, id)
		if len(parents) == 0 {
			delete(d.parentsOf, c)
		}
	}

	for _, rels := range d.relationsBySubject[id] {
		d.invalidations = append(d.invalidations, StalenessInfo{
			Kind: "relation", SubjectID: id,
			Detail:        id + " " + rels.Relation + " " + rels.Object + " removed",
			InvalidatedAt: d.version, Reason: "concept_deleted",
		})
	}
	for _, rels := range d.relationsByObject[id] {
		d.invalidations = append(d.invalidations, StalenessInfo{
			Kind: "relation", SubjectID: rels.Subject,
			Detail:        rels.Subject + " " + rels.Relation + " " + id + " removed",
			InvalidatedAt: d.version, Reason: "concept_deleted",
		})
	}
	delete(d.relationsBySubject, id)
	delete(d.relationsByObject, id)
	for s, rels := range d.relationsBySubject {
		d.relationsBySubject[s] = removeRelsAbout(rels, id)
	}
	for o, rels := range d.relationsByObject {
		d.relationsByObject[o] = removeRelsAbout(rels, id)
	}

	for _, m := range d.conceptMappings[id] {
		d.invalidations = append(d.invalidations, StalenessInfo{
			Kind: "mapping", SubjectID: id,
			Detail:        "mapping " + m.TargetID + " -> " + id + " removed",
			InvalidatedAt: d.version, Reason: "concept_deleted",
		})
		d.mappingsByTarget[m.TargetID] = removeMappingByTarget(d.mappingsByTarget[m.TargetID], m)
		if len(d.mappingsByTarget[m.TargetID]) == 0 {
			delete(d.mappingsByTarget, m.TargetID)
			delete(d.conflictedTargets, m.TargetID)
		}
	}
	delete(d.conceptMappings, id)

	// 级联删除指向该概念的显式术语映射，并记录失效结论。
	keptTerms := d.termMappings[:0]
	for _, tm := range d.termMappings {
		if tm.ConceptID == id {
			d.invalidations = append(d.invalidations, StalenessInfo{
				Kind: "term_mapping", SubjectID: id,
				Detail:        "term mapping '" + tm.Term + "' -> " + id + " removed",
				InvalidatedAt: d.version, Reason: "concept_deleted",
			})
			continue
		}
		keptTerms = append(keptTerms, tm)
	}
	d.termMappings = keptTerms
}

// computeMappingConflicts 重新计算全部外部标识的映射冲突，结果顺序确定。
// 规则：
//   - 同一来源把同一 target_id 映射到多个概念 -> same_source_conflict；
//   - 不同来源把同一 target_id 映射到不同概念 -> cross_source_conflict。
func computeMappingConflicts(d *snapshotData) []MappingConflict {
	// 先清空再重算，使删除/回滚后的状态与数据严格一致。
	for t := range d.conflictedTargets {
		delete(d.conflictedTargets, t)
	}

	targets := make([]string, 0, len(d.mappingsByTarget))
	for t := range d.mappingsByTarget {
		targets = append(targets, t)
	}
	sortStrings(targets)

	conflicts := make([]MappingConflict, 0)
	for _, t := range targets {
		assignments := append([]MappingAssignment(nil), d.mappingsByTarget[t]...)
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

		var conflict *MappingConflict
		for _, src := range sortedKeys(bySource) {
			ms := bySource[src]
			uniq := map[string]bool{}
			for _, m := range ms {
				uniq[m.ConceptID] = true
			}
			if len(uniq) > 1 {
				conflict = &MappingConflict{
					Type: ConflictSameSource, Source: src, TargetID: t, Targets: ms,
				}
				break
			}
		}
		if conflict == nil && len(conceptSet) > 1 {
			conflict = &MappingConflict{
				Type: ConflictCrossSource, TargetID: t, Targets: assignments,
			}
		}
		if conflict != nil {
			d.conflictedTargets[t] = conflict.Type
			conflicts = append(conflicts, *conflict)
		}
	}
	return conflicts
}
