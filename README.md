# biomedical-knowledge-graph-go（本地演示）

一套**纯本地、可复现**的生物医学知识图谱与本体推理组件：概念 / 同义词 / 上下位关系 /
实体关系的导入与查询、层级传递推导、多来源标识归一与冲突标记、**原始文本术语归一解析
（大小写/空白不敏感；概念规范名与同义词无需登记即可解析）与术语冲突标记**、增量修订、
整体回滚，以及"环 / 悬空引用 / 重复冲突定义"的一致性拒绝。

- 无任何外部服务依赖、无网络访问、不连接真实术语库；所有数据来自本地 JSON 样例。
- 相同输入永远产生相同结论（集合遍历、冲突报告、推导路径均做了确定性排序）。
- 每条结论都带 `Evidence`（来源 + 类型 + 细节），可用 `Explain` 追溯。

## 目录结构

```
pkg/kg/            核心库
  types.go         领域类型（Concept / HierarchyEdge / EntityRelation / Mapping ...）
  errors.go        拒绝原因 RejectReason、冲突类型 ConflictType 与错误格式化
  loader.go        本地 JSON 数据集解析
  snapshot.go      不可变版本快照与深拷贝
  builder.go       候选快照构建：概念合并、重复冲突、映射冲突计算、级联删除
  validate.go      一致性校验：悬空引用、层级环（DFS）
  store.go         版本化存储：原子提交、乐观并发、整体回滚（RWMutex）
  reasoner.go      查询：直接/传递上下位、关系、映射/术语归一、证据链、失效结论
  term.go          术语归一规则（NormalizeTerm）、术语归属索引、术语冲突与解析
cmd/biokg/         本地命令行验证工具
testdata/          样例数据（合法、环、悬空、重复冲突、映射冲突、术语冲突）
```

## 数据格式（testdata/ontology.json）

顶层一个 JSON 对象，全部记录都必须带 `source`（来源标识）：

```json
{
  "concepts": [
    {"id": "D001", "name": "Diabetes", "source": "snomed_demo",
     "synonyms": ["DM", "Diabetes mellitus"]}
  ],
  "hierarchy": [
    {"parent": "D010", "child": "D001", "source": "snomed_demo"}
  ],
  "relations": [
    {"subject": "D020", "relation": "treats", "object": "D003",
     "source": "snomed_demo"}
  ],
  "mappings": [
    {"target_id": "SNOMED:73211009", "concept_id": "D001",
     "source": "snomed_demo"}
  ],
  "terms": [
    {"term": "Diabetes", "concept_id": "D001", "source": "snomed_demo"}
  ]
}
```

- `concepts`：内部概念。`id` 内部稳定标识；`name` 规范名；`synonyms` 同义词。
- `hierarchy`：`child is-a parent` 的有向边，必须构成 DAG。
- `relations`：非层级实体关系（subject–relation–object）。
- `mappings`：外部术语标识 `target_id` → 内部概念 `concept_id` 的来源归属（精确匹配）。
- `terms`：原始文本术语 `term` → 内部概念 `concept_id` 的来源归属，
  匹配前先做术语归一（见下），用于解析从论文/检验单/记录里抄来的不规范写法。

解析器只做结构校验（必填字段）；跨记录的语义一致性由导入器负责。

## 推理规则

1. **直接上下位**：`Children(id)` / `Parents(id)` 来自层级边。
2. **传递推导**：`Ancestors` 沿 child→parent、`Descendants` 沿 parent→children 做
   确定性 BFS；每个祖先/后代都返回一条推导路径（最短路径；多条等长时取字典序最小），
   路径上的每条边都是一条可追溯证据。
3. **概念合并**：同一 `id` 可由多个来源定义；规范名一致时合并，同义词取并集，
   `Evidence` 同时保留全部来源。新名称若已是该概念的同义词也允许合并。
4. **实体关系查询**：按主体或客体检索，可按关系类型过滤。
5. **标识归一**：`ResolveMapping(target_id)` 返回该外部标识的全部来源归属与概念。
6. **术语归一**：`ResolveTerm(raw)` 把原始文本经 `NormalizeTerm` 归一后，先与登记术语的
   归一化键精确匹配；无登记命中时回退到概念当前规范名/同义词的派生键。返回归一前后
   对应（`Query` / `NormalizedQuery` / `MatchedTerm`）、命中的全部概念候选与来源依据。

### 哪些写法能解析到（两层匹配，键都是归一化后的文本）

