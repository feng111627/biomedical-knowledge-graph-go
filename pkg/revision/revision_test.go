package revision

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/graph"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/ingest"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/reason"
)

func baseDataset() *model.Dataset {
	return &model.Dataset{
		Concepts: []model.Concept{
			{ID: "ROOT", Label: "root", Source: "s"},
			{ID: "MID", Label: "mid", Source: "s"},
			{ID: "LEAF", Label: "leaf", Source: "s"},
		},
		Parents: []model.ParentLink{
			{ConceptID: "MID", ParentID: "ROOT", Source: "s"},
			{ConceptID: "LEAF", ParentID: "MID", Source: "s"},
		},
	}
}

func TestIncrementalCommit(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	mgr := NewManager(g)

	tx := mgr.Begin()
	if err := tx.AddConcept(model.Concept{ID: "NEW", Label: "new node", Source: "s"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.AddParent(model.ParentLink{ConceptID: "NEW", ParentID: "ROOT", Source: "s"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("INPUT staged ops: %v", tx.Notes())
	snap, err := tx.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Logf("VERDICT committed revision=%d ancestors(NEW)=%v",
		snap.Revision, reason.AncestorsOf(snap, "NEW").Ancestors)
	if _, ok := snap.Concepts["NEW"]; !ok {
		t.Fatal("NEW missing after commit")
	}
	if got := reason.AncestorsOf(snap, "NEW").Ancestors; len(got) != 1 || got[0] != "ROOT" {
		t.Fatalf("NEW ancestors = %v, want [ROOT]", got)
	}
}

func TestInvalidCommitRejectedNoPublish(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	beforeRev := g.Current().Revision

	tx := NewManager(g).Begin()
	// Create a cycle MID -> LEAF -> MID.
	if err := tx.AddParent(model.ParentLink{ConceptID: "MID", ParentID: "LEAF", Source: "s"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("INPUT staged cycle edge MID->LEAF alongside LEAF->MID")
	_, err := tx.Commit()
	var rej *model.RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	t.Logf("VERDICT rejected kind=%s reason=%q", rej.Kind, rej.Message)
	if rej.Kind != model.RejectHierarchyCycle {
		t.Fatalf("kind=%s want hierarchy_cycle", rej.Kind)
	}
	if g.Current().Revision != beforeRev {
		t.Fatal("failed commit must not publish a new revision")
	}
	if len(g.Current().Parents) != 2 {
		t.Fatal("failed commit must leave published parents untouched")
	}
}

func TestDeleteCausesDanglingRejected(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	tx := NewManager(g).Begin()
	if err := tx.DeleteConcept("MID"); err != nil {
		t.Fatal(err)
	}
	t.Log("INPUT delete MID while LEAF still points to it")
	_, err := tx.Commit()
	var rej *model.RejectError
	if !errors.As(err, &rej) || rej.Kind != model.RejectDanglingReference {
		t.Fatalf("expected dangling_reference rejection, got %v", err)
	}
	t.Logf("VERDICT rejected kind=%s reason=%q", rej.Kind, rej.Message)
	if _, ok := g.Current().Concepts["MID"]; !ok {
		t.Fatal("rejected delete must not mutate published graph")
	}
}

func TestRollbackWholeTransaction(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	rev := g.Current().Revision

	tx := NewManager(g).Begin()
	if err := tx.DeleteConcept("LEAF"); err != nil {
		t.Fatal(err)
	}
	if err := tx.RenameConcept("ROOT", "totally different"); err != nil {
		t.Fatal(err)
	}
	if err := tx.AddConcept(model.Concept{ID: "X", Label: "x", Source: "s"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("INPUT staged ops before rollback: %v", tx.Notes())
	tx.Rollback()
	t.Logf("VERDICT %v", tx.Notes())

	cur := g.Current()
	if cur.Revision != rev {
		t.Fatalf("revision changed after rollback: %d -> %d", rev, cur.Revision)
	}
	if _, ok := cur.Concepts["LEAF"]; !ok {
		t.Fatal("LEAF must still exist after rollback")
	}
	if cur.Concepts["ROOT"].Label != "root" {
		t.Fatal("ROOT label change must be rolled back")
	}
	if _, ok := cur.Concepts["X"]; ok {
		t.Fatal("draft-only concept X must never be published")
	}
}

func TestStaleConclusionAfterCommittedDelete(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	before := reason.AncestorsOf(g.Current(), "LEAF")
	t.Logf("derived ancestors(LEAF)=%v at revision %d", before.Ancestors, before.Revision)

	tx := NewManager(g).Begin()
	// Remove LEAF's own parent edge, then delete the concept itself.
	if err := tx.RemoveParent("LEAF", "MID"); err != nil {
		t.Fatal(err)
	}
	if err := tx.DeleteConcept("LEAF"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	stale, why := reason.IsStale(g.Current(), before)
	t.Logf("VERDICT after committed delete: stale=%v reason=%q", stale, why)
	if !stale {
		t.Fatal("old conclusion must be invalidated by committed delete")
	}
}

// TestConcurrentReadersDuringRevisions verifies that readers always observe a
// fully consistent snapshot while many revisions commit. Run with -race.
//
// Invariant asserted on every read: either the full base graph is visible
// (3 concepts) or the full revised graph is visible (4 concepts); never an
// intermediate count, and ancestors() must be terminating + consistent.
func TestConcurrentReadersDuringRevisions(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	mgr := NewManager(g)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	violations := make(chan string, 10000)

	// Readers.
	for r := 0; r < 16; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := g.Current()
				n := len(snap.Concepts)
				if n != 3 && n != 4 {
					violations <- fmt.Sprintf("reader %d saw intermediate concept count %d", id, n)
					return
				}
				// Ancestors must terminate and return a sorted, stable set;
				// LEAF exists in both revisions.
				c := reason.AncestorsOf(snap, "LEAF")
				if c.NotFound {
					violations <- fmt.Sprintf("reader %d saw LEAF missing in committed snapshot", id)
					return
				}
				want := map[string]bool{"MID": true, "ROOT": true}
				if n == 4 {
					// NEW is a sibling of LEAF under ROOT, not an ancestor.
				}
				for _, a := range c.Ancestors {
					if !want[a] {
						violations <- fmt.Sprintf("reader %d unexpected ancestor %s", id, a)
						return
					}
				}
			}
		}(r)
	}

	// Writer: repeatedly add NEW concept+edge then remove it, each via a full
	// validated commit or rollback.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		tx := mgr.Begin()
		if err := tx.AddConcept(model.Concept{ID: "NEW", Label: "new", Source: "s"}); err == nil {
			_ = tx.AddParent(model.ParentLink{ConceptID: "NEW", ParentID: "ROOT", Source: "s"})
			if _, err := tx.Commit(); err != nil {
				tx.Rollback()
			}
		} else {
			tx.Rollback()
		}
		// Remove it again (edge first, to keep references consistent).
		tx2 := mgr.Begin()
		if err := tx2.DeleteConcept("NEW"); err == nil {
			_ = tx2.RemoveParent("NEW", "ROOT")
			if _, err := tx2.Commit(); err != nil {
				tx2.Rollback()
			}
		} else {
			tx2.Rollback()
		}
	}
	close(stop)
	wg.Wait()
	close(violations)
	for v := range violations {
		t.Error(v)
	}
	if len(violations) > 0 {
		t.Fatalf("concurrent readers observed inconsistent snapshots")
	}
}

// TestRollbackDuringConcurrentReaders: while readers run, a draft performs
// drastic deletes but rolls back; LEAF must remain visible the entire time.
func TestRollbackDuringConcurrentReaders(t *testing.T) {
	g := graph.New()
	g.LoadDataset(baseDataset())
	mgr := NewManager(g)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad int64
	var mu sync.Mutex
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := g.Current()
				if _, ok := snap.Concepts["LEAF"]; !ok {
					mu.Lock()
					bad++
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		tx := mgr.Begin()
		_ = tx.DeleteConcept("LEAF")
		_ = tx.DeleteConcept("MID")
		_ = tx.RenameConcept("ROOT", "nope")
		tx.Rollback()
	}
	close(stop)
	wg.Wait()
	if bad != 0 {
		t.Fatalf("LEAF was observed missing %d times despite rollback", bad)
	}
}

// guards against unused import if dataset evolves.
var _ = ingest.Validate
