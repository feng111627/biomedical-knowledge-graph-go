package kg_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

// TestConcurrent_ReadsNeverSeeHalfState：修订期间大量并发查询，
// 每个查询要么基于完整旧版本、要么基于完整新版本，绝不能观察到半更新状态。
// 判定依据：若 "NEW" 概念可见，则它的层级祖先推导也必须同时成立。
func TestConcurrent_ReadsNeverSeeHalfState(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	const readers = 16
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, readers)

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := store.Current()
				view, exists := r.GetConceptIn(snap, "D050")
				if exists {
					// 概念已经可见时，其层级边与祖先必须同时一致可见。
					if view.Concept.Name != "NewConcept" {
						errCh <- fmt.Errorf("half-state: name mismatch %q", view.Concept.Name)
						return
					}
					anc, ok := snap.Ancestors("D050")
					if !ok {
						errCh <- fmt.Errorf("half-state: concept visible but traversal unavailable")
						return
					}
					found := false
					for _, p := range anc {
						if p.ConceptID == "D001" {
							found = true
						}
					}
					if !found {
						errCh <- fmt.Errorf("half-state: D050 visible without ancestor D001: %+v", anc)
						return
					}
					// 概念可见时，同批写入的术语归属也必须一致可见。
					tres := r.ResolveTermIn(snap, "  NEWCONCEPT ")
					if tres == nil || tres.Conflict != nil ||
						len(tres.Candidates) != 1 || tres.Candidates[0].Assignment.ConceptID != "D050" {
						errCh <- fmt.Errorf("half-state: D050 visible without its term assignment: %+v", tres)
						return
					}
				}
			}
		}(i)
	}

	// 持续提交修订（新增/删除 D050），与读取并发。
	var last int64 = v1
	for round := 0; round < 20; round++ {
		res, err := store.ApplyRevision(ctx, kg.Revision{
			BaseVersion:     last,
			UpsertConcepts:  []kg.Concept{{ID: "D050", Name: "NewConcept", Source: "s"}},
			UpsertHierarchy: []kg.HierarchyEdge{{Parent: "D001", Child: "D050", Source: "s"}},
			UpsertTerms:     []kg.TermAssignment{{Term: "NewConcept", ConceptID: "D050", Source: "s"}},
		})
		if err != nil {
			t.Fatalf("add revision: %v", err)
		}
		last = res.Version
		_, err = store.ApplyRevision(ctx, kg.Revision{
			BaseVersion:    last,
			DeleteConcepts: []string{"D050"},
		})
		if err != nil {
			t.Fatalf("delete revision: %v", err)
		}
		last++
	}
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	t.Logf("JUDGEMENT %d readers observed only fully-consistent snapshots across revisions", readers)
}

// TestConcurrent_ConflictingWritesSerialised：两个写者竞争，恰好一个因 stale 被拒。
func TestConcurrent_ConflictingWritesSerialised(t *testing.T) {
	store, _, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.ApplyRevision(ctx, kg.Revision{
				BaseVersion:    v1,
				DeleteConcepts: []string{"D003"},
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if re, ok := err.(*kg.RejectError); ok && re.Errors[0].Reason == kg.RejectStaleRevision {
			rejected++
			t.Logf("JUDGEMENT lost-update prevented: %s", re.Errors[0].Message)
		} else {
			t.Fatalf("unexpected write error: %v", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("want exactly 1 accepted and 1 stale rejection, got accepted=%d rejected=%d", accepted, rejected)
	}
}
