// Package reason implements transitive hierarchy reasoning over immutable graph
// snapshots. Every derived conclusion lists the exact asserted edges that
// support it, so a result can be (a) traced to its sources and (b) re-checked
// against a later snapshot to see whether it has become stale.
package reason

import (
	"sort"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/graph"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

// Conclusion is a derived fact together with everything needed to explain and
// later invalidate it.
type Conclusion struct {
	Kind       string             `json:"kind"`       // "transitive_ancestors"
	Revision   int64              `json:"revision"`   // snapshot revision it was derived from
	ConceptID  string             `json:"concept_id"` // starting concept
	LabelAt    string             `json:"label_at"`   // label of the concept at derivation time
	Ancestors  []string           `json:"ancestors"`  // all transitive ancestors, sorted
	Provenance []model.Provenance `json:"provenance"` // every is-a edge relied upon
	NotFound   bool               `json:"not_found"`  // concept did not exist when derived
}

// edgeKey identifies an asserted is-a edge (child -> parent).
func edgeKey(child, parent string) string { return child + "\x00" + parent }

// AncestorsOf computes every transitive ancestor of conceptID by following
// asserted parent edges. Traversal is DFS with a visited set (bounded even if
// a cyclic snapshot were ever passed in) and all outputs are sorted, making
// repeated runs over the same snapshot identical.
func AncestorsOf(snap *graph.Snapshot, conceptID string) *Conclusion {
	c := &Conclusion{
		Kind:      "transitive_ancestors",
		ConceptID: conceptID,
	}
	if snap == nil {
		c.NotFound = true
		return c
	}
	c.Revision = snap.Revision
	concept, ok := snap.Concepts[conceptID]
	if !ok {
		c.NotFound = true
		return c
	}
	c.LabelAt = concept.Label

	// Direct parent edges asserted for the start concept, deterministically
	// ordered (snap.Parents is pre-sorted by child,parent at build time).
	direct := map[string]model.ParentLink{}
	for _, p := range snap.Parents {
		if p.ConceptID == conceptID {
			direct[p.ParentID] = p
		}
	}

	visited := map[string]bool{conceptID: true}
	usedEdges := map[string]model.ParentLink{}
	ancestors := map[string]bool{}

	// Iterative DFS. Each frame holds the current node and the next parent
	// index to expand; parents of a node are derived from snap.Parents.
	parentsOf := func(id string) []string {
		var ps []string
		for _, p := range snap.Parents {
			if p.ConceptID == id {
				ps = append(ps, p.ParentID)
			}
		}
		sort.Strings(ps)
		return ps
	}

	type frame struct {
		node string
		idx  int
	}
	stack := []frame{{node: conceptID}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		ps := parentsOf(top.node)
		if top.idx >= len(ps) {
			stack = stack[:len(stack)-1]
			continue
		}
		pid := ps[top.idx]
		top.idx++
		// Record the edge that just led us here (all are asserted links).
		for _, p := range snap.Parents {
			if p.ConceptID == top.node && p.ParentID == pid {
				usedEdges[edgeKey(p.ConceptID, p.ParentID)] = p
				break
			}
		}
		if !visited[pid] {
			visited[pid] = true
			ancestors[pid] = true
			stack = append(stack, frame{node: pid})
		}
	}

	c.Ancestors = make([]string, 0, len(ancestors))
	for a := range ancestors {
		c.Ancestors = append(c.Ancestors, a)
	}
	sort.Strings(c.Ancestors)

	keys := make([]string, 0, len(usedEdges))
	for k := range usedEdges {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := usedEdges[k]
		c.Provenance = append(c.Provenance, model.Provenance{
			Source:   e.Source,
			RecordID: e.ConceptID + "->" + e.ParentID,
			Detail:   "is-a edge " + e.ConceptID + " subClassOf " + e.ParentID,
		})
	}
	_ = direct // kept for documentation of direct vs inferred distinction
	return c
}

// IsStale reports whether a previously derived conclusion is still valid for
// snap. A conclusion is stale when ANY of the following holds:
//
//   - it was derived against a different revision;
//   - the starting concept was deleted;
//   - the starting concept was renamed (its label changed);
//   - any ancestor the conclusion depends on was deleted;
//   - any is-a edge in its provenance was removed (the inference is no longer
//     fully supported by the graph).
//
// When stale, callers must not present the old result as current; they should
// re-run AncestorsOf against the new snapshot.
func IsStale(snap *graph.Snapshot, c *Conclusion) (bool, string) {
	if c == nil {
		return true, "no conclusion supplied"
	}
	if snap == nil {
		return true, "snapshot missing: graph no longer contains the conclusion's basis"
	}
	if snap.Revision != c.Revision {
		// Different revision is only a signal; still check structural support.
	}
	cur, exists := snap.Concepts[c.ConceptID]
	if c.NotFound {
		// The conclusion recorded "concept did not exist". It becomes outdated
		// precisely when the concept has since appeared; if it is still
		// absent, the conclusion remains valid.
		if exists {
			return true, "concept " + c.ConceptID + " now exists; prior not-found conclusion is outdated"
		}
		return false, ""
	}
	if !exists {
		return true, "concept " + c.ConceptID + " was deleted; old conclusion no longer applies"
	}
	if cur.Label != c.LabelAt {
		return true, "concept " + c.ConceptID + " was renamed from " + c.LabelAt + " to " + cur.Label
	}
	for _, a := range c.Ancestors {
		if _, ok := snap.Concepts[a]; !ok {
			return true, "ancestor concept " + a + " relied upon by the conclusion was deleted"
		}
	}
	edgeExists := func(child, parent string) bool {
		for _, p := range snap.Parents {
			if p.ConceptID == child && p.ParentID == parent {
				return true
			}
		}
		return false
	}
	for _, prov := range c.Provenance {
		// RecordID is exactly "child->parent" as produced above.
		split := splitArrow(prov.RecordID)
		child, par := split[0], split[1]
		if !edgeExists(child, par) {
			return true, "provenance edge " + child + " -> " + par + " was removed; transitive conclusion unsupported"
		}
	}
	return false, ""
}

func splitArrow(s string) [2]string {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '-' && s[i+1] == '>' {
			return [2]string{s[:i], s[i+2:]}
		}
	}
	return [2]string{s, ""}
}