1. **显式登记的术语**（`terms` 通道）：存在命中时优先使用，行为与既往完全一致。
2. **概念自身定义**（派生层，无需重复登记）：仅当没有任何登记术语命中时回退到此层——
   每个概念的**当前规范名**与**全部同义词**都能直接解析到该概念，大小写、前后或中间
   空白不同的写法命中同一归一键，结果与拿规范写法查**逐字节一致**（候选、顺序、证据
   完全相同，仅 `Query` 回显原始输入）。
   - 来源依据给全：规范名取所有以该名定义概念的来源，同义词取登记该同义词的来源；
     `Explain("term", ...)` 中派生证据以 `concept_name` / `synonym` 类型标注，
     与登记术语的 `term` 类型区分。
   - 派生层同样按统一规则判定冲突（如两个概念共享同一同义词），候选全部保留。
   - 历史曾用名不参与解析：概念改名后，旧名不再是规范写法，其派生结论随改名失效。

### 术语归一规则（确定性、无 locale/外部依赖）

`NormalizeTerm(s)` 依次执行：

1. 用 `strings.Fields` 切词，**去除首尾空白**；
2. 词间任意连续空白（空格、制表符、换行等）**折叠为单个空格**；
3. 统一**转小写**。

因此 `"Cold"`、`"cold"`、`"  Cold  "`、`"COLD"`、`"CoLd"` 的归一化结果都是 `cold`，
解析到同一概念、候选与证据完全一致；`"  Type  2 "` 归一为 `type 2`。
登记时原始文本原样保留在 `TermAssignment.Term` 中用于审计，匹配只用归一化键。
**注意**：规则不做词干/拼写纠错，只做大小写与空白归一；同义词层面的归并来自
概念定义（同义词登记），归一规则本身不做同义词扩展——能与不能解析到什么，
完全由登记数据与概念定义决定，行为可预期。需要在固定快照上判断时使用
`Reasoner.ResolveTermIn(snapshot, raw)`。

### 冲突规则（映射/术语只**标记**不拒绝，由调用方决定如何处置）

外部标识映射（`mappings`，精确键）与原始文本术语（`terms`，归一化键）是两套相互独立的
归属索引，**各自独立计算冲突**，冲突类型完全相同。术语一侧的两层匹配（登记术语 +
概念名/同义词派生）共用同一套冲突规则，但两层的冲突互不相干：登记层的冲突标记只
由登记归属决定，派生层只在无登记命中时现场判定。

| 冲突类型 | 触发条件 | 行为 |
|---|---|---|
| `same_source_conflict` | 同一个来源把同一个标识/归一化术语映射到多个不同概念 | 返回冲突类型 + 该来源的全部候选，不挑一个 |
| `cross_source_conflict` | 不同来源把同一个标识/归一化术语映射到不同概念 | 返回冲突类型 + 全部来源候选 |

- 同源冲突**优先于**跨来源冲突报告：同一术语既有同源分裂又有跨来源分歧时，先报同源。
- 两种冲突一眼分清：同源冲突 `TermConflict.Source` / `MappingConflict.Source` 给出肇事
  来源；跨来源冲突该字段为空，候选按 `(source, concept_id)` 稳定排序全部保留。
- 多来源指向**同一个**概念是"相互印证"，正常归一，并保留全部来源证据。
- 重复执行的冲突类型、候选集合与顺序**逐字节一致**（键排序 + 候选排序均确定）。
- 删除冲突一方（`DeleteTerms` / `DeleteMappings`）后，冲突标记在下次提交自动消失；
  术语键下没有任何归属时冲突状态一并清除。

### 与既有标识映射的兼容范围

- 原有 `mappings` 的归一与冲突行为**完全不变**：键仍是未归一的精确 `target_id`，
  既有用例（`TestMapping_*` 等）全部保留且不回归。
- `terms` 是并列的数据通道，走相同的导入/修订/快照/回滚机制；同一概念可同时拥有
  标识映射与术语归属，两者互不干扰（一侧出现冲突不影响另一侧的解析结论）。
- 概念名/同义词派生解析是只读的回退层：同一归一键存在登记术语时，解析结果与
  既往完全一致（派生归属不混入候选）；派生层自身也不写回任何登记数据，
  因此不会改变标识映射与登记术语的任何既有行为。
- 术语归属引用不存在的概念时同样按 `dangling_reference` 整体拒绝导入/修订。

