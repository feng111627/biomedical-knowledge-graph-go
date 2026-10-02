package kg

import "strings"

// validateData 对候选快照数据做跨记录一致性校验：
// 悬空引用、层级环。重复且冲突的概念定义在构建阶段检测。
// 返回的错误按原因与实体排序，保证相同输入结论一致。
func validateData(d *snapshotData) []*ValidationError {
	errs := make([]*ValidationError, 0)
	errs = append(errs, detectDangling(d)...)
	errs = append(errs, detectCycles(d)...)
	return errs
}

// detectDangling 查找引用了未定义概念的层级边、关系与映射。
func detectDangling(d *snapshotData) []*ValidationError {
	missing := map[string]bool{}
	note := map[string]string{}

	add := func(id, where string) {
		if _, ok := d.concepts[id]; ok || id == "" {
			return
		}
		missing[id] = true
		if note[id] == "" {
			note[id] = where
		}
	}

	for child, parents := range d.parentsOf {
		add(child, "hierarchy child")
		for p := range parents {
			add(p, "hierarchy parent")
		}
	}
	for subject, rels := range d.relationsBySubject {
		add(subject, "relation subject")
		for _, r := range rels {
			add(r.Object, "relation object")
		}
	}
	for _, assignments := range d.mappingsByTarget {
		for _, m := range assignments {
			add(m.ConceptID, "mapping concept_id")
		}
	}
	for _, assignments := range d.termsByKey {
		for _, t := range assignments {
			add(t.ConceptID, "term concept_id")
		}
	}

	if len(missing) == 0 {
		return nil
	}
	ids := make([]string, 0, len(missing))
	for id := range missing {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return []*ValidationError{{
		Reason:   RejectDangling,
		Message:  "references undefined concept (" + note[ids[0]] + "); define it or remove the reference",
		Entities: ids,
	}}
}

// detectCycles 在 child -> parent 的有向图上做 DFS，报告每个环一次。
func detectCycles(d *snapshotData) []*ValidationError {
	nodes := map[string]bool{}
	for id := range d.concepts {
		nodes[id] = true
	}
	for child, parents := range d.parentsOf {
		nodes[child] = true
		for p := range parents {
			nodes[p] = true
		}
	}
	ordered := make([]string, 0, len(nodes))
	for n := range nodes {
		ordered = append(ordered, n)
	}
	sortStrings(ordered)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	stack := []string{}
	seenCycle := map[string]bool{}
	cycles := [][]string{}

	var dfs func(n string)
	dfs = func(n string) {
		color[n] = gray
		stack = append(stack, n)

		parents := make([]string, 0, len(d.parentsOf[n]))
		for p := range d.parentsOf[n] {
			parents = append(parents, p)
		}
		sortStrings(parents)

		for _, p := range parents {
			switch color[p] {
			case white:
				dfs(p)
			case gray:
				idx := -1
				for i, s := range stack {
					if s == p {
						idx = i
						break
					}
				}
				loop := append([]string{}, stack[idx:]...)
				loop = append(loop, p)
				key := strings.Join(canonicalCycle(loop), "|")
				if !seenCycle[key] {
					seenCycle[key] = true
					cycles = append(cycles, loop)
				}
			}
		}

		stack = stack[:len(stack)-1]
		color[n] = black
	}

	for _, n := range ordered {
		if color[n] == white {
			dfs(n)
		}
	}

	errs := make([]*ValidationError, 0, len(cycles))
	for _, loop := range cycles {
		errs = append(errs, &ValidationError{
			Reason:   RejectCycle,
			Message:  "is-a hierarchy contains a cycle; remove one edge to restore a DAG",
			Entities: loop,
		})
	}
	return errs
}

// canonicalCycle 把环旋转到最小元素开头，用于环去重，输出稳定。
func canonicalCycle(loop []string) []string {
	if len(loop) <= 1 {
		return loop
	}
	ring := loop[:len(loop)-1] // 去掉重复的首尾
	minIdx := 0
	for i := 1; i < len(ring); i++ {
		if ring[i] < ring[minIdx] {
			minIdx = i
		}
	}
	out := make([]string, 0, len(ring)+1)
	out = append(out, ring[minIdx:]...)
	out = append(out, ring[:minIdx]...)
	return append(out, out[0])
}
