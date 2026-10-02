package kg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func importTerms(t *testing.T) (*kg.Store, *kg.Reasoner) {
	store := kg.NewStore()
	r := kg.NewReasoner(store)
	ds := loadFile(t, "term_conflicts.json")
	res, err := store.Import(context.Background(), ds)
	if err != nil {
		t.Fatalf("term-conflict dataset imports (term conflicts flagged, not rejected): %v", err)
	}
	t.Logf("JUDGEMENT import accepted with %d mapping conflicts and %d term conflicts",
		len(res.Conflicts), len(res.TermConflicts))
	types := map[kg.ConflictType]bool{}
	for _, c := range res.TermConflicts {
		types[c.Type] = true
		t.Logf("JUDGEMENT term conflict term=%q type=%s sources=%s",
			c.Term, c.Type, describeTermTargets(c.Targets))
	}
	if !types[kg.ConflictSameSource] || !types[kg.ConflictCrossSource] {
		t.Fatalf("expected both term conflict types flagged, got %v", types)
	}
	return store, r
}

func describeTermTargets(ts []kg.TermAssignment) string {
	parts := make([]string, len(ts))
	for i, x := range ts {
		parts[i] = x.Source + "->" + x.ConceptID
	}
	return strings.Join(parts, ",")
}

func TestTerm_NormalizationIsCaseAndWhitespaceInsensitive(t *testing.T) {
	_, r := importTerms(t)

	cases := []string{"Cold", "cold", "  Cold  ", "COLD", " CoLd ", "cold\n"}
	var first *kg.TermResolution
	for _, q := range cases {
		t.Logf("INPUT ResolveTerm %q", q)
		got := r.ResolveTerm(q)
		if got == nil {
			t.Fatalf("term %q must resolve", q)
		}
		if got.NormalizedQuery != "cold" {
			t.Fatalf("normalized form of %q must be \"cold\", got %q", q, got.NormalizedQuery)
		}
		if got.Conflict != nil {
			t.Fatalf("term %q must be conflict-free, got %s", q, got.Conflict.Type)
		}
		if len(got.Candidates) != 2 {
			t.Fatalf("term %q should expose 2 evidenced assignments, got %d", q, len(got.Candidates))
		}
		for _, v := range got.Candidates {
			if v.Assignment.ConceptID != "D030" || v.Conflicted {
				t.Fatalf("term %q candidate wrong: %+v", q, v)
			}
		}
		if first == nil {
			first = got
		} else {
			if candidatesSig(got.Candidates) != candidatesSig(first.Candidates) {
				t.Fatalf("variant %q resolved differently than %q: %s vs %s",
					q, first.Query, candidatesSig(got.Candidates), candidatesSig(first.Candidates))
			}
		}
		t.Logf("JUDGEMENT %q -> normalized=%q -> D030 via %s",
			q, got.NormalizedQuery, candidatesSig(got.Candidates))
	}

	// 多词术语：内部连续空白折叠为单空格（归一规则直接由 NormalizeTerm 覆盖）。
	if got := kg.NormalizeTerm("\t Hello   WORLD \n"); got != "hello world" {
		t.Fatalf("NormalizeTerm whitespace folding wrong, got %q", got)
	}
	t.Logf("JUDGEMENT NormalizeTerm(\"\\t Hello   WORLD \\n\") = %q", kg.NormalizeTerm("\t Hello   WORLD \n"))
}

func TestTerm_CrossSourceConflict(t *testing.T) {
	_, r := importTerms(t)
	t.Log("INPUT ResolveTerm 'T:CROSS' (src1 -> D001, src2 -> D003)")
	got := r.ResolveTerm("T:CROSS")
	if got == nil {
		t.Fatal("t:cross must resolve to a conflicted term")
	}
	if got.Conflict == nil {
		t.Fatal("t:cross must be flagged as conflict")
	}
	if got.Conflict.Type != kg.ConflictCrossSource {
		t.Fatalf("want cross_source_conflict, got %s", got.Conflict.Type)
	}
	if got.Conflict.Source != "" {
		t.Fatalf("cross-source conflict has no single source, got %q", got.Conflict.Source)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("conflicted term must return both candidates, got %d", len(got.Candidates))
	}
	for _, v := range got.Candidates {
		if !v.Conflicted {
			t.Fatalf("candidate %+v must be marked conflicted", v)
		}
	}
	t.Logf("JUDGEMENT cross-source term conflict candidates kept: %s",
		describeTermTargets(got.Conflict.Targets))
}

