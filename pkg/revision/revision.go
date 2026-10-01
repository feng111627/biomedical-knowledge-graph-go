// Package revision provides incremental edits on top of committed snapshots
// with atomic commit and whole-transaction rollback semantics.
//
// Concurrency model:
//
//   - readers use graph.Current() and get an immutable snapshot, so they can
//     NEVER observe a half-edited graph while a transaction is in progress;
//   - a Tx edits a private draft copied from the base snapshot;
//   - Commit runs the full import validation (cycles / dangling refs /
//     conflicting duplicates) and only on success builds a brand-new immutable
//     snapshot and atomically swaps it in;
//   - Rollback simply discards the private draft, leaving the published graph
//     (and every previously derived conclusion) completely untouched.
package revision

import (
	"fmt"
	"sync"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/graph"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/ingest"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

// draft is the private, mutable working copy of a transaction.
type draft struct {
	concepts  map[string]model.Concept
	synonyms  []model.Synonym
	parents   map[[2]string]model.ParentLink // child,parent -> link
	relations map[string]model.EntityRelation
	mappings  map[[3]string]model.Mapping // source,external,concept -> mapping
}

// Manager coordinates revisions against a graph.
type Manager struct {
	g       *graph.Graph
	mu      sync.Mutex // serializes commits; readers are never blocked
	counter int64      // strictly increasing committed revision numbers
}

// NewManager creates a revision manager for g.
func NewManager(g *graph.Graph) *Manager {
	m := &Manager{g: g, counter: 1}
	if s := g.Current(); s != nil {
		m.counter = s.Revision
	}
	return m
}

// Tx is an open revision transaction built from one immutable base snapshot.
type Tx struct {
	m     *Manager
	base  *graph.Snapshot
	work  *draft
	open  bool
	notes []string // human-readable change log for explainability
}

// Begin starts a new revision transaction from the current published snapshot.
func (m *Manager) Begin() *Tx {
	t := &Tx{m: m, open: true}
	if cur := m.g.Current(); cur != nil {
		t.base = cur
		t.work = snapshotToDraft(cur)
	} else {
		t.work = &draft{
			concepts:  map[string]model.Concept{},
			parents:   map[[2]string]model.ParentLink{},
			relations: map[string]model.EntityRelation{},
			mappings:  map[[3]string]model.Mapping{},
		}
	}
	return t
}

func snapshotToDraft(s *graph.Snapshot) *draft {
	d := &draft{
		concepts:  make(map[string]model.Concept, len(s.Concepts)),
		synonyms:  append([]model.Synonym(nil), s.Synonyms...),
		parents:   map[[2]string]model.ParentLink{},
		relations: map[string]model.EntityRelation{},
		mappings:  map[[3]string]model.Mapping{},
	}
	for id, c := range s.Concepts {
		d.concepts[id] = c
	}
	for _, p := range s.Parents {
		d.parents[[2]string{p.ConceptID, p.ParentID}] = p
	}
	for _, r := range s.Relations {
		d.relations[r.ID] = r
	}
	for _, mp := range s.Mappings {
		d.mappings[[3]string{mp.Source, mp.ExternalID, mp.ConceptID}] = mp
	}
	return d
}

// AddConcept inserts a new concept.
func (t *Tx) AddConcept(c model.Concept) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	if _, exists := t.work.concepts[c.ID]; exists {
		return fmt.Errorf("concept %q already exists; use an update/rename operation instead", c.ID)
	}
	t.work.concepts[c.ID] = c
	t.notes = append(t.notes, "add concept "+c.ID)
	return nil
}

// RenameConcept changes the label of an existing concept.
func (t *Tx) RenameConcept(id, label string) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	c, ok := t.work.concepts[id]
	if !ok {
		return fmt.Errorf("cannot rename missing concept %q", id)
	}
	t.work.concepts[id] = model.Concept{ID: id, Label: label, Source: c.Source, Description: c.Description}
	t.notes = append(t.notes, fmt.Sprintf("rename concept %s: %q -> %q", id, c.Label, label))
	return nil
}

