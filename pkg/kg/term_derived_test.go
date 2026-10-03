package kg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

// TestTerm_ConceptNameResolvesWithoutRegistration：概念规范名无需再抄进 terms
// 通道即可解析；大小写/前后空白变体的结果与规范写法逐字节一致。
func TestTerm_ConceptNameResolvesWithoutRegistration(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json") // 该数据集没有任何显式 terms

	t.Log("INPUT ResolveTerm canonical name variants of 'Diabetes' (D001, sources snomed_demo+icd_demo)")
	variants := []string{"Diabetes", "diabetes", "DIABETES", "  Diabetes  ", " DiAbEtEs\n"}
	var first *kg.TermResolution
	for _, q := range variants {
		got := r.ResolveTerm(q)
		if got == nil {
			t.Fatalf("concept name %q must resolve without explicit registration", q)
		}
		if got.NormalizedQuery != "diabetes" || got.MatchedTerm != "diabetes" {
			t.Fatalf("normalised correspondence wrong for %q: %+v", q, got)
		}
		if got.Conflict != nil {
			t.Fatalf("name of a single concept must not conflict, got %+v", got.Conflict)
		}
		if len(got.Candidates) != 2 {
			t.Fatalf("both defining sources must be cited, got %s", candidatesSig(got.Candidates))
		}
		for _, v := range got.Candidates {
			if v.Assignment.ConceptID != "D001" || v.Conflicted {
				t.Fatalf("candidate wrong for %q: %+v", q, v)
			}
		}
		if first == nil {
			first = got
		} else if candidatesSig(got.Candidates) != candidatesSig(first.Candidates) {
			t.Fatalf("variant %q differs from canonical %q: %s vs %s",
				q, first.Query, candidatesSig(got.Candidates), candidatesSig(first.Candidates))
		}
		t.Logf("JUDGEMENT %q -> normalized=%q -> D001 via %s (identical to canonical spelling)",
			q, got.NormalizedQuery, candidatesSig(got.Candidates))
	}

	// 全部来源依据可追溯。
	ev := r.Explain("term", " DIABETES ")
	if len(ev) != 2 {
		t.Fatalf("term evidence must cite both sources, got %+v", ev)
	}
	t.Logf("JUDGEMENT evidence for derived name term: %s=%s | %s=%s",
		ev[0].Source, ev[0].Detail, ev[1].Source, ev[1].Detail)
}

// TestTerm_SynonymResolvesWithoutRegistration：概念同义词（含多词、内部空白折叠）
// 直接解析到所属概念，重复执行结果与顺序完全一致。
func TestTerm_SynonymResolvesWithoutRegistration(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")

	t.Log("INPUT ResolveTerm synonym 'T2DM' (synonym of D003 asserted by icd_demo)")
	got := r.ResolveTerm(" t2dm ")
	if got == nil || len(got.Candidates) != 1 ||
		got.Candidates[0].Assignment.ConceptID != "D003" ||
		got.Candidates[0].Assignment.Source != "icd_demo" {
		t.Fatalf("synonym T2DM must resolve to D003 via icd_demo, got %+v", got)
	}
	t.Logf("JUDGEMENT ' t2dm ' -> normalized=%q -> D003 via %s",
		got.NormalizedQuery, candidatesSig(got.Candidates))

	t.Log("INPUT ResolveTerm multi-word synonym variants of 'Metformin HCl' (D020)")
	variants := []string{"Metformin HCl", "metformin hcl", "  Metformin   HCl ", "\tMETFORMIN\tHCL\n"}
	var firstSig string
	for i, q := range variants {
		got := r.ResolveTerm(q)
		if got == nil || got.NormalizedQuery != "metformin hcl" {
			t.Fatalf("multi-word synonym variant %q must normalise to 'metformin hcl', got %+v", q, got)
		}
		if len(got.Candidates) != 1 || got.Candidates[0].Assignment.ConceptID != "D020" {
			t.Fatalf("variant %q must resolve to D020, got %+v", q, got)
		}
		sig := candidatesSig(got.Candidates)
		if i == 0 {
			firstSig = sig
		} else if sig != firstSig {
			t.Fatalf("variant %q resolved differently: %s vs %s", q, sig, firstSig)
		}
		t.Logf("JUDGEMENT %q -> normalized=%q -> D020 (whitespace folded, case folded)", q, got.NormalizedQuery)
	}

	// 两来源都断言的同义词 "Diabetes mellitus"：两个来源都要出现在候选里。
	got = r.ResolveTerm("Diabetes   mellitus")
	if got == nil || len(got.Candidates) != 2 {
		t.Fatalf("synonym asserted by two sources must cite both, got %+v", got)
	}
	t.Logf("JUDGEMENT 'Diabetes   mellitus' -> D001 via %s", candidatesSig(got.Candidates))
}

