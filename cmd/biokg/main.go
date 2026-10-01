// Command biokg 是一个纯本地的生物医学知识图谱/本体推理演示 CLI。
// 不依赖任何外部服务或真实术语库，数据来自本地 JSON 样例文件。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/kg"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	store := kg.NewStore()
	r := kg.NewReasoner(store)

	switch os.Args[1] {
	case "import":
		fs := flag.NewFlagSet("import", flag.ExitOnError)
		file := fs.String("file", "", "dataset JSON file")
		_ = fs.Parse(os.Args[2:])
		if *file == "" {
			fatal("import requires -file")
		}
		ds := mustLoad(*file)
		res, err := store.Import(ctx, ds)
		if err != nil {
			printReject(err)
			os.Exit(1)
		}
		fmt.Printf("IMPORTED version=%d conflicts=%d\n", res.Version, len(res.Conflicts))
		printConflicts(res.Conflicts)

	case "query":
		fs := flag.NewFlagSet("query", flag.ExitOnError)
		file := fs.String("file", "", "dataset JSON file")
		kind := fs.String("kind", "concept", "concept|search|children|parents|ancestors|descendants|relations|mapping|explain|stale")
		id := fs.String("id", "", "concept id / text / target_id / explain composite id")
		rel := fs.String("rel", "", "relation type filter")
		_ = fs.Parse(os.Args[2:])
		if *file == "" {
			fatal("query requires -file")
		}
		ds := mustLoad(*file)
		if _, err := store.Import(ctx, ds); err != nil {
			printReject(err)
			os.Exit(1)
		}
		runQuery(r, *kind, *id, *rel)

	case "demo":
		runDemo(ctx, store, r)

	default:
		usage()
		os.Exit(2)
	}
}

func runQuery(r *kg.Reasoner, kind, id, rel string) {
	switch kind {
	case "concept":
		v, ok := r.GetConcept(id)
		if !ok {
			fmt.Printf("NOT FOUND %s\n", id)
			return
		}
		printConcept(v)
	case "search":
		for _, v := range r.SearchConcepts(id) {
			printConcept(v)
		}
	case "children":
		for _, v := range r.Children(id) {
			printConcept(v)
		}
	case "parents":
		for _, v := range r.Parents(id) {
			printConcept(v)
		}
	case "ancestors":
		paths, ok := r.Ancestors(id)
		if !ok {
			fmt.Printf("NOT FOUND %s\n", id)
			return
		}
		for _, p := range paths {
			fmt.Printf("ANCESTOR %s path=%s\n", p.ConceptID, strings.Join(p.Path, " -> "))
			for _, e := range r.Explain("ancestor", id+":"+p.ConceptID) {
				fmt.Printf("  EVIDENCE [%s] %s: %s\n", e.Kind, e.Source, e.Detail)
			}
		}
	case "descendants":
		paths, ok := r.Descendants(id)
		if !ok {
			fmt.Printf("NOT FOUND %s\n", id)
			return
		}
		for _, p := range paths {
			fmt.Printf("DESCENDANT %s path=%s\n", p.ConceptID, strings.Join(p.Path, " -> "))
		}
	case "relations":
		for _, v := range r.Relations(id, rel) {
			fmt.Printf("RELATION %s %s %s (source=%s)\n", v.Relation.Subject, v.Relation.Relation, v.Relation.Object, v.Relation.Source)
		}
	case "mapping":
		views, conflict := r.ResolveMapping(id)
		if views == nil {
			fmt.Printf("NO MAPPING for %s\n", id)
			return
		}
		for _, v := range views {
			mark := "OK"
			if v.Conflicted {
				mark = "CONFLICTED"
			}
			fmt.Printf("MAPPING %s -> %s source=%s [%s]\n", id, v.Assignment.ConceptID, v.Assignment.Source, mark)
		}
		if conflict != nil {
			fmt.Printf("CONFLICT type=%s source=%s target=%s\n", conflict.Type, conflict.Source, conflict.TargetID)
		}
		fmt.Println("EVIDENCE:")
		for _, e := range r.Explain("mapping", id) {
			fmt.Printf("  [%s] %s: %s\n", e.Kind, e.Source, e.Detail)
		}
	case "explain":
		// id 形如 ancestor:D001:D010 或 mapping:TARGET
		parts := strings.SplitN(id, ":", 2)
		for _, e := range r.Explain(parts[0], parts[1]) {
			fmt.Printf("  [%s] %s: %s\n", e.Kind, e.Source, e.Detail)
		}
	case "stale":
		for _, s := range r.StaleConclusions() {
			fmt.Printf("STALE kind=%s subject=%s at=%d reason=%s detail=%s\n", s.Kind, s.SubjectID, s.InvalidatedAt, s.Reason, s.Detail)
		}
	default:
		fatal("unknown query kind: " + kind)
	}
}

