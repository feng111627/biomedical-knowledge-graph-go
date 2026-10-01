package kg_test

import (
	"context"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func TestRollback_RestoresWholeState(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	res2, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		DeleteConcepts: []string{"D003"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("INPUT versions before rollback=%v deleted=D003", store.Versions())

	if _, ok := r.GetConcept("D003"); ok {
		t.Fatal("D003 should be deleted at v2")
	}

	if err := store.Rollback(ctx, v1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	t.Logf("JUDGEMENT rolled back to v%d; versions=%v", v1, store.Versions())

	if store.Current().Version() != v1 {
		t.Fatalf("current version = %d, want %d", store.Current().Version(), v1)
	}
	if _, ok := r.GetConcept("D003"); !ok {
		t.Fatal("D003 must be restored after whole-state rollback")
	}
	if paths, _ := r.Ancestors("D003"); len(paths) != 2 {
		t.Fatalf("ancestor conclusions must be restored, got %+v", paths)
	}
	if views, _ := r.ResolveMapping("ICD10:E11"); len(views) != 1 {
		t.Fatal("mapping must be restored after rollback")
	}

	// 回滚丢弃 v2 后，新提交必须成功且使用全新、更大的版本号（乐观版本不复用）。
	res3, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		UpsertConcepts: []kg.Concept{{ID: "D030", Name: "NewDrug", Source: "s"}},
	})
	if err != nil {
		t.Fatalf("commit after rollback: %v", err)
	}
	if res3.Version <= res2.Version {
		t.Fatalf("new version %d must be greater than rolled-back %d", res3.Version, res2.Version)
	}
	t.Logf("JUDGEMENT post-rollback commit version=%d (rolled-back version %d not reused)",
		res3.Version, res2.Version)
}

func TestRollback_UnknownVersionRejected(t *testing.T) {
	store, _, _ := newImportedStore(t, "ontology.json")
	err := store.Rollback(context.Background(), 999)
	if err == nil {
		t.Fatal("rollback to unknown version must fail")
	}
	t.Logf("JUDGEMENT %v", err)
}
