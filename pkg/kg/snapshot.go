package kg

// Snapshot 是某个版本的不可变图谱快照。
type Snapshot struct {
	data *snapshotData
}

// Version 返回快照版本号（单调递增）。
func (s *Snapshot) Version() int64 {
	if s == nil || s.data == nil {
		return 0
	}
	return s.data.version
}

// snapshotData 是快照承载的全部已验证数据。
// 一旦随快照发布即视为不可变；构造新版本一律通过 clone。
type snapshotData struct {
	version int64

	concepts map[string]*conceptRecord
	// childrenOf[parent][child] = 给出该边的来源集合
	childrenOf map[string]map[string]map[string]bool
	// parentsOf[child][parent] = 给出该边的来源集合
	parentsOf map[string]map[string]map[string]bool
	// relationsBySubject / relationsByObject
	relationsBySubject map[string][]EntityRelation
	relationsByObject  map[string][]EntityRelation
	// mappingsByTarget[targetID] = 该外部标识的全部归属
	mappingsByTarget map[string][]MappingAssignment
	// conceptMappings[id] 指向该概念的全部映射
	conceptMappings map[string][]MappingAssignment
	// conflictedTargets 记录处于冲突状态的外部标识及其冲突类型。
	conflictedTargets map[string]ConflictType
	// invalidations 记录本版本相较其基版本被判定失效的旧结论。
	invalidations []StalenessInfo
}

// conceptRecord 是一个概念在快照中的完整记录。
type conceptRecord struct {
	concept Concept
	// nameSources[name] = 给出该规范名的来源；长度 >1 概念即为多来源一致定义。
	nameSources map[string][]string
	synonyms    map[string]map[string]bool // synonym -> set(source)
	sources     map[string]bool
	evidences   []Evidence
}

func newSnapshotData(version int64) *snapshotData {
	return &snapshotData{
		version:            version,
		concepts:           map[string]*conceptRecord{},
		childrenOf:         map[string]map[string]map[string]bool{},
		parentsOf:          map[string]map[string]map[string]bool{},
		relationsBySubject: map[string][]EntityRelation{},
		relationsByObject:  map[string][]EntityRelation{},
		mappingsByTarget:   map[string][]MappingAssignment{},
		conceptMappings:    map[string][]MappingAssignment{},
		conflictedTargets:  map[string]ConflictType{},
	}
}

// clone 生成一份深拷贝，用于从旧版本构造新版本。
func (d *snapshotData) clone(version int64) *snapshotData {
	if d == nil {
		return newSnapshotData(version)
	}
	n := newSnapshotData(version)

	for id, rec := range d.concepts {
		nr := &conceptRecord{
			concept:     rec.concept,
			nameSources: map[string][]string{},
			synonyms:    map[string]map[string]bool{},
			sources:     map[string]bool{},
			evidences:   append([]Evidence(nil), rec.evidences...),
		}
		for name, ss := range rec.nameSources {
			nr.nameSources[name] = append([]string(nil), ss...)
		}
		for syn, ss := range rec.synonyms {
			nr.synonyms[syn] = map[string]bool{}
			for s := range ss {
				nr.synonyms[syn][s] = true
			}
		}
		for s := range rec.sources {
			nr.sources[s] = true
		}
		n.concepts[id] = nr
	}
	copyEdges := func(dst, src map[string]map[string]map[string]bool) {
		for a, inner := range src {
			cp := map[string]map[string]bool{}
			for b, set := range inner {
				cp2 := map[string]bool{}
				for srcName := range set {
					cp2[srcName] = true
				}
				cp[b] = cp2
			}
			dst[a] = cp
		}
	}
	copyEdges(n.childrenOf, d.childrenOf)
	copyEdges(n.parentsOf, d.parentsOf)
	for k, v := range d.relationsBySubject {
		n.relationsBySubject[k] = append([]EntityRelation(nil), v...)
	}
	for k, v := range d.relationsByObject {
		n.relationsByObject[k] = append([]EntityRelation(nil), v...)
	}
	for k, v := range d.mappingsByTarget {
		n.mappingsByTarget[k] = append([]MappingAssignment(nil), v...)
	}
	for k, v := range d.conceptMappings {
		n.conceptMappings[k] = append([]MappingAssignment(nil), v...)
	}
	for k, v := range d.conflictedTargets {
		n.conflictedTargets[k] = v
	}
	n.invalidations = append(n.invalidations, d.invalidations...)
	return n
}

// Ancestors 在该快照上做祖先传递推导（供需要固定快照的调用方使用）。
func (s *Snapshot) Ancestors(id string) ([]AncestorPath, bool) {
	return AncestorsIn(s.data, id)
}

// Descendants 在该快照上做后代传递推导。
func (s *Snapshot) Descendants(id string) ([]AncestorPath, bool) {
	return DescendantsIn(s.data, id)
}
