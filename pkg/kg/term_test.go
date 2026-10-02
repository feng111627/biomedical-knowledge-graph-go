package kg_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func describeCandidates(cs []kg.TermCandidate) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.Source + "->" + c.ConceptID + "(" + c.Origin + ",raw=" + c.Raw + ")"
	}
	return strings.Join(parts, ",")
}

// TestTerm_NormalizeRule：归一规则本身——大小写、首尾/中间空白都被折叠。
func TestTerm_NormalizeRule(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Cold", "cold"},
		{"cold", "cold"},
		{"  cold  ", "cold"},
		{"\tCOLD\n", "cold"},
		{"Common   Cold", "common cold"},
		{" common\tcold \n", "common cold"},
		{"DIABETES Mellitus", "diabetes mellitus"},
		{"   ", ""},
	}
	for _, c := range cases {
		got := kg.NormalizeTerm(c.in)
		t.Logf("INPUT raw=%q JUDGEMENT normalized=%q", c.in, got)
		if got != c.want {
			t.Fatalf("NormalizeTerm(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestTerm_NormalizationVariantsResolveToSameConcept：
// 同一概念的不同原始写法（大小写/空白变化）必须解析到同一个概念，
// 且结果与规范写法一致。
func TestTerm_NormalizationVariantsResolveToSameConcept(t *testing.T) {
	_, r, _ := newImportedStore(t, "term_conflicts.json")
	variants := []string{"Diabetes", "diabetes", " DIABETES ", "\tDiabetes\n"}
	var first *kg.TermResolution
	for _, v := range variants {
		res, conflict := r.ResolveTerm(v)
		t.Logf("INPUT raw=%q JUDGEMENT normalized=%q candidates=%s",
			v, res.Normalized, describeCandidates(res.Candidates))
		if conflict != nil {
			t.Fatalf("diabetes must not conflict, got %+v", conflict)
		}
		if res.Normalized != "diabetes" {
			t.Fatalf("raw %q normalized to %q, want diabetes", v, res.Normalized)
		}
		if len(res.Candidates) == 0 {
			t.Fatalf("raw %q must resolve to D001", v)
		}
		for _, c := range res.Candidates {
			if c.ConceptID != "D001" || c.ConceptName != "Diabetes" {
				t.Fatalf("raw %q resolved to unexpected concept %+v", v, c)
			}
		}
		if first == nil {
			cp := res
			first = &cp
		} else if describeCandidates(first.Candidates) != describeCandidates(res.Candidates) {
			t.Fatalf("variants must give identical candidates:\n%s\nvs\n%s",
				describeCandidates(first.Candidates), describeCandidates(res.Candidates))
		}
	}
}

// TestTerm_InternalWhitespaceFolded：中间多余空白折叠后命中同一概念。
func TestTerm_InternalWhitespaceFolded(t *testing.T) {
	store := kg.NewStore()
	r := kg.NewReasoner(store)
	ds := kg.Dataset{
		Concepts: []kg.Concept{
			{ID: "D001", Name: "Diabetes Mellitus", Source: "s1"},
		},
	}
	if _, err := store.Import(context.Background(), ds); err != nil {
		t.Fatal(err)
	}
	res, _ := r.ResolveTerm("  diabetes\t mellitus ")
	t.Logf("INPUT raw=%q JUDGEMENT normalized=%q -> %s",
		"  diabetes\\t mellitus ", res.Normalized, describeCandidates(res.Candidates))
	if len(res.Candidates) != 1 || res.Candidates[0].ConceptID != "D001" {
		t.Fatalf("internal whitespace must fold to single space and hit D001, got %+v", res.Candidates)
	}
}

// TestTerm_SynonymAndNameResolvable：规范名与同义词都可作为术语解析。
func TestTerm_SynonymAndNameResolvable(t *testing.T) {
	_, r, _ := newImportedStore(t, "term_conflicts.json")
	res, conflict := r.ResolveTerm("DM")
	t.Logf("INPUT raw=%q JUDGEMENT normalized=%q candidates=%s",
		"DM", res.Normalized, describeCandidates(res.Candidates))
	if conflict != nil {
		t.Fatalf("dm agreed on D001 across sources, must not conflict: %+v", conflict)
	}
	if len(res.Candidates) != 3 { // 同义词(src1) + 两个来源的显式映射
		t.Fatalf("dm should have 3 evidenced candidates, got %s", describeCandidates(res.Candidates))
	}
	for _, c := range res.Candidates {
		if c.ConceptID != "D001" {
			t.Fatalf("dm candidate wrong: %+v", c)
		}
	}
	ev := r.Explain("term", "dm")
	if len(ev) != 3 {
		t.Fatalf("term evidence should cite 3 associations, got %+v", ev)
	}
	t.Logf("JUDGEMENT evidence: %s | %s | %s", ev[0].Detail, ev[1].Detail, ev[2].Detail)
}

// TestTerm_SameSourceConflict：同一来源把同一术语指向不同概念。
func TestTerm_SameSourceConflict(t *testing.T) {
	_, r, _ := newImportedStore(t, "term_conflicts.json")
	t.Log("INPUT resolve '  COLD ' (src1: cold->D004, Cold->D002; src2: COLD->D003)")
	res, conflict := r.ResolveTerm("  COLD ")
	if conflict == nil {
		t.Fatal("cold must be flagged as conflict")
	}
	if conflict.Type != kg.ConflictTermSameSource {
		t.Fatalf("want term_same_source_conflict, got %s", conflict.Type)
	}
	if conflict.Source != "src1" {
		t.Fatalf("same-source conflict must identify src1, got %q", conflict.Source)
	}
	// 同源冲突的候选只含该来源的候选，且全部保留、不替调用方挑一个。
	if len(conflict.Candidates) != 3 {
		t.Fatalf("src1 candidates must all be kept, got %s", describeCandidates(conflict.Candidates))
	}
	if !res.Conflicted {
		t.Fatal("resolution must be marked conflicted")
	}
	// 解析结果仍给出全部来源的全部候选。
	if len(res.Candidates) != 4 {
		t.Fatalf("resolution must expose all 4 candidates, got %s", describeCandidates(res.Candidates))
	}
	t.Logf("JUDGEMENT same-source conflict source=%s candidates=%s",
		conflict.Source, describeCandidates(conflict.Candidates))
}

// TestTerm_CrossSourceConflict：不同来源把同一术语指向互斥概念。
func TestTerm_CrossSourceConflict(t *testing.T) {
	_, r, _ := newImportedStore(t, "term_conflicts.json")
	t.Log("INPUT resolve 'T1DM' (src2 synonym -> D002; src1 mapping -> D003)")
	res, conflict := r.ResolveTerm("T1DM")
	if conflict == nil {
		t.Fatal("t1dm must be flagged as conflict")
	}
	if conflict.Type != kg.ConflictTermCrossSource {
		t.Fatalf("want term_cross_source_conflict, got %s", conflict.Type)
	}
	if conflict.Source != "" {
		t.Fatalf("cross-source conflict has no single source, got %q", conflict.Source)
	}
	if len(conflict.Candidates) != 2 || len(res.Candidates) != 2 {
		t.Fatalf("both candidates must be kept, got %s", describeCandidates(conflict.Candidates))
	}
	t.Logf("JUDGEMENT cross-source conflict candidates=%s", describeCandidates(conflict.Candidates))
}

// TestTerm_ConflictDeterministic：同样输入重复跑，冲突判定与顺序完全一致。
func TestTerm_ConflictDeterministic(t *testing.T) {
	_, r, _ := newImportedStore(t, "term_conflicts.json")
	first := ""
	for i := 0; i < 5; i++ {
		res, c := r.ResolveTerm("cold")
		var sb strings.Builder
		sb.WriteString(string(c.Type))
		sb.WriteString("@")
		sb.WriteString(c.Source)
		for _, cand := range res.Candidates {
			sb.WriteString("|")
			sb.WriteString(cand.Source)
			sb.WriteString("->")
			sb.WriteString(cand.ConceptID)
			sb.WriteString("->")
			sb.WriteString(cand.Raw)
		}
		if i == 0 {
			first = sb.String()
		} else if sb.String() != first {
			t.Fatalf("non-deterministic term resolution:\n%s\nvs\n%s", first, sb.String())
		}
	}
	t.Logf("JUDGEMENT stable across repeated runs: %s", first)
}

// TestTerm_ConflictClearedAfterRemovingMapping：删除冲突方后冲突标记按规则变化：
// 先由同源冲突降级为跨来源冲突，再删另一方后彻底消失。
func TestTerm_ConflictClearedAfterRemovingMapping(t *testing.T) {
	store, r, v := newImportedStore(t, "term_conflicts.json")
	res1, err := store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion: v,
		DeleteTermMappings: []kg.TermAssignment{
			{Term: "Cold", ConceptID: "D002", Source: "src1"},
		},
	})
	if err != nil {
		t.Fatalf("revision 1: %v", err)
	}
	// src1 只剩 D004，但 src2 仍指向 D003：同源冲突消失，暴露为跨来源冲突。
	res, conflict := r.ResolveTerm("cold")
	t.Logf("INPUT delete src1 Cold->D002 then resolve 'cold' JUDGEMENT conflict=%v candidates=%s",
		conflict != nil, describeCandidates(res.Candidates))
	if conflict == nil || conflict.Type != kg.ConflictTermCrossSource {
		t.Fatalf("after removing src1's second target, want cross-source conflict, got %+v", conflict)
	}
	found := false
	for _, tc := range res1.TermConflicts {
		if tc.Term == "cold" && tc.Type == kg.ConflictTermCrossSource {
			found = true
		}
	}
	if !found {
		t.Fatalf("revision result must report cold as cross-source term conflict, got %+v", res1.TermConflicts)
	}

	_, err = store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion: res1.Version,
		DeleteTermMappings: []kg.TermAssignment{
			{Term: "COLD", ConceptID: "D003", Source: "src2"},
		},
	})
	if err != nil {
		t.Fatalf("revision 2: %v", err)
	}
	res, conflict = r.ResolveTerm("cold")
	t.Logf("INPUT delete src2 COLD->D003 then resolve 'cold' JUDGEMENT conflict=%v candidates=%s",
		conflict != nil, describeCandidates(res.Candidates))
	if conflict != nil {
		t.Fatalf("conflict must be fully cleared, still %+v", conflict)
	}
	if len(res.Candidates) != 2 { // src1 的同义词 + 显式映射，均指向 D004
		t.Fatalf("only src1->D004 associations should remain, got %s", describeCandidates(res.Candidates))
	}
	for _, c := range res.Candidates {
		if c.ConceptID != "D004" {
			t.Fatalf("remaining candidate must be D004, got %+v", c)
		}
	}
}

