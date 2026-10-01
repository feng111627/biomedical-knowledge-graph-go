package mapping

import (
	"reflect"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

func TestUnambiguousResolutionWithProvenance(t *testing.T) {
	entries := []model.Mapping{
		{Source: "src-a", ExternalID: "E:1", ConceptID: "C1"},
	}
	t.Logf("INPUT single mapping E:1 -> C1 from src-a")
	r := New(entries, nil, nil)
	got := r.ResolveID("E:1")
	if got.IsConflicted() || got.ConceptID != "C1" {
		t.Fatalf("unexpected verdict: %+v", got)
	}
	if len(got.Provenance) != 1 || got.Provenance[0].Source != "src-a" {
		t.Fatalf("provenance must trace to source src-a, got %+v", got.Provenance)
	}
	t.Logf("VERDICT concept=%s provenance=%+v", got.ConceptID, got.Provenance)
}

func TestCrossSourceMutuallyExclusive(t *testing.T) {
	entries := []model.Mapping{
		{Source: "src-a", ExternalID: "E:2", ConceptID: "C1"},
		{Source: "src-b", ExternalID: "E:2", ConceptID: "C2"},
	}
	t.Logf("INPUT E:2 -> C1 (src-a) and E:2 -> C2 (src-b)")
	r := New(entries, nil, nil)
	got := r.ResolveID("E:2")
	if !got.IsConflicted() {
		t.Fatal("expected conflict")
	}
	t.Logf("VERDICT kind=%s reason=%q entries=%d", got.Conflict.Kind, got.Conflict.Reason, len(got.Conflict.Entries))
	if got.Conflict.Kind != model.ConflictMutuallyExclusiveTarget {
		t.Fatalf("kind = %s, want mutually_exclusive_target", got.Conflict.Kind)
	}
	if got.ConceptID != "" {
		t.Fatalf("conflicted resolution must not silently pick a concept, got %q", got.ConceptID)
	}
	if len(got.Conflict.Entries) != 2 {
		t.Fatal("conflict must retain both evidence entries")
	}
}

func TestSameSourceConflictingTargets(t *testing.T) {
	entries := []model.Mapping{
		{Source: "src-a", ExternalID: "E:3", ConceptID: "C1"},
		{Source: "src-a", ExternalID: "E:3", ConceptID: "C2"},
	}
	t.Logf("INPUT same source src-a maps E:3 to both C1 and C2")
	r := New(entries, nil, nil)
	got := r.ResolveID("E:3")
	if !got.IsConflicted() {
		t.Fatal("expected conflict")
	}
	t.Logf("VERDICT kind=%s reason=%q", got.Conflict.Kind, got.Conflict.Reason)
	if got.Conflict.Kind != model.ConflictSameSourceTargets {
		t.Fatalf("kind = %s, want same_source_conflicting_targets", got.Conflict.Kind)
	}
}

// TestConflictKindsDistinct: within one table, both kinds must coexist and be
// distinguishable.
func TestConflictKindsDistinct(t *testing.T) {
	entries := []model.Mapping{
		{Source: "src-a", ExternalID: "SAME", ConceptID: "C1"},
		{Source: "src-a", ExternalID: "SAME", ConceptID: "C2"},
		{Source: "src-a", ExternalID: "CROSS", ConceptID: "C1"},
		{Source: "src-b", ExternalID: "CROSS", ConceptID: "C2"},
	}
	r := New(entries, nil, nil)
	kinds := map[model.ConflictKind]bool{}
	for _, c := range r.Conflicts {
		kinds[c.Kind] = true
		t.Logf("INPUT conflict external=%s VERDICT kind=%s reason=%q", c.External, c.Kind, c.Reason)
	}
	if !kinds[model.ConflictSameSourceTargets] || !kinds[model.ConflictMutuallyExclusiveTarget] {
		t.Fatalf("expected both conflict kinds present, got %v", kinds)
	}
}

func TestTermNormalizationConflict(t *testing.T) {
	concepts := []model.Concept{
		{ID: "COLD", Label: "common cold", Source: "s1"},
		{ID: "CHILLS", Label: "chills", Source: "s2"},
	}
	syns := []model.Synonym{
		{ConceptID: "COLD", Term: "cold", Source: "s1"},
		{ConceptID: "CHILLS", Term: "cold", Source: "s2"},
	}
	t.Logf("INPUT term 'cold' asserted for COLD@s1 and CHILLS@s2")
	r := New(nil, concepts, syns)
	got := r.ResolveTerm("  COLD ")
	t.Logf("VERDICT query='  COLD ' kind=%v reason=%q", got.Conflict, conflictReason(got))
	if !got.IsConflicted() || got.Conflict.Kind != model.ConflictMutuallyExclusiveTarget {
		t.Fatalf("expected cross-source term conflict, got %+v", got.Conflict)
	}
}

// TestDeterministicRepeatedBuilds builds the table repeatedly and asserts the
// serialized conflict ordering is stable.
func TestDeterministicRepeatedBuilds(t *testing.T) {
	entries := []model.Mapping{
		{Source: "z", ExternalID: "Z:9", ConceptID: "C1"},
		{Source: "a", ExternalID: "Z:9", ConceptID: "C2"},
		{Source: "a", ExternalID: "A:1", ConceptID: "C3"},
		{Source: "b", ExternalID: "A:1", ConceptID: "C4"},
	}
	var prev []model.ConflictKind
	for i := 0; i < 20; i++ {
		r := New(entries, nil, nil)
		var ks []model.ConflictKind
		var externals []string
		for _, c := range r.Conflicts {
			ks = append(ks, c.Kind)
			externals = append(externals, c.External)
		}
		if i == 0 {
			prev = append([]model.ConflictKind(nil), ks...)
			t.Logf("first run conflict external order: %v kinds=%v", externals, ks)
			continue
		}
		if !reflect.DeepEqual(ks, prev) {
			t.Fatalf("non-deterministic conflict order: %v vs %v", ks, prev)
		}
	}
}

func TestNormalizeTerm(t *testing.T) {
	cases := map[string]string{
		"  Tamiflu  ":    "tamiflu",
		"Seasonal   Flu": "seasonal flu",
		"ASPIRIN":        "aspirin",
	}
	for in, want := range cases {
		if got := NormalizeTerm(in); got != want {
			t.Fatalf("NormalizeTerm(%q) = %q, want %q", in, got, want)
		}
	}
}

func conflictReason(r Resolution) string {
	if r.Conflict == nil {
		return ""
	}
	return r.Conflict.Reason
}