func TestTerm_SameSourceConflict(t *testing.T) {
	_, r := importTerms(t)
	t.Log("INPUT ResolveTerm '  T:SAMESRC  ' (src1 -> D001 and D002)")
	// 前后空白也必须命中同一归一化键。
	got := r.ResolveTerm("  T:SAMESRC  ")
	if got == nil || got.Conflict == nil {
		t.Fatalf("t:samesrc must be a conflict, got %+v", got)
	}
	if got.Conflict.Type != kg.ConflictSameSource {
		t.Fatalf("want same_source_conflict, got %s", got.Conflict.Type)
	}
	if got.Conflict.Source != "src1" {
		t.Fatalf("same-source conflict must identify source src1, got %q", got.Conflict.Source)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("same-source conflict must keep both candidates, got %d", len(got.Candidates))
	}
	t.Logf("JUDGEMENT same-source term conflict source=%s targets=%s",
		got.Conflict.Source, describeTermTargets(got.Conflict.Targets))
}

func TestTerm_AgreementResolvesWithEvidence(t *testing.T) {
	_, r := importTerms(t)
	t.Log("INPUT ResolveTerm ' DIABETES ' (src1 and src2 both -> D001)")
	got := r.ResolveTerm(" DIABETES ")
	if got == nil {
		t.Fatal("diabetes must resolve")
	}
	if got.Conflict != nil {
		t.Fatalf("agreed term must not conflict, got %+v", got.Conflict)
	}
	if got.NormalizedQuery != "diabetes" {
		t.Fatalf("normalized query wrong: %q", got.NormalizedQuery)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("agreed term should expose 2 evidenced assignments, got %d", len(got.Candidates))
	}
	for _, v := range got.Candidates {
		if v.Assignment.ConceptID != "D001" || v.Conflicted {
			t.Fatalf("diabetes candidate wrong: %+v", v)
		}
	}
	ev := r.Explain("term", " DIABETES ")
	if len(ev) != 2 {
		t.Fatalf("term evidence should cite 2 sources, got %+v", ev)
	}
	t.Logf("JUDGEMENT consistent term evidence: %s | %s", ev[0].Detail, ev[1].Detail)
}

func TestTerm_ConflictDeterministicAcrossRepeats(t *testing.T) {
	_, r := importTerms(t)
	first := ""
	for i := 0; i < 5; i++ {
		got := r.ResolveTerm("t:cross")
		if got == nil || got.Conflict == nil {
			t.Fatal("t:cross must conflict on every run")
		}
		var sb strings.Builder
		sb.WriteString(string(got.Conflict.Type))
		for _, v := range got.Candidates {
			sb.WriteString("|")
			sb.WriteString(v.Assignment.Source)
			sb.WriteString("->")
			sb.WriteString(v.Assignment.ConceptID)
		}
		if i == 0 {
			first = sb.String()
		} else if sb.String() != first {
			t.Fatalf("non-deterministic term conflict:\n%s\nvs\n%s", first, sb.String())
		}
	}
	t.Logf("JUDGEMENT stable across repeated runs: %s", first)
}

func TestTerm_UnknownTermReturnsNil(t *testing.T) {
	_, r := importTerms(t)
	if got := r.ResolveTerm("no-such-term"); got != nil {
		t.Fatalf("unknown term must resolve to nil, got %+v", got)
	}
	t.Log("JUDGEMENT unknown term 'no-such-term' -> nil")
}

func candidatesSig(vs []kg.TermView) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = v.Assignment.Source + "->" + v.Assignment.ConceptID
	}
	return strings.Join(parts, ",")
}

func TestTerm_CoexistsWithExternalIDMapping(t *testing.T) {
	store, r := importTerms(t)
	ctx := context.Background()

	t.Log("INPUT ResolveMapping EXT:OK and ResolveTerm 'diabetes' on the same snapshot")
	mviews, mconf := r.ResolveMapping("EXT:OK")
	if mconf != nil || len(mviews) != 2 {
		t.Fatalf("existing mapping behaviour must be unchanged: views=%d conflict=%+v", len(mviews), mconf)
	}
	tres := r.ResolveTerm("diabetes")
	if tres == nil || tres.Conflict != nil || len(tres.Candidates) != 2 {
		t.Fatalf("term resolution must work alongside mappings: %+v", tres)
	}
	t.Logf("JUDGEMENT mapping candidates=%d conflict=nil; term candidates=%d conflict=nil",
		len(mviews), len(tres.Candidates))

	// 标识映射冲突与术语冲突彼此独立：给 EXT:OK 引入跨来源冲突，术语结论不变。
	v1 := store.Current().Version()
	_, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion: v1,
		UpsertMappings: []kg.MappingAssignment{
			{TargetID: "EXT:OK", ConceptID: "D003", Source: "src3"},
		},
	})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	_, mconf = r.ResolveMapping("EXT:OK")
	if mconf == nil || mconf.Type != kg.ConflictCrossSource {
		t.Fatalf("EXT:OK should now be cross-source conflict, got %+v", mconf)
	}
	tres = r.ResolveTerm("diabetes")
	if tres == nil || tres.Conflict != nil {
		t.Fatalf("term 'diabetes' must stay unaffected by mapping conflict, got %+v", tres)
	}
	t.Logf("JUDGEMENT after mapping-only conflict: mapping=%s, term conflict=%v",
		mconf.Type, tres.Conflict)
}