// TestTerm_CoexistsWithIdentifierMapping：术语冲突与标识映射冲突互不影响。
func TestTerm_CoexistsWithIdentifierMapping(t *testing.T) {
	_, r, _ := newImportedStore(t, "term_conflicts.json")
	// 标识映射行为不变：EXT:CROSS 仍是 cross_source_conflict。
	views, mc := r.ResolveMapping("EXT:CROSS")
	if mc == nil || mc.Type != kg.ConflictCrossSource {
		t.Fatalf("identifier mapping conflict must be unaffected, got %+v", mc)
	}
	if len(views) != 2 {
		t.Fatalf("identifier mapping candidates changed: %+v", views)
	}
	// 术语解析独立于标识映射：同一概念集合上的术语冲突类型不同。
	_, tc := r.ResolveTerm("t1dm")
	if tc == nil || tc.Type != kg.ConflictTermCrossSource {
		t.Fatalf("term conflict must be reported independently, got %+v", tc)
	}
	if tc.Type == mc.Type {
		t.Fatalf("term conflict type %s must be distinguishable from identifier conflict %s", tc.Type, mc.Type)
	}
	t.Logf("JUDGEMENT identifier conflict=%s and term conflict=%s coexist independently", mc.Type, tc.Type)
}

// TestTerm_DeleteConceptInvalidatesConclusions：概念删除后术语结论失效。
func TestTerm_DeleteConceptInvalidatesConclusions(t *testing.T) {
	store, r, v := newImportedStore(t, "term_conflicts.json")
	// 先确认删除前能解析。
	if res, _ := r.ResolveTerm("diabetes"); len(res.Candidates) == 0 {
		t.Fatal("diabetes must resolve before delete")
	}
	_, err := store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion:    v,
		DeleteConcepts: []string{"D001"},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	res, _ := r.ResolveTerm("diabetes")
	t.Logf("INPUT delete D001 then resolve 'diabetes' JUDGEMENT candidates=%s",
		describeCandidates(res.Candidates))
	if len(res.Candidates) != 0 {
		t.Fatalf("term conclusion for deleted concept must not be returned, got %s",
			describeCandidates(res.Candidates))
	}
	// 失效记录里必须能识别术语结论的失效（规范名术语 + 显式术语映射）。
	var termStale, termMappingStale bool
	for _, s := range r.StaleConclusions() {
		t.Logf("JUDGEMENT stale kind=%s subject=%s reason=%s detail=%s", s.Kind, s.SubjectID, s.Reason, s.Detail)
		if s.Kind == "term" && s.SubjectID == "D001" && s.Reason == "concept_deleted" {
			termStale = true
		}
		if s.Kind == "term_mapping" && s.SubjectID == "D001" && s.Reason == "concept_deleted" {
			termMappingStale = true
		}
	}
	if !termStale || !termMappingStale {
		t.Fatalf("expected term and term_mapping staleness for D001, got term=%v term_mapping=%v",
			termStale, termMappingStale)
	}
}

