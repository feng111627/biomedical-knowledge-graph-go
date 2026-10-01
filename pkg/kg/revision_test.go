package kg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func TestRevision_UpsertExtendsHierarchyAtomically(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")

	res, err := store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion: v1,
		UpsertConcepts: []kg.Concept{
			{ID: "D004", Name: "GestationalDiabetes", Source: "src_new", Synonyms: []string{"GDM"}},
		},
		UpsertHierarchy: []kg.HierarchyEdge{
			{Parent: "D001", Child: "D004", Source: "src_new"},
		},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	t.Logf("JUDGEMENT revision accepted v%d -> v%d", v1, res.Version)

	paths, _ := r.Ancestors("D004")
	got := map[string]bool{}
	for _, p := range paths {
		got[p.ConceptID] = true
	}
	if !got["D001"] || !got["D010"] {
		t.Fatalf("new concept should transitively inherit D001 and D010, got %v", got)
	}
}

func TestRevision_StaleBaseVersionRejected(t *testing.T) {
	store, _, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	// 第一次修订成功，版本推进。
	if _, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		UpsertConcepts: []kg.Concept{{ID: "D004", Name: "X", Source: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 仍基于 v1 的第二次修订必须被拒绝为 stale_revision。
	_, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		UpsertConcepts: []kg.Concept{{ID: "D005", Name: "Y", Source: "s"}},
	})
	if err == nil {
		t.Fatal("stale revision must be rejected")
	}
	re := err.(*kg.RejectError)
	if len(re.Errors) != 1 || re.Errors[0].Reason != kg.RejectStaleRevision {
		t.Fatalf("want stale_revision, got %v", err)
	}
	t.Logf("JUDGEMENT %s: %s", re.Errors[0].Reason, re.Errors[0].Message)
}

func TestRevision_IntroducingCycleRejectedAtomically(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	// 试图加入 D010 is-a D003，制造 D010 -> D003 -> D001 -> D010 的环；
	// 同一修订还顺带 upsert 一个新概念，用于验证拒绝时它也不可见（原子性）。
	_, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:     v1,
		UpsertConcepts:  []kg.Concept{{ID: "D099", Name: "ShouldNotAppear", Source: "s"}},
		UpsertHierarchy: []kg.HierarchyEdge{{Parent: "D003", Child: "D010", Source: "s"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("revision creating a cycle must be rejected, got: %v", err)
	}
	t.Logf("JUDGEMENT cycle revision rejected: %v", err)
	if store.Current().Version() != v1 {
		t.Fatalf("version must stay at %d after rejection, got %d", v1, store.Current().Version())
	}
	if _, ok := r.GetConcept("D099"); ok {
		t.Fatal("concept upserted in rejected revision must not be visible")
	}
}

func TestRevision_DeleteInvalidatesDependentConclusions(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	// 删除前：D003 的祖先推导与映射解析均有结果。
	if paths, _ := r.Ancestors("D003"); len(paths) != 2 {
		t.Fatalf("precondition: D003 has 2 ancestors")
	}
	if views, _ := r.ResolveMapping("ICD10:E11"); len(views) != 1 {
		t.Fatal("precondition: ICD10:E11 maps to D003")
	}

	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		DeleteConcepts: []string{"D003"},
	})
	if err != nil {
		t.Fatalf("delete revision: %v", err)
	}
	t.Logf("JUDGEMENT delete accepted at v%d", res.Version)

	// 删除后：过期结论不再返回。
	if _, ok := r.GetConcept("D003"); ok {
		t.Fatal("deleted concept must not be returned")
	}
	if paths, ok := r.Ancestors("D003"); ok {
		t.Fatalf("ancestors of deleted concept must be gone, got %+v", paths)
	}
	if views, c := r.ResolveMapping("ICD10:E11"); views != nil || c != nil {
		t.Fatalf("mapping to deleted concept must be gone, got %+v", views)
	}
	if rels := r.Relations("D020", "treats"); len(rels) != 0 {
		t.Fatalf("relation depending on deleted concept must be gone, got %+v", rels)
	}

	// 失效结论必须可识别并说明原因。
	stale := r.StaleConclusions()
	if len(stale) == 0 {
		t.Fatal("stale conclusions should be recorded after delete")
	}
	kinds := map[string]bool{}
	for _, s := range stale {
		kinds[s.Kind] = true
		if s.Reason != "concept_deleted" || s.InvalidatedAt != res.Version {
			t.Fatalf("stale entry missing proper invalidation metadata: %+v", s)
		}
		t.Logf("JUDGEMENT stale kind=%s subject=%s detail=%q", s.Kind, s.SubjectID, s.Detail)
	}
	for _, want := range []string{"hierarchy", "relation", "mapping"} {
		if !kinds[want] {
			t.Fatalf("expected invalidated kind %s, got %v", want, kinds)
		}
	}
}

func TestRevision_RenameInvalidatesOldConclusions(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		RenameConcepts: []kg.Rename{{ID: "D003", NewName: "Type2DiabetesMellitus", Source: "src_new"}},
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	v, ok := r.GetConcept("D003")
	if !ok || v.Concept.Name != "Type2DiabetesMellitus" {
		t.Fatalf("rename not visible: %+v ok=%v", v, ok)
	}
	stale := r.StaleConclusions()
	found := false
	for _, s := range stale {
		if s.Kind == "rename" && s.Reason == "concept_renamed" && s.InvalidatedAt == res.Version {
			found = true
			t.Logf("JUDGEMENT %s: %s", s.Reason, s.Detail)
		}
	}
	if !found {
		t.Fatalf("rename must record an invalidation entry, got %+v", stale)
	}
}
