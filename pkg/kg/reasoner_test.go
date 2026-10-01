package kg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func newImportedStore(t *testing.T, file string) (*kg.Store, *kg.Reasoner, int64) {
	t.Helper()
	store := kg.NewStore()
	r := kg.NewReasoner(store)
	ds := loadFile(t, file)
	res, err := store.Import(context.Background(), ds)
	if err != nil {
		t.Fatalf("import %s: %v", file, err)
	}
	return store, r, res.Version
}

func TestAncestors_TransitiveWithPath(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")

	paths, ok := r.Ancestors("D003")
	if !ok {
		t.Fatal("D003 should exist")
	}
	t.Logf("INPUT ancestors query id=D003 (D003 is-a D001 is-a D010)")
	got := map[string]string{}
	for _, p := range paths {
		got[p.ConceptID] = strings.Join(p.Path, " -> ")
		t.Logf("JUDGEMENT ancestor=%s path=%s", p.ConceptID, got[p.ConceptID])
	}
	if got["D001"] != "D003 -> D001" {
		t.Fatalf("D001 path wrong: %q", got["D001"])
	}
	if got["D010"] != "D003 -> D001 -> D010" {
		t.Fatalf("transitive ancestor D010 path wrong: %q", got["D010"])
	}
	if len(got) != 2 {
		t.Fatalf("D003 should have exactly 2 ancestors, got %d", len(got))
	}
}

func TestDescendants_Transitive(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	paths, ok := r.Descendants("D010")
	if !ok {
		t.Fatal("D010 should exist")
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p.ConceptID] = true
		t.Logf("JUDGEMENT descendant of D010: %s path=%s", p.ConceptID, strings.Join(p.Path, " -> "))
	}
	for _, want := range []string{"D001", "D003", "D002", "D020"} {
		if !got[want] {
			t.Fatalf("D010 descendants should include %s", want)
		}
	}
}

func TestConcept_SynonymsMergedAcrossSources(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	v, ok := r.GetConcept("D001")
	if !ok {
		t.Fatal("D001 missing")
	}
	t.Logf("INPUT concept D001 defined by snomed_demo + icd_demo")
	t.Logf("JUDGEMENT name=%q synonyms=%v", v.Concept.Name, v.Synonyms)
	wantSyns := map[string]bool{"DM": true, "Diabetes mellitus": true}
	for s := range wantSyns {
		found := false
		for _, got := range v.Synonyms {
			if got == s {
				found = true
			}
		}
		if !found {
			t.Fatalf("merged synonyms should contain %q, got %v", s, v.Synonyms)
		}
	}
	// 多来源证据必须可追溯。
	sources := map[string]bool{}
	for _, e := range v.Evidences {
		sources[e.Source] = true
	}
	if !sources["snomed_demo"] || !sources["icd_demo"] {
		t.Fatalf("concept evidence must cite both sources, got %v", sources)
	}
}

func TestSearch_ByNameAndSynonym(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	hits := r.SearchConcepts("T2DM") // 同义词命中
	if len(hits) != 1 || hits[0].Concept.ID != "D003" {
		t.Fatalf("synonym search T2DM should hit D003, got %+v", hits)
	}
	t.Logf("JUDGEMENT search 'T2DM' -> %s (%s)", hits[0].Concept.ID, hits[0].Concept.Name)

	hits = r.SearchConcepts("Diabetes") // 名称命中：D001
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.Concept.ID] = true
	}
	if !ids["D001"] {
		t.Fatalf("name search should hit D001, got %v", ids)
	}
}

func TestRelations_Filtered(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	rels := r.Relations("D020", "")
	if len(rels) != 1 || rels[0].Relation.Relation != "treats" || rels[0].Relation.Object != "D003" {
		t.Fatalf("D020 relations wrong: %+v", rels)
	}
	t.Logf("JUDGEMENT D020 --treats--> D003 evidence=%+v", rels[0].Evidences)

	if filtered := r.Relations("D020", "causes"); len(filtered) != 0 {
		t.Fatalf("relation filter mismatch: %+v", filtered)
	}
}

func TestExplain_AncestorEvidenceChain(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	ev := r.Explain("ancestor", "D003:D010")
	if len(ev) != 2 {
		t.Fatalf("expected 2 edge evidences for D003:D010, got %d (%+v)", len(ev), ev)
	}
	t.Log("INPUT explain ancestor chain D003:D010")
	for i, e := range ev {
		t.Logf("JUDGEMENT step %d: [%s] %s: %s", i+1, e.Kind, e.Source, e.Detail)
	}
	if ev[0].Detail != "D003 is-a D001" || ev[1].Detail != "D001 is-a D010" {
		t.Fatalf("evidence chain order/content wrong: %+v", ev)
	}
}

func TestQuery_UnknownConcept(t *testing.T) {
	_, r, _ := newImportedStore(t, "ontology.json")
	if _, ok := r.GetConcept("NOPE"); ok {
		t.Fatal("unknown concept should report not found")
	}
	if paths, ok := r.Ancestors("NOPE"); ok || paths != nil {
		t.Fatal("ancestors of unknown concept must be nil/false")
	}
	if views, c := r.ResolveMapping("EXT:MISSING"); views != nil || c != nil {
		t.Fatal("unknown mapping must resolve to nothing")
	}
}