func printConcept(v kg.ConceptView) {
	fmt.Printf("CONCEPT %s name=%q sources=%s synonyms=%s\n",
		v.Concept.ID, v.Concept.Name, joinEvSources(v.Evidences), strings.Join(v.Synonyms, ","))
	for _, e := range v.Evidences {
		fmt.Printf("  EVIDENCE [%s] %s: %s\n", e.Kind, e.Source, e.Detail)
	}
}

func joinEvSources(es []kg.Evidence) string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range es {
		if !seen[e.Source] {
			seen[e.Source] = true
			out = append(out, e.Source)
		}
	}
	return strings.Join(out, ",")
}

func printConflicts(cs []kg.MappingConflict) {
	for _, c := range cs {
		targets := make([]string, len(c.Targets))
		for i, t := range c.Targets {
			targets[i] = t.Source + "->" + t.ConceptID
		}
		fmt.Printf("CONFLICT type=%s source=%s target=%s targets=[%s]\n",
			c.Type, c.Source, c.TargetID, strings.Join(targets, ", "))
	}
}

func printReject(err error) {
	if re, ok := err.(*kg.RejectError); ok {
		for _, ve := range re.Errors {
			fmt.Fprintf(os.Stderr, "REJECTED reason=%s entities=%s msg=%s\n",
				ve.Reason, strings.Join(ve.Entities, " -> "), ve.Message)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
}

func mustLoad(path string) kg.Dataset {
	raw, err := os.ReadFile(path)
	if err != nil {
		fatal(err.Error())
	}
	ds, err := kg.LoadDataset(raw)
	if err != nil {
		fatal(err.Error())
	}
	return ds
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `biokg - local biomedical knowledge graph demo

usage:
  biokg import -file testdata/ontology.json
  biokg query  -file testdata/ontology.json -kind ancestors -id D001
  biokg query  -file testdata/ontology.json -kind mapping   -id SNOMED:73211009
  biokg demo`)
}

// runDemo 打印一段 JSON 结果，展示导入/推导/冲突/修订/回滚的完整流程。
func runDemo(ctx context.Context, store *kg.Store, r *kg.Reasoner) {
	out := map[string]any{}

	ds, err := kg.LoadDataset([]byte(demoDataset))
	if err != nil {
		fatal(err.Error())
	}
	res, err := store.Import(ctx, ds)
	if err != nil {
		fatal(err.Error())
	}
	out["import_version"] = res.Version
	out["import_conflicts"] = res.Conflicts
	anc, _ := r.Ancestors("D003")
	out["ancestors_of_D003"] = anc

	rev := kg.Revision{
		BaseVersion:    res.Version,
		DeleteConcepts: []string{"D003"},
	}
	rres, err := store.ApplyRevision(ctx, rev)
	out["revision_version"] = rres.Version
	out["stale_after_delete"] = r.StaleConclusions()

	_ = store.Rollback(ctx, res.Version)
	out["versions_after_rollback"] = store.Versions()
	out["ancestors_after_rollback"] = func() []kg.AncestorPath {
		a, _ := r.Ancestors("D003")
		return a
	}()
	if err != nil {
		out["error"] = err.Error()
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

const demoDataset = `{
  "concepts": [
    {"id":"D010","name":"Disease","source":"snomed_demo","synonyms":["DIS"]},
    {"id":"D001","name":"Diabetes","source":"snomed_demo","synonyms":["DM"]},
    {"id":"D003","name":"Type2Diabetes","source":"icd_demo","synonyms":["T2DM"]},
    {"id":"D020","name":"Metformin","source":"snomed_demo","synonyms":[]}
  ],
  "hierarchy": [
    {"parent":"D010","child":"D001","source":"snomed_demo"},
    {"parent":"D001","child":"D003","source":"icd_demo"}
  ],
  "relations": [
    {"subject":"D020","relation":"treats","object":"D003","source":"snomed_demo"}
  ],
  "mappings": [
    {"target_id":"SNOMED:73211009","concept_id":"D001","source":"snomed_demo"},
    {"target_id":"SNOMED:73211009","concept_id":"D003","source":"icd_demo"}
  ]
}`