// TestTerm_RenameRemovesOldTermConclusions：改名与删除保持一致——旧名及其
// 术语结论不再出现在正常解析结果里，失效信息可识别，回滚后整体恢复。
func TestTerm_RenameRemovesOldTermConclusions(t *testing.T) {
	store, r, v1 := newImportedStore(t, "term_conflicts.json")
	ctx := context.Background()

	if got := r.ResolveTerm("cold"); got == nil || len(got.Candidates) != 2 {
		t.Fatal("precondition: 'cold' resolves to D030 with 2 assignments")
	}
	t.Log("INPUT rename D030 'Cold' -> 'CommonCold' (source src3)")
	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		RenameConcepts: []kg.Rename{{ID: "D030", NewName: "CommonCold", Source: "src3"}},
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	// 旧名（含大小写变体）不能再作为正常结果查到——与删除概念的表现一致。
	for _, q := range []string{"cold", "Cold", " COLD "} {
		if got := r.ResolveTerm(q); got != nil {
			t.Fatalf("old name %q must not resolve after rename, got %+v", q, got)
		}
	}
	t.Logf("JUDGEMENT old name 'cold' (and variants) no longer resolves at v%d", res.Version)

	// 失效信息能看出哪条术语结论作废、因为什么作废。
	stale := r.StaleConclusions()
	found := map[string]bool{}
	for _, s := range stale {
		if s.Kind == "term" && s.Reason == "concept_renamed" && s.InvalidatedAt == res.Version {
			found[s.Detail] = true
			t.Logf("JUDGEMENT stale term entry: reason=%s detail=%q", s.Reason, s.Detail)
		}
	}
	if len(found) == 0 {
		t.Fatalf("rename must record invalidated term conclusions, got %+v", stale)
	}

	// 新规范名立即可解析（含变体）。
	got := r.ResolveTerm(" commoncold ")
	if got == nil || len(got.Candidates) != 1 || got.Candidates[0].Assignment.ConceptID != "D030" {
		t.Fatalf("new name must resolve to D030, got %+v", got)
	}
	t.Logf("JUDGEMENT ' commoncold ' -> normalized=%q -> D030 via %s",
		got.NormalizedQuery, candidatesSig(got.Candidates))

	// 回滚到改名前：旧术语结论整体恢复，新名不再可解析。
	if err := store.Rollback(ctx, v1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := r.ResolveTerm("COLD"); got == nil || len(got.Candidates) != 2 {
		t.Fatalf("old term conclusions must be restored after rollback, got %+v", got)
	}
	if got := r.ResolveTerm("commoncold"); got != nil {
		t.Fatalf("new name must not resolve after rollback, got %+v", got)
	}
	t.Logf("JUDGEMENT rollback to v%d restored 'COLD' -> D030 and dropped 'commoncold'", v1)
}

// TestTerm_RenameKeepsSynonymBackedTerms：改名只作废旧名与显式登记的术语结论；
// 仍由当前同义词支撑的归属保持有效，且不出现在失效信息里。
func TestTerm_RenameKeepsSynonymBackedTerms(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	t.Log("INPUT rename D003 'Type2Diabetes' -> 'Type2DiabetesMellitus'; synonyms T2DM/NIDDM unchanged")
	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		RenameConcepts: []kg.Rename{{ID: "D003", NewName: "Type2DiabetesMellitus", Source: "src_new"}},
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}

	if got := r.ResolveTerm("type2diabetes"); got != nil {
		t.Fatalf("old canonical name must not resolve after rename, got %+v", got)
	}
	t.Log("JUDGEMENT old name 'type2diabetes' no longer resolves")

	got := r.ResolveTerm(" T2DM ")
	if got == nil || len(got.Candidates) != 1 || got.Candidates[0].Assignment.ConceptID != "D003" {
		t.Fatalf("synonym-backed term must survive rename, got %+v", got)
	}
	t.Logf("JUDGEMENT synonym ' T2DM ' still resolves -> D003 via %s", candidatesSig(got.Candidates))

	got = r.ResolveTerm("type2diabetesmellitus")
	if got == nil || got.Candidates[0].Assignment.ConceptID != "D003" {
		t.Fatalf("new name must resolve to D003, got %+v", got)
	}
	t.Logf("JUDGEMENT new name resolves -> D003 via %s", candidatesSig(got.Candidates))

	stale := r.StaleConclusions()
	sawOldName, sawSynonym := false, false
	for _, s := range stale {
		if s.Kind == "term" && s.InvalidatedAt == res.Version {
			if strings.Contains(s.Detail, "'type2diabetes'") {
				sawOldName = true
			}
			if strings.Contains(s.Detail, "'t2dm'") || strings.Contains(s.Detail, "'niddm'") {
				sawSynonym = true
			}
			t.Logf("JUDGEMENT stale term entry: %s", s.Detail)
		}
	}
	if !sawOldName {
		t.Fatal("old name term conclusion must be recorded as stale")
	}
	if sawSynonym {
		t.Fatal("synonym-backed terms are still valid and must not be marked stale")
	}
}