func TestTerm_DeleteConceptInvalidatesAndRemovesTerm(t *testing.T) {
	store, r, v1 := newImportedStore(t, "term_conflicts.json")
	ctx := context.Background()

	if got := r.ResolveTerm("cold"); got == nil || len(got.Candidates) != 2 {
		t.Fatal("precondition: term 'cold' resolves with 2 assignments")
	}

	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		DeleteConcepts: []string{"D030"},
	})
	if err != nil {
		t.Fatalf("delete revision: %v", err)
	}
	t.Logf("JUDGEMENT delete D030 accepted at v%d", res.Version)

	if got := r.ResolveTerm("cold"); got != nil {
		t.Fatalf("term relying on deleted concept must not be returned, got %+v", got)
	}
	stale := r.StaleConclusions()
	foundTerm := false
	for _, s := range stale {
		if s.Kind == "term" && s.Reason == "concept_deleted" && s.InvalidatedAt == res.Version {
			foundTerm = true
			t.Logf("JUDGEMENT stale term entry: %s", s.Detail)
		}
	}
	if !foundTerm {
		t.Fatalf("term conclusion must be recorded as stale after concept delete, got %+v", stale)
	}
	// 回滚后术语结论整体恢复。
	if err := store.Rollback(ctx, v1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := r.ResolveTerm("COLD"); got == nil || len(got.Candidates) != 2 {
		t.Fatalf("term must be restored after rollback, got %+v", got)
	}
	t.Log("JUDGEMENT after rollback 'COLD' resolves again")
}

func TestTerm_RenameConceptInvalidatesTermConclusion(t *testing.T) {
	store, r, v1 := newImportedStore(t, "term_conflicts.json")
	ctx := context.Background()

	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		RenameConcepts: []kg.Rename{{ID: "D030", NewName: "CommonCold", Source: "src3"}},
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	stale := r.StaleConclusions()
	found := false
	for _, s := range stale {
		if s.Kind == "term" && s.Reason == "concept_renamed" && s.InvalidatedAt == res.Version {
			found = true
			t.Logf("JUDGEMENT rename invalidated term conclusion: %s", s.Detail)
		}
	}
	if !found {
		t.Fatalf("rename must invalidate prior term conclusions, got %+v", stale)
	}
}

func TestTerm_RevisionUpsertDeleteAndConflictClear(t *testing.T) {
	store, r := importTerms(t)
	ctx := context.Background()
	v1 := store.Current().Version()

	// 新增一个一致的术语归属（不同表面形式，归一后同键），冲突不应产生。
	_, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion: v1,
		UpsertTerms: []kg.TermAssignment{
			{Term: " COLD ", ConceptID: "D030", Source: "src3"},
		},
	})
	if err != nil {
		t.Fatalf("upsert term revision: %v", err)
	}
	got := r.ResolveTerm("cold")
	if got == nil || got.Conflict != nil || len(got.Candidates) != 3 {
		t.Fatalf("after agreeing upsert want 3 candidates no conflict, got %+v", got)
	}
	t.Logf("JUDGEMENT agreeing term upsert -> %d candidates", len(got.Candidates))

	// 删除跨来源冲突的一方，冲突标记必须在下一版本消失。
	v2 := store.Current().Version()
	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion: v2,
		DeleteTerms: []kg.TermAssignment{
			{Term: "t:cross", ConceptID: "D003", Source: "src2"},
		},
	})
	if err != nil {
		t.Fatalf("delete term revision: %v", err)
	}
	got = r.ResolveTerm("T:CROSS")
	if got == nil {
		t.Fatal("t:cross should still resolve after one side removed")
	}
	if got.Conflict != nil {
		t.Fatalf("term conflict must clear after removing one side, still %s", got.Conflict.Type)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Assignment.ConceptID != "D001" {
		t.Fatalf("only src1->D001 should remain, got %+v", got.Candidates)
	}
	t.Logf("JUDGEMENT conflict cleared at v%d, remaining %s",
		res.Version, candidatesSig(got.Candidates))
}

func TestTerm_DanglingReferenceRejected(t *testing.T) {
	store := kg.NewStore()
	_, err := store.Import(context.Background(), kg.Dataset{
		Concepts: []kg.Concept{{ID: "D001", Name: "A", Source: "s"}},
		Terms: []kg.TermAssignment{
			{Term: "ghost", Normalized: "ghost", ConceptID: "D999", Source: "s"},
		},
	})
	if err == nil {
		t.Fatal("term referencing undefined concept must be rejected")
	}
	re, ok := err.(*kg.RejectError)
	if !ok || re.Errors[0].Reason != kg.RejectDangling {
		t.Fatalf("want dangling_reference rejection, got %v", err)
	}
	t.Logf("JUDGEMENT dangling term rejected: %s", re.Errors[0].Message)
}
