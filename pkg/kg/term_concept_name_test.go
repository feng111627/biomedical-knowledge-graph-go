package kg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

// TestTermName_CanonicalNameResolvesWithoutRegistration：
// 概念规范名无需再抄一遍登记为术语，即可直接解析；
// 大小写/前后空白变体的结果与规范写法逐字节一致。
func TestTermName_CanonicalNameResolvesWithoutRegistration(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json") // 该数据集没有任何 terms 登记

	canonical := r.ResolveTerm("Diabetes")
	if canonical == nil {
		t.Fatal("canonical name 'Diabetes' must resolve without any term registration")
	}
	if canonical.Conflict != nil {
		t.Fatalf("canonical name must be conflict-free, got %s", canonical.Conflict.Type)
	}
	// D001 由 snomed_demo 与 icd_demo 两个来源以同名定义，依据必须给全。
	if len(canonical.Candidates) != 2 {
		t.Fatalf("want 2 evidenced candidates (snomed_demo, icd_demo), got %d", len(canonical.Candidates))
	}
	for _, v := range canonical.Candidates {
		if v.Assignment.ConceptID != "D001" || v.Conflicted {
			t.Fatalf("canonical name candidate wrong: %+v", v)
		}
	}
	t.Logf("JUDGEMENT 'Diabetes' -> D001 via %s (no registration needed)",
		candidatesSig(canonical.Candidates))

	// 大小写与前后空白变体：归一前后对应一致，候选与规范写法完全相同。
	for _, q := range []string{"diabetes", "DIABETES", "  Diabetes  ", " DiAbetes\n"} {
		t.Logf("INPUT ResolveTerm %q", q)
		got := r.ResolveTerm(q)
		if got == nil {
			t.Fatalf("variant %q of canonical name must resolve", q)
		}
		if got.NormalizedQuery != "diabetes" || got.MatchedTerm != "diabetes" {
			t.Fatalf("normalization correspondence wrong for %q: %+v", q, got)
		}
		if candidatesSig(got.Candidates) != candidatesSig(canonical.Candidates) {
			t.Fatalf("variant %q resolved differently from canonical form: %s vs %s",
				q, candidatesSig(got.Candidates), candidatesSig(canonical.Candidates))
		}
		t.Logf("JUDGEMENT %q -> normalized=%q identical to canonical 'Diabetes'",
			q, got.NormalizedQuery)
	}

	// 来源依据可追溯：Explain 给出两个来源的 concept_name 证据。
	ev := r.Explain("term", " DIABETES ")
	if len(ev) != 2 {
		t.Fatalf("canonical name evidence should cite 2 sources, got %+v", ev)
	}
	for _, e := range ev {
		if e.Kind != "concept_name" {
			t.Fatalf("canonical name evidence kind must be concept_name, got %+v", e)
		}
		t.Logf("JUDGEMENT evidence source=%s kind=%s detail=%q", e.Source, e.Kind, e.Detail)
	}
}

// TestTermName_SynonymResolvesWithVariants：
// 概念同义词（含多词、内部空白折叠变体）同样直接解析到对应概念。
func TestTermName_SynonymResolvesWithVariants(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")

	t.Log("INPUT ResolveTerm 'DM' (synonym of D001 from snomed_demo)")
	got := r.ResolveTerm("DM")
	if got == nil || len(got.Candidates) != 1 ||
		got.Candidates[0].Assignment.ConceptID != "D001" ||
		got.Candidates[0].Assignment.Source != "snomed_demo" {
		t.Fatalf("synonym 'DM' must resolve to D001 via snomed_demo, got %+v", got)
	}
	t.Logf("JUDGEMENT 'DM' -> D001 via %s", candidatesSig(got.Candidates))

	// 多词同义词：中间连续空白折叠后命中同一归一键。
	t.Log("INPUT ResolveTerm '  Diabetes   Mellitus ' (multi-word synonym, extra spaces)")
	got = r.ResolveTerm("  Diabetes   Mellitus ")
	if got == nil {
		t.Fatal("multi-word synonym with extra whitespace must resolve")
	}
	if got.NormalizedQuery != "diabetes mellitus" {
		t.Fatalf("whitespace folding wrong: %q", got.NormalizedQuery)
	}
	// snomed_demo 与 icd_demo 都登记了该同义词，两个来源都要在。
	if len(got.Candidates) != 2 {
		t.Fatalf("synonym 'Diabetes mellitus' should expose 2 sources, got %d", len(got.Candidates))
	}
	for _, v := range got.Candidates {
		if v.Assignment.ConceptID != "D001" || v.Conflicted {
			t.Fatalf("synonym candidate wrong: %+v", v)
		}
	}
	t.Logf("JUDGEMENT '  Diabetes   Mellitus ' -> D001 via %s", candidatesSig(got.Candidates))

	ev := r.Explain("term", "diabetes mellitus")
	if len(ev) != 2 || ev[0].Kind != "synonym" {
		t.Fatalf("synonym evidence must be kind=synonym with 2 sources, got %+v", ev)
	}
	t.Logf("JUDGEMENT synonym evidence: %s=%s, %s=%s",
		ev[0].Source, ev[0].Kind, ev[1].Source, ev[1].Kind)

	// 另一个概念的同义词互不串扰。
	if got := r.ResolveTerm(" t2dm "); got == nil ||
		len(got.Candidates) != 1 || got.Candidates[0].Assignment.ConceptID != "D003" {
		t.Fatalf("synonym 't2dm' must resolve to D003 only, got %+v", got)
	}
	t.Log("JUDGEMENT ' t2dm ' -> D003 (icd_demo), no cross-concept leakage")
}

