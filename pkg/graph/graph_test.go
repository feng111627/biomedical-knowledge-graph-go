package graph

import (
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

func TestSnapshotIndexesAndOrder(t *testing.T) {
	d := &model.Dataset{
		Concepts: []model.Concept{
			{ID: "P", Label: "p", Source: "s"},
			{ID: "C", Label: "c", Source: "s"},
			{ID: "A", Label: "a", Source: "s"},
		},
		Parents: []model.ParentLink{
			{ConceptID: "C", ParentID: "P", Source: "s"},
			{ConceptID: "A", ParentID: "P", Source: "s"},
		},
		Synonyms: []model.Synonym{
			{ConceptID: "C", Term: "see", Source: "s"},
			{ConceptID: "A", Term: "ay", Source: "s"},
		},
	}
	snap := BuildSnapshot(7, d)
	if snap.Revision != 7 {
		t.Fatalf("revision = %d", snap.Revision)
	}
	if got := snap.ChildrenOf("P"); len(got) != 2 || got[0] != "A" || got[1] != "C" {
		t.Fatalf("children not sorted: %v", got)
	}
	if len(snap.SynonymsOf("C")) != 1 || snap.SynonymsOf("C")[0].Term != "see" {
		t.Fatalf("synonym index wrong: %+v", snap.SynonymsOf("C"))
	}
	t.Logf("VERDICT children(P)=%v synonyms sorted=%v", snap.ChildrenOf("P"), snap.Synonyms)
}

func TestAtomicSwapAndIsolation(t *testing.T) {
	d1 := &model.Dataset{Concepts: []model.Concept{{ID: "X", Label: "x", Source: "s"}}}
	g := New()
	s1 := g.LoadDataset(d1)
	s2 := BuildSnapshot(2, &model.Dataset{
		Concepts: []model.Concept{{ID: "Y", Label: "y", Source: "s"}},
	})
	// Readers hold s1 independently of the swap.
	g.Swap(s2)
	if _, ok := s1.Concepts["X"]; !ok {
		t.Fatal("old snapshot must stay intact after swap")
	}
	if _, ok := g.Current().Concepts["X"]; ok {
		t.Fatal("current snapshot must be s2")
	}
	if _, ok := g.Current().Concepts["Y"]; !ok {
		t.Fatal("Y missing in current snapshot")
	}
	// Mutating the input dataset after LoadDataset must not affect state.
	d1.Concepts[0].Label = "mutated"
	if s1.Concepts["X"].Label != "x" {
		t.Fatal("published snapshot must be isolated from later input mutation")
	}
	t.Log("VERDICT snapshots immutable, swap atomic, input isolated")
}