// TestTerm_DerivedTermsCoexistWithMappings：派生术语（规范名/同义词）与外部标识
// 映射同时存在时互不干扰；标识映射的归一与冲突标记行为完全不变。
func TestTerm_DerivedTermsCoexistWithMappings(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	t.Log("INPUT ResolveMapping SNOMED:73211009 and ResolveTerm 'diabetes' on the same snapshot")
	mviews, mconf := r.ResolveMapping("SNOMED:73211009")
	if mconf != nil || len(mviews) != 1 || mviews[0].Assignment.ConceptID != "D001" {
		t.Fatalf("mapping behaviour must be unchanged: views=%+v conflict=%+v", mviews, mconf)
	}
	tres := r.ResolveTerm("diabetes")
	if tres == nil || tres.Conflict != nil || len(tres.Candidates) != 2 {
		t.Fatalf("derived term must resolve alongside mappings: %+v", tres)
	}
	t.Logf("JUDGEMENT mapping candidates=%d conflict=nil; derived term candidates=%d conflict=nil",
		len(mviews), len(tres.Candidates))

	// 给映射制造跨来源冲突，派生术语结论不受影响。
	if _, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion: v1,
		UpsertMappings: []kg.MappingAssignment{
			{TargetID: "SNOMED:73211009", ConceptID: "D002", Source: "icd_demo"},
		},
	}); err != nil {
		t.Fatalf("revision: %v", err)
	}
	_, mconf = r.ResolveMapping("SNOMED:73211009")
	if mconf == nil || mconf.Type != kg.ConflictCrossSource {
		t.Fatalf("mapping must now be cross-source conflict, got %+v", mconf)
	}
	tres = r.ResolveTerm("diabetes")
	if tres == nil || tres.Conflict != nil || len(tres.Candidates) != 2 {
		t.Fatalf("derived term must stay unaffected by mapping conflict, got %+v", tres)
	}
	t.Logf("JUDGEMENT mapping conflict=%s while derived term 'diabetes' still resolves cleanly with %d candidates",
		mconf.Type, len(tres.Candidates))
}

// TestTerm_DerivedTermConflictDeterministic：派生术语与显式术语指向不同概念时
// 同样按既有规则标记冲突，候选全保留、重复执行顺序一致。
func TestTerm_DerivedTermConflictDeterministic(t *testing.T) {
	store := kg.NewStore()
	r := kg.NewReasoner(store)
	t.Log("INPUT concept D100 name 'Aspirin' (srcA) + explicit term ' aspirin ' -> D200 (srcB)")
	_, err := store.Import(context.Background(), kg.Dataset{
		Concepts: []kg.Concept{
			{ID: "D100", Name: "Aspirin", Source: "srcA"},
			{ID: "D200", Name: "AspirinConcept2", Source: "srcB"},
		},
		Terms: []kg.TermAssignment{
			{Term: " aspirin ", ConceptID: "D200", Source: "srcB"},
		},
	})
	if err != nil {
		t.Fatalf("derived+explicit disagreement must be flagged, not rejected: %v", err)
	}
	var firstSig string
	for i := 0; i < 5; i++ {
		got := r.ResolveTerm("ASPIRIN")
		if got == nil || got.Conflict == nil {
			t.Fatalf("run %d: 'aspirin' must be a cross-source conflict, got %+v", i, got)
		}
		if got.Conflict.Type != kg.ConflictCrossSource {
			t.Fatalf("run %d: want cross_source_conflict, got %s", i, got.Conflict.Type)
		}
		if len(got.Candidates) != 2 {
			t.Fatalf("run %d: both candidates must be kept, got %s", i, candidatesSig(got.Candidates))
		}
		sig := string(got.Conflict.Type) + "|" + candidatesSig(got.Candidates)
		if i == 0 {
			firstSig = sig
		} else if sig != firstSig {
			t.Fatalf("run %d: non-deterministic conflict: %s vs %s", i, sig, firstSig)
		}
	}
	t.Logf("JUDGEMENT stable across runs: %s", firstSig)
}