## 一致性语义（导入 / 修订要么全成，要么全不成）

提交前会在候选快照上执行下列检查；只要存在任何一条，**整个提交被拒绝**，当前版本不变：

| 拒绝原因 RejectReason | 含义 |
|---|---|
| `cycle` | 上下位层级存在环。错误实体给出规范旋转后的环（如 `A -> C -> B -> A`） |
| `dangling_reference` | 层级边 / 实体关系 / 映射引用了未定义的概念。错误实体稳定列出全部悬空 ID |
| `duplicate_conflict` | 同一概念被重复定义且互相冲突：① 同来源同 ID 不同规范名；② 跨来源同 ID 且名称既不相等也不是已登记同义词 |
| `stale_revision` | 修订携带的 `BaseVersion` 与当前版本不一致（乐观并发检测到丢失更新） |

拒绝原因按类别 + 实体排序后输出，重复执行结果逐字节一致。

## 版本、并发与回滚语义

- 每次成功的 `Import` / `ApplyRevision` 产生一个**不可变快照**和单调递增的新版本号。
- 新快照由旧快照**深拷贝**构造；读操作先取当前快照再计算，全程无需持写锁。
  修订进行中的并发查询只会看到完整旧版本或完整新版本，**看不到半更新状态**
  （并发测试同时校验概念、层级推导与术语归属在同一快照内一致可见）。
- 写操作在互斥锁内串行化；`Revision.BaseVersion` 构成乐观并发控制，冲突时后到者收到
  `stale_revision`，不会静默覆盖。
- `Rollback(version)` 整体回滚到任一历史版本：该版本之后的提交被整体丢弃，
  查询结果（概念、边、关系、映射、**术语解析**、推导结论）恢复为该版本。回滚后再提交
  使用**全新、更大**的版本号，版本号不复用。

### 过期结论失效

- **删除概念**：级联删除相关层级边、实体关系、标识映射与**术语归属**；这些依赖项登记为
  失效结论（`StaleConclusions`，含 `hierarchy/relation/mapping/term`、失效版本与
  `concept_deleted`），并且不会再出现在正常查询结果中（`ResolveTerm` 返回 nil）。
- **改名**：与删除保持一致。概念 ID 不变、规范名更新，并登记一条 `rename` /
  `concept_renamed` 失效记录；先前依赖该概念的每条术语结论登记一条 `term` /
  `concept_renamed` 失效记录，**同时从术语索引中移除**——不会再出现在正常解析结果里
  （`ResolveTerm` 返回 nil），只能从失效信息中看到它已作废及作废原因。改名后的
  新规范名立即作为派生术语可解析；旧规范名不再是当前规范名，随之退出解析。
- 回滚到旧版本后，这些结论随快照整体恢复（术语解析结果一起退回）。

## 本地验证

需要 Go 1.25+（开发环境为 Go 1.26）。

```bash
# 1) 全部单测（带竞态检测；测试日志会打印输入与判定依据）
go test -race -count=1 ./...

# 2) 命令行手工验证
go run ./cmd/biokg import -file testdata/ontology.json
go run ./cmd/biokg query  -file testdata/ontology.json -kind ancestors -id D003
go run ./cmd/biokg query  -file testdata/ontology.json -kind mapping   -id SNOMED:73211009

# 环 / 悬空 / 重复冲突：退出码非 0，并打印区分明确的拒绝原因
go run ./cmd/biokg import -file testdata/hierarchy_cycle.json
go run ./cmd/biokg import -file testdata/dangling.json
go run ./cmd/biokg import -file testdata/duplicate_conflict.json

# 映射冲突（同源 / 跨来源）
go run ./cmd/biokg query -file testdata/mapping_conflicts.json -kind mapping -id EXT:SAMESRC
go run ./cmd/biokg query -file testdata/mapping_conflicts.json -kind mapping -id EXT:CROSS

# 术语归一（大小写/前后空白不敏感，结果与规范写法一致）
go run ./cmd/biokg query -file testdata/term_conflicts.json -kind term -id "  CoLd "
# 概念规范名/同义词无需登记即可解析（ontology.json 没有任何 terms 登记）
go run ./cmd/biokg query -file testdata/ontology.json -kind term -id "  DIABETES "
go run ./cmd/biokg query -file testdata/ontology.json -kind term -id "  Diabetes   Mellitus "
go run ./cmd/biokg query -file testdata/ontology.json -kind explain -id "term:dm"
# 术语冲突（同源 / 跨来源，CONFLICTED 标记 + 候选全保留）
go run ./cmd/biokg query -file testdata/term_conflicts.json -kind term -id "t:samesrc"
go run ./cmd/biokg query -file testdata/term_conflicts.json -kind term -id "T:CROSS"
# 术语来源依据
go run ./cmd/biokg query -file testdata/term_conflicts.json -kind explain -id "term: DIABETES "

# 端到端 JSON 演示：导入 → 传递推导 → 删除致失效 → 整体回滚
go run ./cmd/biokg demo
```

