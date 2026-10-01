package kg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func importMappings(t *testing.T) (*kg.Store, *kg.Reasoner) {
	store := kg.NewStore()
	r := kg.NewReasoner(store)
	ds := loadFile(t, "mapping_conflicts.json")
	res, err := store.Import(context.Background(), ds)
	if err != nil {
		t.Fatalf("mapping-conflict dataset imports (conflicts are flagged, not rejected): %v", err)
	}
	t.Logf("JUDGEMENT import accepted with %d flagged conflicts", len(res.Conflicts))
	types := map[kg.ConflictType]bool{}
	for _, c := range res.Conflicts {
		types[c.Type] = true
		t.Logf("JUDGEMENT conflict target=%s type=%s sources=%s",
			c.TargetID, c.Type, describeTargets(c.Targets))
	}
	if !types[kg.ConflictSameSource] || !types[kg.ConflictCrossSource] {
		t.Fatalf("expected both conflict types flagged, got %v", types)
	}
	return store, r
}

func describeTargets(ts []kg.MappingAssignment) string {
	parts := make([]string, len(ts))
	for i, x := range ts {
		parts[i] = x.Source + "->" + x.ConceptID
	}
	return strings.Join(parts, ",")
}

func TestMapping_CrossSourceConflict(t *testing.T) {
	_, r := importMappings(t)
	t.Log("INPUT resolve EXT:CROSS (src1->D001, src2->D003)")
	views, conflict := r.ResolveMapping("EXT:CROSS")
	if conflict == nil {
		t.Fatal("EXT:CROSS must be flagged as conflict")
	}
	if conflict.Type != kg.ConflictCrossSource {
		t.Fatalf("want cross_source_conflict, got %s", conflict.Type)
	}
	if conflict.Source != "" {
		t.Fatalf("cross-source conflict has no single source, got %q", conflict.Source)
	}
	// 冲突时必须把全部候选返回，不能替调用方挑一个。
	if len(views) != 2 {
		t.Fatalf("conflicted mapping must return both candidates, got %d", len(views))
	}
	for _, v := range views {
		if !v.Conflicted {
			t.Fatalf("candidate %+v must be marked conflicted", v)
		}
	}
	t.Logf("JUDGEMENT cross-source conflict, candidates kept: %s", describeTargets(conflict.Targets))
}

func TestMapping_SameSourceConflict(t *testing.T) {
	_, r := importMappings(t)
	t.Log("INPUT resolve EXT:SAMESRC (src1 -> D001 and D002)")
	_, conflict := r.ResolveMapping("EXT:SAMESRC")
	if conflict == nil {
		t.Fatal("EXT:SAMESRC must be a conflict")
	}
	if conflict.Type != kg.ConflictSameSource {
		t.Fatalf("want same_source_conflict, got %s", conflict.Type)
	}
	if conflict.Source != "src1" {
		t.Fatalf("same-source conflict must identify source src1, got %q", conflict.Source)
	}
	t.Logf("JUDGEMENT same-source conflict source=%s targets=%s",
		conflict.Source, describeTargets(conflict.Targets))
}

func TestMapping_ConsistentAcrossSourcesResolves(t *testing.T) {
	_, r := importMappings(t)
	t.Log("INPUT resolve EXT:OK (src1 -> D001, src2 -> D001)")
	views, conflict := r.ResolveMapping("EXT:OK")
	if conflict != nil {
		t.Fatalf("agreed mapping must not conflict, got %+v", conflict)
	}
	if len(views) != 2 {
		t.Fatalf("agreed mapping should expose 2 evidenced assignments, got %d", len(views))
	}
	for _, v := range views {
		if v.Assignment.ConceptID != "D001" || v.Conflicted {
			t.Fatalf("EXT:OK assignment wrong: %+v", v)
		}
	}
	ev := r.Explain("mapping", "EXT:OK")
	if len(ev) != 2 {
		t.Fatalf("mapping evidence should cite 2 sources, got %+v", ev)
	}
	t.Logf("JUDGEMENT consistent mapping evidence: %s | %s", ev[0].Detail, ev[1].Detail)
}

func TestMapping_ConflictResolutionIsDeterministic(t *testing.T) {
	store := kg.NewStore()
	ds := loadFile(t, "mapping_conflicts.json")
	if _, err := store.Import(context.Background(), ds); err != nil {
		t.Fatal(err)
	}
	r := kg.NewReasoner(store)

	first := ""
	for i := 0; i < 5; i++ {
		views, c := r.ResolveMapping("EXT:CROSS")
		var sb strings.Builder
		sb.WriteString(string(c.Type))
		for _, v := range views {
			sb.WriteString("|")
			sb.WriteString(v.Assignment.Source)
			sb.WriteString("->")
			sb.WriteString(v.Assignment.ConceptID)
		}
		if i == 0 {
			first = sb.String()
		} else if sb.String() != first {
			t.Fatalf("non-deterministic conflict resolution:\n%s\nvs\n%s", first, sb.String())
		}
	}
	t.Logf("JUDGEMENT stable across repeated runs: %s", first)
}

func TestMapping_ConflictClearedAfterRemovingSource(t *testing.T) {
	store := kg.NewStore()
	r := kg.NewReasoner(store)
	ds := loadFile(t, "mapping_conflicts.json")
	res, err := store.Import(context.Background(), ds)
	if err != nil {
		t.Fatal(err)
	}
	// 删除冲突方之一后，冲突标记必须随之消失。
	_, err = store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion: res.Version,
		DeleteMappings: []kg.MappingAssignment{
			{TargetID: "EXT:CROSS", ConceptID: "D003", Source: "src2"},
		},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	views, conflict := r.ResolveMapping("EXT:CROSS")
	if conflict != nil {
		t.Fatalf("conflict must be cleared after removing one side, still %+v", conflict)
	}
	if len(views) != 1 || views[0].Assignment.ConceptID != "D001" {
		t.Fatalf("only src1->D001 should remain, got %+v", views)
	}
	t.Log("JUDGEMENT conflict cleared, remaining mapping src1->D001")
}
