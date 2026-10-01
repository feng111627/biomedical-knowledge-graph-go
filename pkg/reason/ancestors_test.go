package reason

import (
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/graph"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

func ds() *model.Dataset {
	return &model.Dataset{
		Concepts: []model.Concept{
			{ID: "ROOT", Label: "root", Source: "s"},
			{ID: "L1A", Label: "left", Source: "s"},
			{ID: "L1B", Label: "right", Source: "s"},
			{ID: "LEAF", Label: "leaf", Source: "s"},
		},
		Parents: []model.ParentLink{
			{ConceptID: "L1A", ParentID: "ROOT", Source: "src-left"},
			{ConceptID: "L1B", ParentID: "ROOT", Source: "src-right"},
			{ConceptID: "LEAF", ParentID: "L1A", Source: "src-diamond"},
			{ConceptID: "LEAF", ParentID: "L1B", Source: "src-diamond"},
		},
	}
}

func TestTransitiveAncestorsDiamond(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	t.Logf("INPUT diamond hierarchy LEAF -> {L1A,L1B} -> ROOT")
	c := AncestorsOf(snap, "LEAF")
	t.Logf("VERDICT ancestors=%v provenanceEdges=%d", c.Ancestors, len(c.Provenance))
	want := []string{"L1A", "L1B", "ROOT"}
	if len(c.Ancestors) != len(want) {
		t.Fatalf("ancestors = %v, want %v", c.Ancestors, want)
	}
	for i, w := range want {
		if c.Ancestors[i] != w {
			t.Fatalf("ancestors = %v, want sorted %v", c.Ancestors, want)
		}
	}
	if len(c.Provenance) != 4 {
		t.Fatalf("expected 4 is-a edges in provenance, got %d: %+v", len(c.Provenance), c.Provenance)
	}
	srcs := map[string]bool{}
	for _, p := range c.Provenance {
		srcs[p.Source] = true
	}
	if !srcs["src-left"] || !srcs["src-right"] || !srcs["src-diamond"] {
		t.Fatalf("provenance must cover every contributing source, got %v", srcs)
	}
}

func TestAncestorsNotFound(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	c := AncestorsOf(snap, "GHOST")
	if !c.NotFound {
		t.Fatal("expected NotFound for missing concept")
	}
	stale, why := IsStale(snap, c)
	t.Logf("INPUT missing concept GHOST -> not_found=%v stale-on-same-snapshot=%v", c.NotFound, stale)
	if stale {
		t.Fatalf("not-found conclusion should be consistent on the same snapshot, got: %s", why)
	}
}

func TestStaleAfterDelete(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	before := AncestorsOf(snap, "LEAF")

	d2 := ds()
	var kept []model.Concept
	for _, c := range d2.Concepts {
		if c.ID != "L1A" {
			kept = append(kept, c)
		}
	}
	d2.Concepts = kept
	// Keep LEAF->L1A edge referencing now-deleted L1A only to build a draft
	// manually; BuildSnapshot tolerates it but staleness compares concepts.
	var keptEdges []model.ParentLink
	for _, p := range d2.Parents {
		if p.ConceptID != "L1A" {
			keptEdges = append(keptEdges, p)
		}
	}
	d2.Parents = keptEdges
	snap2 := graph.BuildSnapshot(2, d2)
	stale, why := IsStale(snap2, before)
	t.Logf("INPUT delete L1A + its edges; VERDICT stale=%v reason=%q", stale, why)
	if !stale {
		t.Fatal("conclusion must be stale after an ancestor is deleted")
	}
}

func TestStaleAfterRename(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	before := AncestorsOf(snap, "LEAF")
	d2 := ds()
	for i := range d2.Concepts {
		if d2.Concepts[i].ID == "LEAF" {
			d2.Concepts[i].Label = "leaf renamed"
		}
	}
	snap2 := graph.BuildSnapshot(2, d2)
	stale, why := IsStale(snap2, before)
	t.Logf("INPUT rename LEAF; VERDICT stale=%v reason=%q", stale, why)
	if !stale {
		t.Fatal("conclusion must be stale after concept rename")
	}
}

func TestStaleAfterEdgeRemoval(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	before := AncestorsOf(snap, "LEAF")
	d2 := ds()
	var kept []model.ParentLink
	for _, p := range d2.Parents {
		if !(p.ConceptID == "LEAF" && p.ParentID == "L1A") {
			kept = append(kept, p)
		}
	}
	d2.Parents = kept
	snap2 := graph.BuildSnapshot(2, d2)
	stale, why := IsStale(snap2, before)
	t.Logf("INPUT remove edge LEAF->L1A; VERDICT stale=%v reason=%q", stale, why)
	if !stale {
		t.Fatal("conclusion must be stale once a provenance edge is removed")
	}
}

func TestStaleAfterNoOpRevision(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	before := AncestorsOf(snap, "LEAF")
	snap2 := graph.BuildSnapshot(2, ds()) // identical content, newer revision
	stale, why := IsStale(snap2, before)
	t.Logf("INPUT same content at new revision; VERDICT stale=%v why=%q", stale, why)
	if stale {
		t.Fatal("structurally identical graph must still support the old conclusion")
	}
}

func TestDeterministicAncestorOrder(t *testing.T) {
	snap := graph.BuildSnapshot(1, ds())
	first := AncestorsOf(snap, "LEAF")
	for i := 0; i < 30; i++ {
		c := AncestorsOf(snap, "LEAF")
		if len(c.Ancestors) != len(first.Ancestors) {
			t.Fatal("ancestor set changed across runs")
		}
		for j := range c.Ancestors {
			if c.Ancestors[j] != first.Ancestors[j] {
				t.Fatalf("ancestor order not deterministic: %v vs %v", c.Ancestors, first.Ancestors)
			}
		}
	}
}