// TestTerm_RenameInvalidatesOldName：改名后旧名术语结论失效、新名可解析。
func TestTerm_RenameInvalidatesOldName(t *testing.T) {
	store, r, v := newImportedStore(t, "term_conflicts.json")
	_, err := store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion:    v,
		RenameConcepts: []kg.Rename{{ID: "D001", NewName: "DiabetesMellitus", Source: "src1"}},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	// 旧规范名仍可通过显式术语映射 "  Diabetes " -> D001 命中（映射未删），
	// 但规范名来源的候选必须消失。
	res, _ := r.ResolveTerm("diabetes")
	t.Logf("INPUT rename D001 -> DiabetesMellitus then resolve 'diabetes' JUDGEMENT candidates=%s",
		describeCandidates(res.Candidates))
	for _, c := range res.Candidates {
		if c.Origin == kg.TermOriginConceptName {
			t.Fatalf("old canonical name must no longer resolve, got %+v", c)
		}
		if c.ConceptName != "DiabetesMellitus" {
			t.Fatalf("candidate must show new canonical name, got %+v", c)
		}
	}
	// 新规范名可解析。
	res2, _ := r.ResolveTerm(" diabetesmellitus ")
	if len(res2.Candidates) == 0 {
		t.Fatal("new canonical name must resolve")
	}
	// 失效记录可识别旧名术语结论失效。
	found := false
	for _, s := range r.StaleConclusions() {
		t.Logf("JUDGEMENT stale kind=%s reason=%s detail=%s", s.Kind, s.Reason, s.Detail)
		if s.Kind == "term" && s.Reason == "concept_renamed" && strings.Contains(s.Detail, "Diabetes") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected term staleness for renamed concept")
	}
}

