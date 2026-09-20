package sqlite

// schema 核心建表 DDL（Q235-3 加法演进：打开时总是幂等执行 CREATE
// TABLE IF NOT EXISTS，旧库自动补建缺失表）。从 db.go 拆出（行数
// 治理）。

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,  -- Canonical ID
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    file_path TEXT,
    line_start INTEGER,
    line_end INTEGER,
    properties JSON,
    -- 生成列：供签名搜索（预留，无索引；见下方 idx_nodes_signature 说明）
    signature_text TEXT GENERATED ALWAYS AS (json_extract(properties, '$.signature')) VIRTUAL,
    created_at INTEGER DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_nodes_file_kind ON nodes(file_path, kind) WHERE file_path IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name);
-- Q246：原 idx_nodes_signature(signature_text) 删除——全仓库无任何查询
-- 用过它（signature 只在 SELECT 里 json_extract 出来；唯一的 WHERE 使用
-- 是 id 主键查）。保留的是**每次节点插入**都要重算的写负担（生成列
-- 索引）。旧库由 init 里 DROP INDEX IF EXISTS 清掉。

CREATE TABLE IF NOT EXISTS edges (
    -- Q246：id 去掉 AUTOINCREMENT（无任何查询/外键用过 edges.id 值；
    -- AUTOINCREMENT 每插一行都要读改 sqlite_sequence b-tree + 额外 WAL
    -- 写入，百万级边构建实测是可观开销）
    id INTEGER PRIMARY KEY,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    tool_source TEXT NOT NULL,
    confidence REAL NOT NULL DEFAULT 0.5,
    metadata JSON,
    -- R69：同义边调用次数（UNIQUE 合并时累加——真实调用频率）
    count INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE,
    -- 同义边合并：同一 (source, target, kind) 保留最高置信度（TD.md 5.3）
    UNIQUE(source_id, target_id, kind)
);
-- Q254 Stage 1：删掉 4 个冗余 edges 索引（实测证据见 field_trace §100）：
--   idx_edges_source / idx_edges_source_kind → UNIQUE(source_id,target_id,kind)
--     自动索引提供最左前缀，且**覆盖** target_id+kind（EXPLAIN 实测：
--     SEARCH edges USING COVERING INDEX sqlite_autoindex_edges_1）
--   idx_edges_target → idx_edges_target_kind(target_id,kind) 最左前缀顶替
--   idx_edges_confidence → 全仓库无任何查询按 confidence 过滤（仅 SELECT 列）
-- 真实业务库（7.46M 边）实测这三条单列/复合索引 ≈3.5GB。旧库由
-- dropRedundantEdgeIndexes（maintenance.go）在 Open 时幂等 DROP——**不递增
-- SchemaVersion**：删索引不改表结构，不该逼用户重建整库。
CREATE INDEX IF NOT EXISTS idx_edges_kind ON edges(kind);
-- 唯一以 target_id 打头的索引（入边查询靠它，必须保留）
CREATE INDEX IF NOT EXISTS idx_edges_target_kind ON edges(target_id, kind);

