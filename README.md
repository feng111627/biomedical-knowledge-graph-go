# 本地生物医学知识图谱与本体推理组件

一个纯本地、无外部服务依赖的 Go 组件：从本地 JSON 样例导入生物医学概念、同义词、
上下位（subClassOf）层级与实体关系，支持多来源标识/术语归一、层级传递推理、
结构化一致性校验、增量修订与整体回滚。所有结论都带来源溯源，相同输入重复执行结果一致。

## 目录结构

| 路径 | 职责 |
| --- | --- |
| `pkg/model` | 核心类型：概念、同义词、父类边、实体关系、映射、溯源；拒绝原因与冲突类型枚举 |
| `pkg/graph` | 不可变快照（Snapshot）+ `atomic.Pointer` 原子切换；派生索引（子节点、同义词、关系） |
| `pkg/ingest` | 本地 JSON 加载与一致性校验：层级环、悬空引用、重复冲突定义 |
| `pkg/mapping` | 多来源标识/术语归一与映射；两类冲突的识别与证据保留 |
| `pkg/reason` | 层级传递推理（祖先推导、逐条边溯源）与旧结论失效识别 |
| `pkg/revision` | 增量修订事务：私有草稿、提交前全量校验、原子提交、整体回滚 |
| `cmd/kgdemo` | 本地端到端可运行演示 |
| `testdata/` | 本地样例数据（合法、含环、悬空、重复冲突、同源冲突） |

## 数据格式

导入文件为一个 JSON 对象（见 `testdata/valid_dataset.json`）：

```json
{
  "name": "biomedical-sample",
  "concepts": [
    {"id": "FLU", "label": "influenza", "source": "local-ontology"}
  ],
  "synonyms": [
    {"concept_id": "FLU", "term": "flu", "source": "local-terms"}
  ],
  "parents": [
    {"concept_id": "FLU", "parent_id": "RESPDIS", "source": "mesh-lite"}
  ],
  "relations": [
    {"id": "R1", "subject_id": "OSELTAMIVIR", "predicate": "treats",
     "object_id": "FLU", "source": "drugbank-lite"}
  ],
  "mappings": [
    {"source": "mesh-lite", "external_id": "MSH:D003120",
     "concept_id": "FLU", "term": "Influenza"}
  ]
}
```

- `concepts[].source`、`synonyms[].source`、`parents[].source`、`relations[].source`、
  `mappings[].source` 表示每条断言的来源，推理结论会携带它们作为溯源。
- `parents` 中 `concept_id -> parent_id` 表示“前者 is-a 后者”，支持多继承（菱形层级）。
- `mappings` 一条记录只允许指向一个概念；同一 `external_id` 出现多个目标概念即构成冲突。

样例文件：

| 文件 | 预期判定 |
| --- | --- |
| `testdata/valid_dataset.json` | 导入成功；含跨来源互斥标识冲突与术语冲突 |
| `testdata/cycle_dataset.json` | 拒绝：`hierarchy_cycle`（A -> B -> C -> A） |
| `testdata/dangling_dataset.json` | 拒绝：`dangling_reference`（同义词/层级/关系/映射引用未定义概念） |
| `testdata/duplicate_conflict_dataset.json` | 拒绝：`duplicate_conflict`（同一 id 定义标签冲突） |
| `testdata/samesource_conflict_dataset.json` | 导入成功但映射表报告 `same_source_conflicting_targets` |

## 一致性校验规则（导入与每次提交都执行）

校验按固定顺序执行，任一不满足即**整批拒绝**，返回结构化的 `*model.RejectError`
（含 `Kind`、可解释 `Message`、环路径等证据），绝不静默丢弃记录：

1. **重复且互相冲突的定义 — `duplicate_conflict`**
   - 同一概念 id 重复出现但 `label` 不同（完全相同的重复断言幂等，允许）；
   - 同一关系 id 复用但三元组不同；概念/关系 id 为空也拒绝。
2. **悬空引用 — `dangling_reference`**
   - 同义词、父类边两端、关系主客体、映射目标引用了未定义概念。
   - 缺失 id 按字典序汇总在错误信息中，保证可解释、稳定。
