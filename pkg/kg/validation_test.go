package kg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

// loadFile 从 testdata 读取样例并记录输入，保证单测日志可解释。
func loadFile(t *testing.T, name string) kg.Dataset {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	t.Logf("INPUT file=%s bytes=%d", name, len(raw))
	ds, err := kg.LoadDataset(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	t.Logf("INPUT concepts=%d hierarchy=%d relations=%d mappings=%d",
		len(ds.Concepts), len(ds.Hierarchy), len(ds.Relations), len(ds.Mappings))
	return ds
}

func rejectReasons(t *testing.T, err error) []kg.RejectReason {
	t.Helper()
	re, ok := err.(*kg.RejectError)
	if !ok {
		t.Fatalf("expected *RejectError, got %T: %v", err, err)
	}
	reasons := make([]kg.RejectReason, 0, len(re.Errors))
	for _, ve := range re.Errors {
		reasons = append(reasons, ve.Reason)
		t.Logf("JUDGEMENT reason=%s entities=[%s] message=%q",
			ve.Reason, strings.Join(ve.Entities, " -> "), ve.Message)
	}
	return reasons
}

func TestImport_ValidOntology(t *testing.T) {
	store := kg.NewStore()
	ds := loadFile(t, "ontology.json")
	res, err := store.Import(context.Background(), ds)
	if err != nil {
		t.Fatalf("valid ontology should import, got: %v", err)
	}
	t.Logf("JUDGEMENT import accepted version=%d conflicts=%d", res.Version, len(res.Conflicts))
	if res.Version != 1 {
		t.Fatalf("version = %d, want 1", res.Version)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("ontology.json should have no mapping conflicts, got %d", len(res.Conflicts))
	}
}

func TestImport_CycleRejected(t *testing.T) {
	store := kg.NewStore()
	ds := loadFile(t, "hierarchy_cycle.json")
	_, err := store.Import(context.Background(), ds)
	if err == nil {
		t.Fatal("cycle must be rejected, got nil error")
	}
	reasons := rejectReasons(t, err)
	found := false
	for _, r := range reasons {
		if r == kg.RejectCycle {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected cycle rejection, got %v", reasons)
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error should mention cycle: %v", err)
	}
	// 拒绝后当前版本必须保持为空版本 0（原子性）。
	if v := store.Current().Version(); v != 0 {
		t.Fatalf("store version after rejected import = %d, want 0", v)
	}
}

func TestImport_DanglingRejected(t *testing.T) {
	store := kg.NewStore()
	ds := loadFile(t, "dangling.json")
	_, err := store.Import(context.Background(), ds)
	if err == nil {
		t.Fatal("dangling references must be rejected, got nil error")
	}
	reasons := rejectReasons(t, err)
	for _, r := range reasons {
		if r != kg.RejectDangling {
			t.Fatalf("expected only dangling reasons, got %s", r)
		}
	}
	// 全部三个悬空 ID 都应被稳定列出。
	re := err.(*kg.RejectError)
	joined := ""
	for _, ve := range re.Errors {
		joined += strings.Join(ve.Entities, ",")
	}
	for _, want := range []string{"D777", "D888", "D999"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("dangling entities %q should contain %s", joined, want)
		}
	}
	t.Logf("JUDGEMENT all dangling ids reported: %s", joined)
}

func TestImport_DuplicateConflictingDefinitionRejected(t *testing.T) {
	store := kg.NewStore()
	ds := loadFile(t, "duplicate_conflict.json")
	_, err := store.Import(context.Background(), ds)
	if err == nil {
		t.Fatal("conflicting duplicate definitions must be rejected")
	}
	reasons := rejectReasons(t, err)
	if len(reasons) != 1 || reasons[0] != kg.RejectDuplicateConflict {
		t.Fatalf("want single duplicate_conflict, got %v", reasons)
	}
}

func TestImport_SameSourceDuplicateNameConflict(t *testing.T) {
	store := kg.NewStore()
	ds := kg.Dataset{
		Concepts: []kg.Concept{
			{ID: "X1", Name: "NameA", Source: "src1"},
			{ID: "X1", Name: "NameB", Source: "src1"},
		},
	}
	t.Logf("INPUT two concepts same id=X1 same source=src1 different names NameA/NameB")
	_, err := store.Import(context.Background(), ds)
	if err == nil {
		t.Fatal("same-source conflicting names must be rejected")
	}
	reasons := rejectReasons(t, err)
	if reasons[0] != kg.RejectDuplicateConflict {
		t.Fatalf("want duplicate_conflict, got %s", reasons[0])
	}
}

func TestImport_RejectionIsDeterministic(t *testing.T) {
	// 相同输入多次导入，拒绝原因的顺序与文本必须完全一致。
	ds := loadFile(t, "hierarchy_cycle.json")
	var first string
	for i := 0; i < 3; i++ {
		store := kg.NewStore()
		_, err := store.Import(context.Background(), ds)
		if err == nil {
			t.Fatal("cycle must be rejected")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("non-deterministic rejection:\n%s\nvs\n%s", first, err.Error())
		}
	}
	t.Logf("JUDGEMENT deterministic rejection: %s", first)
}