// TestTerm_RollbackRestoresResolution：回滚后术语解析结果一起退回。
func TestTerm_RollbackRestoresResolution(t *testing.T) {
	store, r, v := newImportedStore(t, "term_conflicts.json")
	before, _ := r.ResolveTerm("diabetes")
	_, err := store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion:    v,
		DeleteConcepts: []string{"D001"},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	if res, _ := r.ResolveTerm("diabetes"); len(res.Candidates) != 0 {
		t.Fatal("sanity: diabetes must be gone after delete")
	}
	if err := store.Rollback(context.Background(), v); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	after, _ := r.ResolveTerm("diabetes")
	t.Logf("INPUT rollback to v%d then resolve 'diabetes' JUDGEMENT candidates=%s",
		v, describeCandidates(after.Candidates))
	if describeCandidates(before.Candidates) != describeCandidates(after.Candidates) {
		t.Fatalf("rollback must restore term resolution:\n%s\nvs\n%s",
			describeCandidates(before.Candidates), describeCandidates(after.Candidates))
	}
	// 术语冲突标记也一起恢复。
	if _, c := r.ResolveTerm("cold"); c == nil || c.Type != kg.ConflictTermSameSource {
		t.Fatalf("rollback must restore term conflict, got %+v", c)
	}
}