CREATE TABLE IF NOT EXISTS build_metadata (
    build_id TEXT PRIMARY KEY,
    commit_sha TEXT,
    tool_name TEXT,           -- 'all' (全量) 或 'incremental'
    status TEXT,              -- 'success', 'degraded', 'failed'
    duration_ms INTEGER,
    error_message TEXT,
    nodes_count INTEGER,      -- 构建产物规模（--memory auto 判断缓存，P0④）
    edges_count INTEGER,
    degrade_stats TEXT, -- R6：降级统计 JSON（AST 死代码类问题提前暴露）
    dispatch_pkgs TEXT,
    -- Q254b：构建时工作区变更 Go 文件的内容指纹（stale 判定；空=老构建）
    worktree_fingerprint TEXT, -- P0-2：dispatch 相关包 JSON 数组（增量补 Load 用）
    timestamp INTEGER DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_build_commit ON build_metadata(commit_sha);

-- 函数字段摘要表（SSA 字段追溯，field_trace.md §5.2）
CREATE TABLE IF NOT EXISTS function_field_summary (
    function_id TEXT NOT NULL,
    access_kind TEXT NOT NULL CHECK(access_kind IN ('direct_read','direct_write','indirect_write')),
    field_path TEXT NOT NULL,
    instance_path TEXT,
    line_start INTEGER,
    code_snippet TEXT,
    FOREIGN KEY(function_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE(function_id, access_kind, field_path)
);
CREATE INDEX IF NOT EXISTS idx_summary_func_access ON function_field_summary(function_id, access_kind);
CREATE INDEX IF NOT EXISTS idx_summary_field ON function_field_summary(field_path);

-- 摘要来源表（Q161）：间接写多来源（调用点 × 被调函数），origin/confidence
-- 查询期从 dispatch_to 边 join（callee 为候选实现时自然带出）
CREATE TABLE IF NOT EXISTS summary_origins (
    function_id TEXT NOT NULL,
    access_kind TEXT NOT NULL CHECK(access_kind IN ('direct_read','direct_write','indirect_write')),
    field_path TEXT NOT NULL,
    call_line INTEGER,
    callee_id TEXT,
    FOREIGN KEY(function_id) REFERENCES nodes(id) ON DELETE CASCADE,
    UNIQUE(function_id, access_kind, field_path, call_line, callee_id)
);
CREATE INDEX IF NOT EXISTS idx_summary_origins_func ON summary_origins(function_id, access_kind);

-- 表达式索引：field_access 定位（S2/S3 起点），字段追溯，field_trace.md §5.2
-- Q246：full_path 改**部分索引**——所有等值使用（repo_trace.go 锚点、
-- repo_dispatch.go FindFieldReads）都带 kind='field_access'（SQLite 用
-- 部分索引要求该谓词在查询 WHERE 顶层合取式中出现，已用 EXPLAIN QUERY
-- PLAN 断言）；索引只维护 field_access 行，每次节点插入的 json_extract
-- 求值次数减半。idx_nodes_func_id 不动：repo_trace.go 的 ssa_value/
-- parameter 起点查询不带 kind 过滤（改部分索引会退化成全表扫）。
CREATE INDEX IF NOT EXISTS idx_nodes_field_path ON nodes(json_extract(properties, '$.full_path'))
    WHERE kind = 'field_access';
CREATE INDEX IF NOT EXISTS idx_nodes_func_id ON nodes(json_extract(properties, '$.func_id'));

-- 表关联候选缓存（P0③）：relations 结果按 build_id 持久化（--all 全量
-- 重建 / 单表查询命中直接返回）；from_col='' 为 marker 行（标记"该表已
-- 计算过、无关联"），避免无关联表每次查询重算
CREATE TABLE IF NOT EXISTS relation_candidates (
    build_id TEXT NOT NULL,
    from_table TEXT NOT NULL,
    from_col TEXT NOT NULL,
    to_table TEXT NOT NULL,
    to_col TEXT NOT NULL,
    hops INTEGER NOT NULL,
    type TEXT NOT NULL,
    PRIMARY KEY (build_id, from_table, from_col, to_table, to_col)
);
CREATE INDEX IF NOT EXISTS idx_relcand_build_from ON relation_candidates(build_id, from_table);
`

// configSchema 配置表（Q220c）：独立于 schema 版本演进——幂等补建
// （CREATE TABLE IF NOT EXISTS，打开时始终执行，旧库自动获得配置表，
// 不要求 clean 重建）；ResetGraphTables 不 DROP（clean/reindex 保留）。
// 用户连线规则：外键形态列（merchant_id 等）值来自参数、无值流验证时，
// 由用户声明规则连线。from_table=” 为模式规则（所有含 from_col 列的
// 表 → to_table.to_col）；否则为显式列对。生效时校验目标表/列存在。

const configSchema = `
CREATE TABLE IF NOT EXISTS relation_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_table TEXT NOT NULL DEFAULT '',
    from_col TEXT NOT NULL,
    to_table TEXT NOT NULL,
    to_col TEXT NOT NULL DEFAULT 'id',
    created_at INTEGER NOT NULL DEFAULT 0
);
-- Q228：全量 relations 计算进度（precompute 命令/查询端自动兜底写入，
-- 按 build_id 主键——增量构建或分析逻辑版本变更自动失效）。
-- status: pending / running / done；done_count/total_count = 已完成表数/总表数。
CREATE TABLE IF NOT EXISTS relation_progress (
    build_id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    done_count INTEGER NOT NULL DEFAULT 0,
    total_count INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
-- W2：历史问答（ask 命令/serve 对话界面的 Q&A 收集）——后续 wiki
-- --with-qa 创建时作为 AI 参考资料（符号/表名匹配相关性）。
CREATE TABLE IF NOT EXISTS qa_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    question TEXT NOT NULL,
    answer TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '', -- 打包的项目上下文摘要（符号/表名）
    agent TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0
);
`