3. **层级环 — `hierarchy_cycle`**
   - 对白/灰/黑三色 DFS（有界、迭代式，不会因环卡死），再次遇到灰色节点即报环；
   - 显式自环 `A -> A` 同样拒绝，错误信息给出完整环路。

## 推理规则与溯源

- `reason.AncestorsOf(snapshot, id)`：沿父类边做带访问集合的 DFS，返回**全部传递祖先**
  （支持多继承/菱形），结果按字典序排序。
- 每个 `Conclusion` 携带：
  - `revision`：推导时的快照版本；
  - `label_at`：推导时概念的标签（用于识别改名）；
  - `ancestors`：祖先集合；
  - `provenance`：推导所依赖的**每一条 is-a 边**及其来源（`source` + `child->parent`）。
- 遍历有界（visited 集合），即使把含环快照直接传入也不会死循环。

## 多来源归一与冲突规则

`mapping.New(...)` 对标识和归一化后的术语（小写 + 折叠空白，例如
`"  Cold  "` 与 `"cold"` 等价）分组，同一组指向多个概念时按以下规则标记，**绝不任选一个**：

| 冲突类型 | 触发条件 |
| --- | --- |
| `same_source_conflicting_targets` | **同一来源内**把同一标识/术语映射到多个不同概念（优先级更高） |
| `mutually_exclusive_target` | 同一标识/术语在**不同来源**间映射到互斥概念，且没有任何单一来源自相矛盾 |

冲突对象保留全部参与条目（证据），冲突列表按外部标识排序；重复构建结果顺序稳定。
无冲突时解析结果同样携带 `Provenance`，可追溯到具体来源。

## 并发一致性、修订与回滚语义

- 图谱状态以**不可变快照**表示；读者通过 `graph.Current()` 拿到快照后不受任何写入影响。
- 修订通过事务进行（`revision.Manager.Begin()`）：
  1. 从事务基础快照**复制一份私有草稿**（增量操作：增/改名/删概念、增删父类边、
     增关系、增映射、增同义词）；
  2. `Commit()` 先对草稿跑**全量一致性校验**（环/悬空/重复冲突）；
  3. 校验通过才构建全新快照并以 `atomic.Pointer.Store` **原子切换**；
  4. 校验失败则不发布任何新版本，已发布图谱保持不变。
- `Rollback()` 仅丢弃私有草稿：不触碰已发布快照与版本号，读者全程读到的是旧快照，
  **读不到任何半更新的中间状态**（并发测试用 `-race` 验证）。
- 修订号由管理器内单调递增计数器分配，提交互斥串行。

### 旧推理结论失效识别

`reason.IsStale(snapshot, conclusion)` 在以下任一情况判定结论失效，并返回原因，
调用方不应继续把旧结果当当前结论，应基于新快照重新推导：

- 起始概念被删除；
- 起始概念被改名（`label_at` 与当前标签不符）；
- 结论依赖的某个祖先被删除；
- 溯源中的任一条 is-a 边被移除（结论不再被完整支撑）；
- “概念不存在”结论在概念后来出现时失效。
- 仅版本号变化但结构完全一致（内容相同的新修订）不算失效。

## 本地验证

无需联网、无需真实术语库，全部使用 `testdata/`：

```bash
# 端到端演示：拒绝原因、查询、祖先推理+溯源、冲突、修订/回滚、失效识别
go run ./cmd/kgdemo

# 全部单元测试 + 集成测试（含并发场景，建议带竞态检测）
go test ./... -race -count=1 -v

# 覆盖率
go test ./... -count=1 -cover
```

测试覆盖的关键场景：

- 层级循环（含自环）与悬空引用、重复冲突定义，三类拒绝原因可区分；
- 跨来源互斥映射冲突与同源冲突目标，两类冲突类型可区分；
- 并发读者在高频提交/回滚期间只能看到完整快照（概念数只可能是提交前或提交后的值），
  `-race` 下无数据竞争；
- 整体回滚后版本号与内容不变；改名/删除/移边后旧结论被识别为失效。
- 所有相关用例均通过 `t.Logf` 打印输入数据与判定依据（kind、原因、证据条目）。