// TestTerm_RevisionAddsTermMapping：修订中新增术语映射立即可解析。
func TestTerm_RevisionAddsTermMapping(t *testing.T) {
	store, r, v := newImportedStore(t, "term_conflicts.json")
	res, err := store.ApplyRevision(context.Background(), kg.Revision{
		BaseVersion: v,
		UpsertTermMappings: []kg.TermAssignment{
			{Term: "Sugar Diabetes", ConceptID: "D001", Source: "src1"},
		},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	t.Logf("JUDGEMENT revision committed version=%d term_conflicts=%d", res.Version, len(res.TermConflicts))
	got, _ := r.ResolveTerm("  sugar   DIABETES ")
	t.Logf("INPUT resolve '  sugar   DIABETES ' JUDGEMENT normalized=%q candidates=%s",
		got.Normalized, describeCandidates(got.Candidates))
	if len(got.Candidates) != 1 || got.Candidates[0].ConceptID != "D001" {
		t.Fatalf("new term mapping must resolve, got %+v", got.Candidates)
	}
}

// TestTerm_DanglingMappingRejected：术语映射引用未定义概念时整体拒绝。
func TestTerm_DanglingMappingRejected(t *testing.T) {
	store := kg.NewStore()
	_, err := store.Import(context.Background(), kg.Dataset{
		Concepts:     []kg.Concept{{ID: "D001", Name: "Diabetes", Source: "s1"}},
		TermMappings: []kg.TermAssignment{{Term: "cold", ConceptID: "D999", Source: "s1"}},
	})
	if err == nil {
		t.Fatal("term mapping to undefined concept must be rejected")
	}
	t.Logf("INPUT term mapping cold->D999 (undefined) JUDGEMENT rejected: %v", err)
	re, ok := err.(*kg.RejectError)
	if !ok || re.Errors[0].Reason != kg.RejectDangling {
		t.Fatalf("want dangling_reference, got %v", err)
	}
}

// TestTerm_ConcurrentReadsSeeConsistentSnapshot：修订进行中的并发术语解析，
// 只能看到完整旧版本或完整新版本：术语候选可见时，其概念与规范名必然同时一致可见。
func TestTerm_ConcurrentReadsSeeConsistentSnapshot(t *testing.T) {
	store, r, v1 := newImportedStore(t, "term_conflicts.json")
	ctx := context.Background()

	const readers = 8
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, readers)

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := store.Current()
				res, _ := snap.ResolveTerm("  ColdRemedy ")
				if res.Normalized != "coldremedy" {
					errCh <- fmt.Errorf("normalization unstable: %q", res.Normalized)
					return
				}
				if len(res.Candidates) > 0 {
					// 候选可见时，概念本身与规范名必须同时一致可见。
					view, ok := r.GetConceptIn(snap, "D060")
					if !ok {
						errCh <- fmt.Errorf("half-state: term candidate visible but concept D060 missing")
						return
					}
					if view.Concept.Name != "ColdRemedy" {
						errCh <- fmt.Errorf("half-state: name mismatch %q", view.Concept.Name)
						return
					}
					for _, c := range res.Candidates {
						if c.ConceptName != "ColdRemedy" {
							errCh <- fmt.Errorf("half-state: candidate name mismatch %+v", c)
							return
						}
					}
				}
			}
		}()
	}

	last := v1
	for round := 0; round < 20; round++ {
		res, err := store.ApplyRevision(ctx, kg.Revision{
			BaseVersion:    last,
			UpsertConcepts: []kg.Concept{{ID: "D060", Name: "ColdRemedy", Source: "s"}},
			UpsertTermMappings: []kg.TermAssignment{
				{Term: "ColdRemedy", ConceptID: "D060", Source: "s"},
			},
		})
		if err != nil {
			t.Fatalf("add revision: %v", err)
		}
		last = res.Version
		res, err = store.ApplyRevision(ctx, kg.Revision{
			BaseVersion:    last,
			DeleteConcepts: []string{"D060"},
		})
		if err != nil {
			t.Fatalf("delete revision: %v", err)
		}
		last = res.Version
	}
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	t.Logf("JUDGEMENT %d readers observed only fully-consistent term resolutions across revisions", readers)
}
