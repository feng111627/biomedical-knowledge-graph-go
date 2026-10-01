package ingest

import (
	"path/filepath"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

// TestRejectKinds verifies that the three structural failure modes are kept
// distinct and reported with an explainable reason. Inputs and the exact
// verdict basis are logged, per project test requirements.
func TestRejectKinds(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		wantKind model.RejectKind
	}{
		{"hierarchy cycle", "cycle_dataset.json", model.RejectHierarchyCycle},
		{"dangling references", "dangling_dataset.json", model.RejectDanglingReference},
		{"duplicate conflicting definition", "duplicate_conflict_dataset.json", model.RejectDuplicateConflict},
	}
	dir := filepath.Join("..", "..", "testdata")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			t.Logf("INPUT file=%s", path)
			_, err := LoadFile(path)
			if err == nil {
				t.Fatalf("expected rejection of kind %s, got nil", tc.wantKind)
			}
			rej, ok := err.(*model.RejectError)
			if !ok {
				t.Fatalf("expected *model.RejectError, got %T: %v", err, err)
			}
			t.Logf("VERDICT kind=%s reason=%q paths=%v", rej.Kind, rej.Message, rej.Paths)
			if rej.Kind != tc.wantKind {
				t.Fatalf("kind = %s, want %s", rej.Kind, tc.wantKind)
			}
			if rej.Message == "" {
				t.Fatal("rejection must carry an explainable message")
			}
		})
	}
}

func TestCycleSelfLoop(t *testing.T) {
	d := &model.Dataset{
		Concepts: []model.Concept{{ID: "A", Label: "a", Source: "s"}},
		Parents:  []model.ParentLink{{ConceptID: "A", ParentID: "A", Source: "s"}},
	}
	t.Logf("INPUT self-loop A->A on %d concept(s)", len(d.Concepts))
	err := Validate(d)
	rej := mustReject(t, err)
	t.Logf("VERDICT kind=%s reason=%q", rej.Kind, rej.Message)
	if rej.Kind != model.RejectHierarchyCycle {
		t.Fatalf("kind = %s, want hierarchy_cycle", rej.Kind)
	}
}

func TestDanglingBothEnds(t *testing.T) {
	d := &model.Dataset{
		Concepts: []model.Concept{{ID: "A", Label: "a", Source: "s"}},
		Parents:  []model.ParentLink{{ConceptID: "A", ParentID: "P", Source: "s"}},
	}
	t.Logf("INPUT parent link A->P where P undefined")
	err := Validate(d)
	rej := mustReject(t, err)
	t.Logf("VERDICT kind=%s reason=%q", rej.Kind, rej.Message)
	if rej.Kind != model.RejectDanglingReference {
		t.Fatalf("kind = %s, want dangling_reference", rej.Kind)
	}
}

func TestDuplicateConceptConflict(t *testing.T) {
	d := &model.Dataset{Concepts: []model.Concept{
		{ID: "A", Label: "alpha", Source: "s1"},
		{ID: "A", Label: "beta", Source: "s2"},
	}}
	t.Logf("INPUT two concepts with id A but labels alpha vs beta")
	err := Validate(d)
	rej := mustReject(t, err)
	t.Logf("VERDICT kind=%s reason=%q", rej.Kind, rej.Message)
	if rej.Kind != model.RejectDuplicateConflict {
		t.Fatalf("kind = %s, want duplicate_conflict", rej.Kind)
	}
}

// TestIdempotentDuplicateAccept: the same definition repeated is allowed
// (deterministic re-import must not reject identical re-assertions).
func TestIdempotentDuplicateAccept(t *testing.T) {
	d := &model.Dataset{Concepts: []model.Concept{
		{ID: "A", Label: "alpha", Source: "s1"},
		{ID: "A", Label: "alpha", Source: "s2"},
	}}
	t.Logf("INPUT two identical definitions of A (label alpha)")
	if err := Validate(d); err != nil {
		t.Fatalf("identical re-assertion should be accepted, got %v", err)
	}
	t.Log("VERDICT accepted: identical re-assertion is idempotent")
}

func TestValidFileLoads(t *testing.T) {
	ds, err := LoadFile(filepath.Join("..", "..", "testdata", "valid_dataset.json"))
	if err != nil {
		t.Fatalf("valid dataset rejected: %v", err)
	}
	t.Logf("INPUT valid dataset: concepts=%d parents=%d mappings=%d",
		len(ds.Concepts), len(ds.Parents), len(ds.Mappings))
}

// TestValidationDeterministic runs validation many times and ensures a cycle
// verdict always reports the same path (map iteration must not change output).
func TestValidationDeterministic(t *testing.T) {
	for i := 0; i < 50; i++ {
		_, err := LoadFile(filepath.Join("..", "..", "testdata", "cycle_dataset.json"))
		rej := mustReject(t, err)
		if len(rej.Paths) != 1 || rej.Paths[0][0] != "A" {
			t.Fatalf("non-deterministic cycle evidence: %v", rej.Paths)
		}
	}
}

func mustReject(t *testing.T, err error) *model.RejectError {
	t.Helper()
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	rej, ok := err.(*model.RejectError)
	if !ok {
		t.Fatalf("expected *model.RejectError, got %T: %v", err, err)
	}
	return rej
}