// TestTermName_RegisteredTermStillWinsOverDerived：
// 同一归一键既有登记术语、又能从概念名派生时，行为与既往完全一致
// （只返回登记归属，候选数量不变），两条通道互不干扰。
func TestTermName_RegisteredTermStillWinsOverDerived(t *testing.T) {
	_, r := importTerms(t) // D030 规范名 "Cold"，且 "Cold"/"cold" 已登记为术语

	got := r.ResolveTerm("cold")
	if got == nil || got.Conflict != nil || len(got.Candidates) != 2 {
		t.Fatalf("registered term behaviour must be unchanged, got %+v", got)
	}
	for _, v := range got.Candidates {
		if v.Assignment.ConceptID != "D030" {
			t.Fatalf("registered candidate wrong: %+v", v)
		}
	}
	t.Logf("JUDGEMENT registered term 'cold' unchanged: %s (derived name entry not merged)",
		candidatesSig(got.Candidates))
}

// TestTermName_RenameRemovesOldTermConclusions：
// 改名与删除保持一致：旧术语结论不再出现在正常解析结果里，
// 失效信息可识别原因，回滚后整体恢复。
func TestTermName_RenameRemovesOldTermConclusions(t *testing.T) {
	store, r, v1 := newImportedStore(t, "term_conflicts.json")
	ctx := context.Background()

	if got := r.ResolveTerm("cold"); got == nil || len(got.Candidates) != 2 {
		t.Fatal("precondition: registered term 'cold' resolves with 2 assignments")
	}

	res, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1,
		RenameConcepts: []kg.Rename{{ID: "D030", NewName: "CommonCold", Source: "src3"}},
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	t.Logf("JUDGEMENT rename D030 Cold->CommonCold accepted at v%d", res.Version)

	// 与删除一致：旧术语结论不能再作为正常、无冲突的结果返回。
	if got := r.ResolveTerm("cold"); got != nil {
		t.Fatalf("term conclusion derived from renamed concept must not resolve, got %+v", got)
	}
	t.Log("JUDGEMENT ResolveTerm 'cold' -> nil after rename (parity with delete)")

	// 失效信息能看出已作废、因为什么作废。
	stale := r.StaleConclusions()
	foundTerm := false
	for _, s := range stale {
		if s.Kind == "term" && s.SubjectID == "D030" &&
			s.Reason == "concept_renamed" && s.InvalidatedAt == res.Version {
			foundTerm = true
			t.Logf("JUDGEMENT stale term entry: %s (reason=%s)", s.Detail, s.Reason)
		}
	}
	if !foundTerm {
		t.Fatalf("rename must record term invalidation, got %+v", stale)
	}

	// 新规范名立即可解析（概念自身定义派生，无需重新登记）。
	got := r.ResolveTerm(" commoncold ")
	if got == nil || len(got.Candidates) != 1 ||
		got.Candidates[0].Assignment.ConceptID != "D030" ||
		got.Candidates[0].Assignment.Source != "src3" {
		t.Fatalf("new canonical name must resolve to D030 via src3, got %+v", got)
	}
	t.Logf("JUDGEMENT new name ' commoncold ' -> D030 via %s", candidatesSig(got.Candidates))

	// 回滚到改名前版本：术语结论整体恢复。
	if err := store.Rollback(ctx, v1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	got = r.ResolveTerm("COLD")
	if got == nil || got.Conflict != nil || len(got.Candidates) != 2 {
		t.Fatalf("term conclusions must be restored after rollback, got %+v", got)
	}
	t.Logf("JUDGEMENT after rollback 'COLD' resolves again: %s", candidatesSig(got.Candidates))
}

// TestTermName_DeleteAndRenameParity：删除与改名对同一术语结论的表现一致。
func TestTermName_DeleteAndRenameParity(t *testing.T) {
	ctx := context.Background()

	// 删除路径。
	storeDel, rDel, v1 := newImportedStore(t, "term_conflicts.json")
	if _, err := storeDel.ApplyRevision(ctx, kg.Revision{
		BaseVersion: v1, DeleteConcepts: []string{"D030"},
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	delGot := rDel.ResolveTerm("cold")

	// 改名路径。
	storeRen, rRen, v1b := newImportedStore(t, "term_conflicts.json")
	if _, err := storeRen.ApplyRevision(ctx, kg.Revision{
		BaseVersion:    v1b,
		RenameConcepts: []kg.Rename{{ID: "D030", NewName: "CommonCold", Source: "src3"}},
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	renGot := rRen.ResolveTerm("cold")

	t.Logf("JUDGEMENT after delete: 'cold' -> %+v; after rename: 'cold' -> %+v", delGot, renGot)
	if (delGot == nil) != (renGot == nil) {
		t.Fatalf("delete and rename must agree on term invalidation: delete=%+v rename=%+v",
			delGot, renGot)
	}
	if delGot != nil || renGot != nil {
		t.Fatal("both delete and rename must remove the old term conclusion")
	}
}

// TestTermName_DerivedCoexistsWithMappings：
// 规范名/同义词派生解析与既有标识映射同时存在、互不干扰。
func TestTermName_DerivedCoexistsWithMappings(t *testing.T) {
	store, r, v1 := newImportedStore(t, "ontology.json")
	ctx := context.Background()

	t.Log("INPUT ResolveMapping SNOMED:73211009 and ResolveTerm 'Diabetes' on the same snapshot")
	mviews, mconf := r.ResolveMapping("SNOMED:73211009")
	if mconf != nil || len(mviews) != 1 || mviews[0].Assignment.ConceptID != "D001" {
		t.Fatalf("mapping behaviour must be unchanged: views=%+v conflict=%+v", mviews, mconf)
	}
	tres := r.ResolveTerm("Diabetes")
	if tres == nil || tres.Conflict != nil || len(tres.Candidates) != 2 {
		t.Fatalf("derived name resolution must work alongside mappings: %+v", tres)
	}
	t.Logf("JUDGEMENT mapping -> D001 (no conflict); name 'Diabetes' -> D001 via %s",
		candidatesSig(tres.Candidates))

	// 给同一概念引入一个映射冲突：派生术语解析不受影响。
	if _, err := store.ApplyRevision(ctx, kg.Revision{
		BaseVersion: v1,
		UpsertMappings: []kg.MappingAssignment{
			{TargetID: "SNOMED:73211009", ConceptID: "D003", Source: "icd_demo"},
		},
	}); err != nil {
		t.Fatalf("revision: %v", err)
	}
	_, mconf = r.ResolveMapping("SNOMED:73211009")
	if mconf == nil || mconf.Type != kg.ConflictCrossSource {
		t.Fatalf("mapping should now be cross-source conflict, got %+v", mconf)
	}
	tres = r.ResolveTerm(" diabetes ")
	if tres == nil || tres.Conflict != nil || len(tres.Candidates) != 2 {
		t.Fatalf("derived name resolution must stay unaffected by mapping conflict, got %+v", tres)
	}
	t.Logf("JUDGEMENT mapping conflict=%s; name ' diabetes ' still clean via %s",
		mconf.Type, candidatesSig(tres.Candidates))
}

// TestTermName_DerivedResolutionDeterministic：同样的输入重复解析，结果与顺序完全一致。
func TestTermName_DerivedResolutionDeterministic(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	first := ""
	for i := 0; i < 5; i++ {
		got := r.ResolveTerm("  DIABETES   MELLITUS ")
		if got == nil {
			t.Fatal("derived synonym must resolve on every run")
		}
		var sb strings.Builder
		sb.WriteString(got.NormalizedQuery)
		for _, v := range got.Candidates {
			sb.WriteString("|")
			sb.WriteString(v.Assignment.Source)
			sb.WriteString("->")
			sb.WriteString(v.Assignment.ConceptID)
		}
		if i == 0 {
			first = sb.String()
		} else if sb.String() != first {
			t.Fatalf("non-deterministic derived resolution:\n%s\nvs\n%s", first, sb.String())
		}
	}
	t.Logf("JUDGEMENT stable across repeated runs: %s", first)
}