## 测试覆盖

| 测试 | 场景 |
|---|---|
| `TestImport_CycleRejected` / `..._Deterministic` | 层级环被拒绝、环路径稳定、重复结果一致 |
| `TestImport_DanglingRejected` | 层级/关系/映射三类悬空引用一次性列出 |
| `TestImport_DuplicateConflictingDefinitionRejected`、`TestImport_SameSourceDuplicateNameConflict` | 跨来源与同来源重复冲突定义 |
| `TestMapping_*` | 同源冲突、跨来源冲突、一致映射、确定性、冲突解除 |
| `TestTerm_NormalizationIsCaseAndWhitespaceInsensitive` | Cold/cold/前后空白等写法解析到同一概念、空白折叠规则 |
| `TestTerm_SameSourceConflict` / `..._CrossSourceConflict` | 两类术语冲突分别标记、肇事来源/全部候选完整保留 |
| `TestTerm_AgreementResolvesWithEvidence` / `..._UnknownTerm...` | 多来源一致归一并保留证据；未登记术语返回 nil |
| `TestTerm_ConflictDeterministicAcrossRepeats` | 重复解析冲突类型与候选顺序完全一致 |
| `TestTerm_CoexistsWithExternalIDMapping` | 术语与标识映射同时存在、互不影响，原映射行为不回归 |
| `TestTermName_CanonicalNameResolvesWithoutRegistration` | 规范名无需登记即可解析，大小写/空白变体与规范写法逐字节一致，来源依据给全 |
| `TestTermName_SynonymResolvesWithVariants` | 同义词（含多词、内部空白折叠）直接解析，证据标注 synonym |
| `TestTermName_RegisteredTermStillWinsOverDerived` | 登记术语优先于派生归属，既有行为不变 |
| `TestTermName_RenameRemovesOldTermConclusions` | 改名后旧术语结论不再解析、失效原因可识别、新名可解析、回滚恢复 |
| `TestTermName_DeleteAndRenameParity` | 删除与改名对同一术语结论的表现一致 |
| `TestTermName_DerivedCoexistsWithMappings` | 派生解析与标识映射并存，映射冲突不影响派生结论 |
| `TestTermName_DerivedResolutionDeterministic` | 派生解析重复执行结果与顺序完全一致 |
| `TestTerm_DeleteConceptInvalidatesAndRemovesTerm` | 删除概念致术语失效、登记 stale、回滚后恢复 |
| `TestTerm_RenameConceptInvalidatesTermConclusion` | 改名后先前术语结论可被识别为失效 |
| `TestTerm_RevisionUpsertDeleteAndConflictClear` | 术语增量增删、一致归并不产生冲突、删一方冲突解除 |
| `TestTerm_DanglingReferenceRejected` | 术语引用未定义概念时整体拒绝（dangling_reference） |
| `TestAncestors_*` / `TestDescendants_*` / `TestExplain_*` | 传递推导路径与逐边证据链 |
| `TestRevision_StaleBaseVersionRejected` | 乐观并发拒绝丢失更新 |
| `TestRevision_IntroducingCycleRejectedAtomically` | 修订引入环时整批回滚（顺带写入也不可见） |
| `TestRevision_DeleteInvalidatesDependentConclusions` / `..._Rename...` | 删除/改名后过期结论失效且可识别 |
| `TestRollback_RestoresWholeState` | 整体回滚恢复全部状态，新版本号不复用 |
| `TestConcurrent_ReadsNeverSeeHalfState` | 16 个读协程 × 40 次修订，概念/层级/术语只读一致快照 |
| `TestConcurrent_ConflictingWritesSerialised` | 并发写恰好一成一拒（stale） |

所有测试均使用 `t.Logf` 打印 `INPUT ...` 与 `JUDGEMENT ...`，
`go test -v` 可查看每个判定的输入与依据。
