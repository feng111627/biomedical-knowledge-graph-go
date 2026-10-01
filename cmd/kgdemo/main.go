// Command kgdemo runs an end-to-end local demonstration of the biomedical
// knowledge-graph component using only local sample files (no network or real
// terminology services).
//
// It shows:
//  1. loading + validating a valid dataset (and rejecting cyclic / dangling /
//     duplicate-conflicting datasets with explicit reasons);
//  2. querying concepts, synonyms and entity relations;
//  3. transitive ancestor reasoning with per-edge provenance;
//  4. multi-source identifier / term normalization with conflict kinds;
//  5. incremental revision with atomic commit and full rollback;
//  6. stale-conclusion detection after delete / rename.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/graph"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/ingest"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/mapping"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/reason"
	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/revision"
)

func main() {
	dataDir := "testdata"
	if len(os.Args) > 1 {
		dataDir = os.Args[1]
	}

	fmt.Println("=== 1. Import validation (rejections) ===")
	for _, p := range []string{"cycle_dataset.json", "dangling_dataset.json", "duplicate_conflict_dataset.json"} {
		_, err := ingest.LoadFile(dataDir + "/" + p)
		var rej *model.RejectError
		if errors.As(err, &rej) {
			fmt.Printf("INPUT %-32s REJECTED kind=%-20s reason=%s\n", p, rej.Kind, rej.Message)
		} else if err != nil {
			fmt.Printf("INPUT %-32s ERROR %v\n", p, err)
		}
	}

	fmt.Println("\n=== 2. Load valid dataset ===")
	ds, err := ingest.LoadFile(dataDir + "/valid_dataset.json")
	must(err)
	g := graph.New()
	snap := g.LoadDataset(ds)
	fmt.Printf("loaded %q revision=%d concepts=%d synonyms=%d parents=%d relations=%d mappings=%d\n",
		ds.Name, snap.Revision, len(snap.Concepts), len(snap.Synonyms),
		len(snap.Parents), len(snap.Relations), len(snap.Mappings))

	fmt.Println("\n=== 3. Queries ===")
	fmt.Printf("concept FLU  = %s\n", snap.Concepts["FLU"].Label)
	fmt.Printf("children of DISEASE = %v\n", snap.ChildrenOf("DISEASE"))
	fmt.Printf("synonyms of COLD    = ")
	for _, s := range snap.SynonymsOf("COLD") {
		fmt.Printf("%q@%s ", s.Term, s.Source)
	}
	fmt.Println()
	fmt.Printf("relations on subject OSELTAMIVIR = ")
	for _, r := range snap.RelationsBySubject("OSELTAMIVIR") {
		fmt.Printf("(%s %s %s@%s) ", r.SubjectID, r.Predicate, r.ObjectID, r.Source)
	}
	fmt.Println()

	fmt.Println("\n=== 4. Transitive hierarchy reasoning (with provenance) ===")
	c := reason.AncestorsOf(snap, "COLD")
	out, _ := json.MarshalIndent(c, "", "  ")
	fmt.Println(string(out))

	fmt.Println("\n=== 5. Multi-source normalization & conflicts ===")
	resolver := mapping.New(snap.Mappings, ds.Concepts, ds.Synonyms)
	for _, q := range []string{"UMLS:COLD", "MSH:D003120", "LOC:42", "X:7788"} {
		r := resolver.ResolveID(q)
		if r.IsConflicted() {
			fmt.Printf("INPUT id=%-12s CONFLICT kind=%-30s reason=%s\n", q, r.Conflict.Kind, r.Conflict.Reason)
		} else {
			fmt.Printf("INPUT id=%-12s -> concept=%s evidence=%d\n", q, r.ConceptID, len(r.Provenance))
		}
	}
	for _, q := range []string{"tamiflu", "  Cold  "} {
		r := resolver.ResolveTerm(q)
		if r.IsConflicted() {
			fmt.Printf("INPUT term=%-12q CONFLICT kind=%-30s reason=%s\n", q, r.Conflict.Kind, r.Conflict.Reason)
		} else {
			fmt.Printf("INPUT term=%-12q -> concept=%s evidence=%d\n", q, r.ConceptID, len(r.Provenance))
		}
	}

	fmt.Println("\n=== 6. Incremental revision + rollback ===")
	mgr := revision.NewManager(g)

	// 6a. A valid incremental change commits atomically.
	tx := mgr.Begin()
	must(tx.AddConcept(model.Concept{ID: "PNEUMONIA", Label: "pneumonia", Source: "local-ontology"}))
	must(tx.AddParent(model.ParentLink{ConceptID: "PNEUMONIA", ParentID: "RESPDIS", Source: "local-ontology"}))
	must(tx.AddParent(model.ParentLink{ConceptID: "PNEUMONIA", ParentID: "INFECT", Source: "local-ontology"}))
	newSnap, err := tx.Commit()
	must(err)
	fmt.Printf("committed revision=%d: pneumonia ancestors=%v\n",
		newSnap.Revision, reason.AncestorsOf(newSnap, "PNEUMONIA").Ancestors)

	// 6b. An invalid incremental change is rejected; published graph unchanged.
	bad := mgr.Begin()
	must(bad.AddConcept(model.Concept{ID: "X1", Label: "x one", Source: "s"}))
	must(bad.AddParent(model.ParentLink{ConceptID: "DISEASE", ParentID: "X1", Source: "s"}))
	must(bad.AddParent(model.ParentLink{ConceptID: "X1", ParentID: "DISEASE", Source: "s"}))
	if _, err := bad.Commit(); err != nil {
		var rej *model.RejectError
		if errors.As(err, &rej) {
			fmt.Printf("invalid revision REJECTED kind=%s reason=%s\n", rej.Kind, rej.Message)
		}
	}
	bad.Rollback()
	fmt.Printf("after rejected+rollback, still revision=%d\n", g.Current().Revision)

	// 6c. Explicit whole-transaction rollback: draft edits never appear.
	rb := mgr.Begin()
	must(rb.DeleteConcept("FLU"))
	fmt.Printf("draft notes before rollback: %v\n", rb.Notes())
	rb.Rollback()
	if _, ok := g.Current().Concepts["FLU"]; !ok {
		panic("FLU disappeared despite rollback")
	}
	fmt.Printf("after rollback FLU still present, revision=%d\n", g.Current().Revision)

	fmt.Println("\n=== 7. Stale conclusion detection ===")
	before := reason.AncestorsOf(g.Current(), "COLD")
	fmt.Printf("derived at revision=%d ancestors=%v\n", before.Revision, before.Ancestors)

	del := mgr.Begin()
	must(del.DeleteConcept("INFECT"))
	del.Rollback() // demonstrate rollback keeps it valid first

	stale, why := reason.IsStale(g.Current(), before)
	fmt.Printf("after unrelated rollback: stale=%v (revision still %d)\n", stale, g.Current().Revision)

	ren := mgr.Begin()
	must(ren.RenameConcept("COLD", "common cold syndrome"))
	_, err = ren.Commit()
	if err != nil {
		fmt.Printf("rename commit failed: %v\n", err)
	}
	stale, why = reason.IsStale(g.Current(), before)
	fmt.Printf("after rename: stale=%v reason=%s\n", stale, why)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
