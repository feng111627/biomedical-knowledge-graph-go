// Package graph holds the in-memory knowledge graph. State is represented by
// immutable snapshots; revisions build a new snapshot and atomically swap it
// in, so concurrent readers never observe a half-updated graph.
package graph

import (
	"sort"
	"sync/atomic"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

// Snapshot is an immutable, point-in-time view of the whole graph. All slices
// and maps inside a snapshot must be treated as read-only after publication.
type Snapshot struct {
	Revision  int64
	Concepts  map[string]model.Concept // concept ID -> concept
	Synonyms  []model.Synonym
	Parents   []model.ParentLink
	Relations []model.EntityRelation
	Mappings  []model.Mapping

	// derived indexes (also read-only after construction)
	children  map[string][]string // parent ID -> ordered child IDs
	synByTerm map[string][]string // normalized term -> concept IDs
	synByConc map[string][]model.Synonym
	relBySubj map[string][]model.EntityRelation
	relByObj  map[string][]model.EntityRelation
}

// ChildrenOf returns the directly asserted children of a concept.
func (s *Snapshot) ChildrenOf(id string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.children[id]...)
}

// SynonymsOf returns all synonyms asserted for a concept.
func (s *Snapshot) SynonymsOf(id string) []model.Synonym {
	if s == nil {
		return nil
	}
	return append([]model.Synonym(nil), s.synByConc[id]...)
}

// RelationsBySubject returns relations whose subject is id.
func (s *Snapshot) RelationsBySubject(id string) []model.EntityRelation {
	if s == nil {
		return nil
	}
	return append([]model.EntityRelation(nil), s.relBySubj[id]...)
}

// RelationsByObject returns relations whose object is id.
func (s *Snapshot) RelationsByObject(id string) []model.EntityRelation {
	if s == nil {
		return nil
	}
	return append([]model.EntityRelation(nil), s.relByObj[id]...)
}

// Graph is a concurrency-safe holder of the current immutable Snapshot.
type Graph struct {
	current atomic.Pointer[Snapshot]
}

// New creates an empty graph.
func New() *Graph { return &Graph{} }

// Current returns the current snapshot (or nil before the first load).
func (g *Graph) Current() *Snapshot { return g.current.Load() }

// Swap atomically replaces the current snapshot and returns the new revision.
func (g *Graph) Swap(s *Snapshot) { g.current.Store(s) }

// LoadDataset replaces graph state with an imported dataset snapshot.
// The data is copied, so later mutation of d cannot affect published state.
func (g *Graph) LoadDataset(d *model.Dataset) *Snapshot {
	s := BuildSnapshot(1, d)
	g.Swap(s)
	return s
}

// BuildSnapshot constructs an immutable snapshot with derived indexes.
// Slices are copied and every ordered output is sorted, which guarantees that
// the same input always produces identical, deterministic traversal order.
func BuildSnapshot(revision int64, d *model.Dataset) *Snapshot {
	s := &Snapshot{
		Revision:  revision,
		Concepts:  make(map[string]model.Concept, len(d.Concepts)),
		Synonyms:  append([]model.Synonym(nil), d.Synonyms...),
		Parents:   append([]model.ParentLink(nil), d.Parents...),
		Relations: append([]model.EntityRelation(nil), d.Relations...),
		Mappings:  append([]model.Mapping(nil), d.Mappings...),
		children:  map[string][]string{},
		synByTerm: map[string][]string{},
		synByConc: map[string][]model.Synonym{},
		relBySubj: map[string][]model.EntityRelation{},
		relByObj:  map[string][]model.EntityRelation{},
	}
	for _, c := range d.Concepts {
		s.Concepts[c.ID] = c
	}
	for _, l := range s.Parents {
		s.children[l.ParentID] = append(s.children[l.ParentID], l.ConceptID)
	}
	for _, syn := range s.Synonyms {
		key := normalizeTerm(syn.Term)
		s.synByTerm[key] = append(s.synByTerm[key], syn.ConceptID)
		s.synByConc[syn.ConceptID] = append(s.synByConc[syn.ConceptID], syn)
	}
	for _, r := range s.Relations {
		s.relBySubj[r.SubjectID] = append(s.relBySubj[r.SubjectID], r)
		s.relByObj[r.ObjectID] = append(s.relByObj[r.ObjectID], r)
	}
	for id := range s.children {
		sort.Strings(s.children[id])
	}
	for k := range s.synByTerm {
		sort.Strings(s.synByTerm[k])
	}
	sort.Slice(s.Synonyms, func(i, j int) bool {
		if s.Synonyms[i].ConceptID != s.Synonyms[j].ConceptID {
			return s.Synonyms[i].ConceptID < s.Synonyms[j].ConceptID
		}
		return s.Synonyms[i].Term < s.Synonyms[j].Term
	})
	sort.Slice(s.Parents, func(i, j int) bool {
		if s.Parents[i].ConceptID != s.Parents[j].ConceptID {
			return s.Parents[i].ConceptID < s.Parents[j].ConceptID
		}
		return s.Parents[i].ParentID < s.Parents[j].ParentID
	})
	sort.Slice(s.Relations, func(i, j int) bool {
		if s.Relations[i].SubjectID != s.Relations[j].SubjectID {
			return s.Relations[i].SubjectID < s.Relations[j].SubjectID
		}
		if s.Relations[i].Predicate != s.Relations[j].Predicate {
			return s.Relations[i].Predicate < s.Relations[j].Predicate
		}
		return s.Relations[i].ObjectID < s.Relations[j].ObjectID
	})
	sort.Slice(s.Mappings, func(i, j int) bool {
		if s.Mappings[i].ExternalID != s.Mappings[j].ExternalID {
			return s.Mappings[i].ExternalID < s.Mappings[j].ExternalID
		}
		if s.Mappings[i].Source != s.Mappings[j].Source {
			return s.Mappings[i].Source < s.Mappings[j].Source
		}
		return s.Mappings[i].ConceptID < s.Mappings[j].ConceptID
	})
	return s
}

// normalizeTerm lower-cases and trims a term so matching is case-insensitive
// and whitespace-insensitive.
func normalizeTerm(t string) string {
	b := make([]byte, 0, len(t))
	started := false
	for i := 0; i < len(t); i++ {
		ch := t[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if started {
				b = append(b, ' ')
				started = false
			}
			continue
		}
		b = append(b, ch)
		started = true
	}
	for len(b) > 0 && b[len(b)-1] == ' ' {
		b = b[:len(b)-1]
	}
	return string(b)
}
