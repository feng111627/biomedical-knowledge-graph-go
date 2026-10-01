// Package integration_test runs end-to-end scenarios over the local sample
// files, exercising import, query, reasoning, mapping conflicts, revisions,
// rollback and staleness together.
package integration_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/graph"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/ingest"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/mapping"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/reason"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/revision"
)

func dataPath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEndToEnd(t *testing.T) {
	// 1. Invalid batches are rejected with distinct, logged reasons.
	for _, tc := range []struct {
		file string
		kind model.RejectKind
	}{
		{"cycle_dataset.json", model.RejectHierarchyCycle},
		{"dangling_dataset.json", model.RejectDanglingReference},
		{"duplicate_conflict_dataset.json", model.RejectDuplicateConflict},
	} {
		_, err := ingest.LoadFile(dataPath(t, tc.file))
		var rej *model.RejectError
		if !errors.As(err, &rej) || rej.Kind != tc.kind {
			t.Fatalf("%s: want reject kind %s, got %v", tc.file, tc.kind, err)
		}
		t.Logf("IMPORT %s -> rejected kind=%s reason=%q", tc.file, rej.Kind, rej.Message)
	}

	// 2. Valid import and queries.
	ds, err := ingest.LoadFile(dataPath(t, "valid_dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New()
	snap := g.LoadDataset(ds)
	t.Logf("IMPORT valid_dataset -> revision=%d concepts=%d", snap.Revision, len(snap.Concepts))
	if got := snap.ChildrenOf("DISEASE"); len(got) != 2 {
		t.Fatalf("DISEASE children = %v, want 2", got)
	}

	// 3. Transitive reasoning carries ancestors and provenance.
	c := reason.AncestorsOf(snap, "FLU")
	t.Logf("REASON ancestors(FLU)=%v evidence=%d", c.Ancestors, len(c.Provenance))
	wantAncestors := map[string]bool{"RESPDIS": true, "INFECT": true, "DISEASE": true}
	if len(c.Ancestors) != len(wantAncestors) {
		t.Fatalf("ancestors = %v", c.Ancestors)
	}
	for _, a := range c.Ancestors {
		if !wantAncestors[a] {
			t.Fatalf("unexpected ancestor %s", a)
		}
	}
	if len(c.Provenance) == 0 {
		t.Fatal("conclusion must carry provenance")
	}

	// 4. Multi-source mapping: unambiguous and conflicted.
	resolver := mapping.New(snap.Mappings, ds.Concepts, ds.Synonyms)
	if r := resolver.ResolveID("UMLS:COLD"); r.IsConflicted() || r.ConceptID != "COLD" {
		t.Fatalf("UMLS:COLD should resolve to COLD, got %+v", r)
	}
	r := resolver.ResolveID("X:7788")
	if !r.IsConflicted() || r.Conflict.Kind != model.ConflictMutuallyExclusiveTarget {
		t.Fatalf("X:7788 should be cross-source conflict, got %+v", r.Conflict)
	}
	t.Logf("MAP X:7788 -> conflict kind=%s (%d evidence entries)",
		r.Conflict.Kind, len(r.Conflict.Entries))
	rt := resolver.ResolveTerm("cold")
	if !rt.IsConflicted() || rt.Conflict.Kind != model.ConflictMutuallyExclusiveTarget {
		t.Fatalf("term 'cold' should conflict across sources, got %+v", rt.Conflict)
	}
	t.Logf("TERM cold -> conflict kind=%s reason=%q", rt.Conflict.Kind, rt.Conflict.Reason)

	// Same-source conflict dataset yields the distinct stronger kind.
	ss, err := ingest.LoadFile(dataPath(t, "samesource_conflict_dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	ssr := mapping.New(ss.Mappings, ss.Concepts, ss.Synonyms)
	sr := ssr.ResolveID("DUP:1")
	if !sr.IsConflicted() || sr.Conflict.Kind != model.ConflictSameSourceTargets {
		t.Fatalf("DUP:1 should be same-source conflict, got %+v", sr.Conflict)
	}
	t.Logf("MAP DUP:1 -> conflict kind=%s reason=%q", sr.Conflict.Kind, sr.Conflict.Reason)

	// 5. Incremental revision + atomic rollback.
	mgr := revision.NewManager(g)
	before := g.Current().Revision
	tx := mgr.Begin()
	if err := tx.DeleteConcept("FLU"); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if g.Current().Revision != before {
		t.Fatal("rollback changed the published revision")
	}
	if _, ok := g.Current().Concepts["FLU"]; !ok {
		t.Fatal("FLU must survive rollback")
	}
	t.Logf("ROLLBACK -> revision stays %d, FLU present=%v", before, true)

	// 6. Committed rename invalidates a previously derived conclusion.
	old := reason.AncestorsOf(g.Current(), "COLD")
	rn := mgr.Begin()
	if err := rn.RenameConcept("COLD", "acute coryza syndrome"); err != nil {
		t.Fatal(err)
	}
	if _, err := rn.Commit(); err != nil {
		t.Fatal(err)
	}
	stale, why := reason.IsStale(g.Current(), old)
	if !stale {
		t.Fatal("old conclusion must be stale after rename")
	}
	t.Logf("STALE after rename -> %v (%s)", stale, why)
}