// DeleteConcept removes a concept. Removing a concept that other records still
// point at is rejected at Commit time as a dangling reference, never silently
// cascaded.
func (t *Tx) DeleteConcept(id string) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	if _, ok := t.work.concepts[id]; !ok {
		return fmt.Errorf("cannot delete missing concept %q", id)
	}
	delete(t.work.concepts, id)
	t.notes = append(t.notes, "delete concept "+id)
	return nil
}

// AddSynonym adds an asserted synonym.
func (t *Tx) AddSynonym(s model.Synonym) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	t.work.synonyms = append(t.work.synonyms, s)
	t.notes = append(t.notes, fmt.Sprintf("add synonym %q -> %s", s.Term, s.ConceptID))
	return nil
}

// AddParent adds a subclass edge; duplicate identical edges are idempotent.
func (t *Tx) AddParent(link model.ParentLink) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	k := [2]string{link.ConceptID, link.ParentID}
	if _, ok := t.work.parents[k]; ok {
		return nil
	}
	t.work.parents[k] = link
	t.notes = append(t.notes, "add parent edge "+link.ConceptID+" -> "+link.ParentID)
	return nil
}

// RemoveParent deletes a subclass edge.
func (t *Tx) RemoveParent(child, parent string) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	k := [2]string{child, parent}
	if _, ok := t.work.parents[k]; !ok {
		return fmt.Errorf("parent edge %s -> %s does not exist", child, parent)
	}
	delete(t.work.parents, k)
	t.notes = append(t.notes, "remove parent edge "+child+" -> "+parent)
	return nil
}

// AddRelation inserts a relation; a reused id with a different triple fails.
func (t *Tx) AddRelation(r model.EntityRelation) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	t.work.relations[r.ID] = r
	t.notes = append(t.notes, "add relation "+r.ID)
	return nil
}

// AddMapping records one source-backed identifier mapping.
func (t *Tx) AddMapping(mp model.Mapping) error {
	if !t.open {
		return fmt.Errorf("transaction closed")
	}
	t.work.mappings[[3]string{mp.Source, mp.ExternalID, mp.ConceptID}] = mp
	t.notes = append(t.notes, "add mapping "+mp.Source+":"+mp.ExternalID+" -> "+mp.ConceptID)
	return nil
}

// toDataset materializes the private draft into a validatable dataset. Output
// ordering is sorted by the snapshot builder, so it is deterministic.
func (d *draft) toDataset() *model.Dataset {
	ds := &model.Dataset{Synonyms: append([]model.Synonym(nil), d.synonyms...)}
	for _, c := range d.concepts {
		ds.Concepts = append(ds.Concepts, c)
	}
	for _, p := range d.parents {
		ds.Parents = append(ds.Parents, p)
	}
	for _, r := range d.relations {
		ds.Relations = append(ds.Relations, r)
	}
	for _, mp := range d.mappings {
		ds.Mappings = append(ds.Mappings, mp)
	}
	return ds
}

// Commit validates the full edited dataset and, only if valid, publishes it as
// a new atomically-swapped snapshot. A failed commit leaves the published
// graph unchanged and the transaction open for inspection/rollback.
func (t *Tx) Commit() (*graph.Snapshot, error) {
	if !t.open {
		return nil, fmt.Errorf("transaction closed")
	}
	ds := t.work.toDataset()
	if err := ingest.Validate(ds); err != nil {
		// Published graph is untouched: this is effectively a rejection, and
		// the caller can still Rollback or inspect and retry.
		return nil, err
	}
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	t.m.counter++
	snap := graph.BuildSnapshot(t.m.counter, ds)
	t.m.g.Swap(snap)
	t.open = false
	return snap, nil
}

// Rollback discards every uncommitted change in this transaction.
func (t *Tx) Rollback() {
	t.open = false
	t.work = nil
	t.notes = append(t.notes, "rolled back: draft discarded, published snapshot untouched")
}

// Notes returns the ordered, human-readable list of staged operations.
func (t *Tx) Notes() []string { return append([]string(nil), t.notes...) }
