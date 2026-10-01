# biomedical-knowledge-graph-go（本地演示）

一套**纯本地、可复现**的生物医学知识图谱与本体推理组件：概念 / 同义词 / 上下位关系 /
实体关系的导入与查询、层级传递推导、多来源标识归一与冲突标记、增量修订、整体回滚，
以及"环 / 悬空引用 / 重复冲突定义"的一致性拒绝。

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
  reasoner.go      查询：直接/传递上下位、关系、映射归一、证据链、失效结论
cmd/biokg/         本地命令行验证工具
testdata/          样例数据（合法、环、悬空、重复冲突、映射冲突）
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
  ]
}
```

- `concepts`：内部概念。`id` 内部稳定标识；`name` 规范名；`synonyms` 同义词。
- `hierarchy`：`child is-a parent` 的有向边，必须构成 DAG。
- `relations`：非层级实体关系（subject–relation–object）。
- `mappings`：外部术语标识 `target_id` → 内部概念 `concept_id` 的来源归属。

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

### 冲突规则（映射只**标记**不拒绝，由调用方决定如何处置）

| 冲突类型 | 触发条件 | 行为 |
|---|---|---|
| `same_source_conflict` | 同一个来源把同一个 `target_id` 映射到多个不同概念 | 返回冲突类型 + 该来源的全部候选，不挑一个 |
| `cross_source_conflict` | 不同来源把同一个 `target_id` 映射到不同概念 | 返回冲突类型 + 全部来源候选 |

多来源映射到**同一个**概念是"相互印证"，正常归一，并保留两条证据。删除冲突的一方后，
冲突标记会在下次提交中自动消失。

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
  修订进行中的并发查询只会看到完整旧版本或完整新版本，**看不到半更新状态**。
- 写操作在互斥锁内串行化；`Revision.BaseVersion` 构成乐观并发控制，冲突时后到者收到
  `stale_revision`，不会静默覆盖。
- `Rollback(version)` 整体回滚到任一历史版本：该版本之后的提交被整体丢弃，
  查询结果（概念、边、关系、映射、推导结论）恢复为该版本。回滚后再提交使用**全新、
  更大**的版本号，版本号不复用。

### 过期结论失效

- **删除概念**：级联删除相关层级边、实体关系与映射；这些依赖项登记为失效结论
  （`StaleConclusions`，含 `hierarchy/relation/mapping`、失效版本与 `concept_deleted`），
  并且不会再出现在正常查询结果中。
- **改名**：概念 ID 不变、规范名更新，并登记一条 `rename` / `concept_renamed` 失效记录。
- 回滚到旧版本后，这些结论随快照整体恢复。

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
| `TestAncestors_*` / `TestDescendants_*` / `TestExplain_*` | 传递推导路径与逐边证据链 |
| `TestRevision_StaleBaseVersionRejected` | 乐观并发拒绝丢失更新 |
| `TestRevision_IntroducingCycleRejectedAtomically` | 修订引入环时整批回滚（顺带写入也不可见） |
| `TestRevision_DeleteInvalidatesDependentConclusions` / `..._Rename...` | 删除/改名后过期结论失效且可识别 |
| `TestRollback_RestoresWholeState` | 整体回滚恢复全部状态，新版本号不复用 |
| `TestConcurrent_ReadsNeverSeeHalfState` | 16 个读协程 × 40 次修订，只读一致快照 |
| `TestConcurrent_ConflictingWritesSerialised` | 并发写恰好一成一拒（stale） |

所有测试均使用 `t.Logf` 打印 `INPUT ...` 与 `JUDGEMENT ...`，
`go test -v` 可查看每个判定的输入与依据。
