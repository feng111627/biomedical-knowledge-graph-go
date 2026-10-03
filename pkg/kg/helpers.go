package kg

import "sort"

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func appendUnique(xs []string, x string) []string {
	if containsString(xs, x) {
		return xs
	}
	return append(xs, x)
}

func appendUniqueEvidence(xs []Evidence, e Evidence) []Evidence {
	for _, v := range xs {
		if v == e {
			return xs
		}
	}
	return append(xs, e)
}

func sortedKeys(m map[string][]MappingAssignment) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedSourceSet 以来源集合的排序切片形式返回，保证派生登记顺序确定。
func sortedSourceSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func relationLess(a, b EntityRelation) bool {
	if a.Subject != b.Subject {
		return a.Subject < b.Subject
	}
	if a.Relation != b.Relation {
		return a.Relation < b.Relation
	}
	if a.Object != b.Object {
		return a.Object < b.Object
	}
	return a.Source < b.Source
}

func containsRelation(xs []EntityRelation, r EntityRelation) bool {
	for _, v := range xs {
		if v == r {
			return true
		}
	}
	return false
}

func relationMatch(v, r EntityRelation) bool {
	if v.Subject != r.Subject || v.Relation != r.Relation || v.Object != r.Object {
		return false
	}
	return r.Source == "" || v.Source == r.Source
}

func removeRelationMatch(xs []EntityRelation, r EntityRelation) []EntityRelation {
	out := xs[:0]
	for _, v := range xs {
		if !relationMatch(v, r) {
			out = append(out, v)
		}
	}
	return out
}

func removeRelsAbout(xs []EntityRelation, id string) []EntityRelation {
	out := xs[:0]
	for _, v := range xs {
		if v.Subject != id && v.Object != id {
			out = append(out, v)
		}
	}
	return out
}

func containsMapping(xs []MappingAssignment, m MappingAssignment) bool {
	for _, v := range xs {
		if v == m {
			return true
		}
	}
	return false
}

func mappingMatch(v, m MappingAssignment) bool {
	if v.TargetID != m.TargetID || v.ConceptID != m.ConceptID {
		return false
	}
	return m.Source == "" || v.Source == m.Source
}

func removeMappingMatch(xs []MappingAssignment, m MappingAssignment) []MappingAssignment {
	out := xs[:0]
	for _, v := range xs {
		if !mappingMatch(v, m) {
			out = append(out, v)
		}
	}
	return out
}

func removeMappingByTarget(xs []MappingAssignment, m MappingAssignment) []MappingAssignment {
	return removeMappingMatch(xs, m)
}
