#!/usr/bin/env python3
"""dbdiag.py —— codeintel 索引库诊断（Q254：体量/查询计划分析）。

由 scripts/dbdiag.sh 调用；也可直接 `python3 scripts/dbdiag.py --db <path> --mode all`。
全程只读（URI mode=ro）——生产库安全。
"""
from __future__ import annotations

import argparse
import os
import shutil
import sqlite3
import subprocess
import sys

# ---------------------------------------------------------------------------
# 冗余索引候选（Q254 Stage 1 决策依据）：
#   idx_edges_source       → UNIQUE(source_id,target_id,kind) 最左前缀且覆盖
#                            target_id+kind（实测 COVERING INDEX）
#   idx_edges_source_kind  → 同上（kind 是 UNIQUE 索引的列，等值过滤索引内完成）
#   idx_edges_target       → idx_edges_target_kind(target_id,kind) 最左前缀顶替
#   idx_edges_confidence   → 全仓库无任何查询按 confidence 过滤（仅 SELECT 列）
# 不能删：idx_edges_kind（邻接加载器 WHERE kind IN (...) 走它）、
#         idx_edges_target_kind（唯一以 target_id 打头的索引）。
DROP_CANDIDATES = {
    "idx_edges_source": "UNIQUE(source_id,target_id,kind) 最左前缀 + 覆盖 target_id/kind",
    "idx_edges_source_kind": "同上（kind 是 UNIQUE 索引列，索引内过滤）",
    "idx_edges_target": "idx_edges_target_kind(target_id,kind) 最左前缀顶替",
    "idx_edges_confidence": "无查询按 confidence 过滤（仅 SELECT 列）——待确认",
}

# 热点查询（--plans）：名 → SQL（含 :ID: 占位，用库里真实 ID 替换）
PLAN_QUERIES = [
    ("邻接-出边(source_id=?)", "SELECT target_id, kind FROM edges WHERE source_id = :EDGE:"),
    ("邻接-出边+kind", "SELECT target_id, kind, confidence FROM edges "
     "WHERE source_id = :EDGE: AND kind IN ('argument','returns')"),
    ("邻接-入边(target_id=?)", "SELECT source_id, kind FROM edges WHERE target_id = :EDGE:"),
    ("邻接-入边+kind", "SELECT source_id FROM edges WHERE target_id = :EDGE: AND kind = 'summary_io'"),
    ("邻接加载器(kind IN)", "SELECT source_id, target_id, kind FROM edges WHERE kind IN "
     "('calls','argument','returns','phi_operand','summary_io')"),
    ("chain(implements 反查)", "SELECT target_id FROM edges WHERE kind = 'implements' "
     "AND target_id NOT LIKE '%Unimplemented%'"),
    ("dispatch(kind 全扫)", "SELECT target_id, confidence, metadata FROM edges WHERE kind = 'dispatch_to'"),
    ("dispatch(source+kind)", "SELECT target_id FROM edges WHERE source_id = :EDGE: AND kind = 'indirect_write'"),
    ("节点主键(id=?)", "SELECT id, kind, name FROM nodes WHERE id = :NODE:"),
    ("节点按 kind", "SELECT COUNT(*) FROM nodes WHERE kind = 'function'"),
    ("节点按 func_id(表达式索引)", "SELECT id FROM nodes WHERE json_extract(properties,'$.func_id') = :NODE:"),
    ("节点按 file+kind", "SELECT id FROM nodes WHERE file_path = 'x.go' AND kind = 'function'"),
]

MINIMAL_DDL = """
CREATE TABLE nodes (id TEXT PRIMARY KEY, kind TEXT, name TEXT, file_path TEXT, properties JSON);
CREATE TABLE edges (
  id INTEGER PRIMARY KEY, source_id TEXT NOT NULL, target_id TEXT NOT NULL, kind TEXT NOT NULL,
  tool_source TEXT NOT NULL, confidence REAL NOT NULL DEFAULT 0.5, metadata JSON,
  count INTEGER NOT NULL DEFAULT 1, FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
  FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE, UNIQUE(source_id, target_id, kind));
CREATE INDEX idx_edges_source ON edges(source_id);
CREATE INDEX idx_edges_target ON edges(target_id);
CREATE INDEX idx_edges_kind ON edges(kind);
CREATE INDEX idx_edges_confidence ON edges(confidence) WHERE confidence >= 0.8;
CREATE INDEX idx_edges_source_kind ON edges(source_id, kind);
CREATE INDEX idx_edges_target_kind ON edges(target_id, kind);
CREATE TABLE build_metadata (build_id TEXT PRIMARY KEY, status TEXT, duration_ms INTEGER, error_message TEXT);
"""


def connect_ro(db: str) -> sqlite3.Connection:
    return sqlite3.connect(f"file:{db}?mode=ro", uri=True)


def mb(n: int | float) -> str:
    return f"{n / 1024 / 1024:.1f}MB"


def health(con: sqlite3.Connection, c: sqlite3.Cursor) -> None:
    tables = [r[0] for r in c.execute(
        "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]
    print("== 表与行数 ==")
    for t in tables:
        try:
            n = c.execute(f'SELECT COUNT(*) FROM "{t}"').fetchone()[0]
        except sqlite3.Error as e:
            n = f"(err {e})"
        print(f"  {t}: {n} 行")
    print("== build_metadata（最新 3 条）==")
    try:
        cols = [d[0] for d in c.execute("SELECT * FROM build_metadata LIMIT 1").description]
        for r in c.execute("SELECT * FROM build_metadata ORDER BY rowid DESC LIMIT 3"):
            print("  ", dict(zip(cols, r)))
    except sqlite3.Error as e:
        print("  (无 build_metadata:", e, ")")
    print("== 完整性 marker ==")
    try:
        n = c.execute("SELECT COUNT(*) FROM relation_candidates WHERE from_col=''").fetchone()[0]
        print(f"  relation_candidates from_col='' marker 行: {n}"
              "（完整性判定，非数据——诊断勿当表列）")
    except sqlite3.Error:
        pass


def hasIndex(c: sqlite3.Cursor, name: str) -> bool:
    return c.execute("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?",
                     (name,)).fetchone()[0] > 0


def dbstat_sizes(c: sqlite3.Cursor, db: str) -> dict[str, int] | None:
    """逐表/索引字节数；python 的 SQLite 未编译 dbstat 时回退系统 sqlite3 CLI。

    两条路都不可用返回 None（其余段落仍可输出）。
    """
    try:
        rows = c.execute(
            "SELECT name, SUM(pgsize) FROM dbstat GROUP BY name ORDER BY 2 DESC").fetchall()
        return {name: int(size) for name, size in rows}
    except sqlite3.Error:
        pass
    try:
        out = subprocess.run(
            ["sqlite3", db, "SELECT name, SUM(pgsize) FROM dbstat GROUP BY name ORDER BY 2 DESC;"],
            capture_output=True, text=True, timeout=900, check=True).stdout
        sizes = {}
        for line in out.splitlines():
            name, _, size = line.rpartition("|")
            if name and size.isdigit():
                sizes[name] = int(size)
        if sizes:
            print("  (dbstat 由系统 sqlite3 CLI 提供——python 的 SQLite 未编译该虚拟表)")
            return sizes
    except (OSError, subprocess.SubprocessError):
        pass
    return None


def size_report(db: str, c: sqlite3.Cursor) -> None:
    page_size = c.execute("PRAGMA page_size").fetchone()[0]
    page_count = c.execute("PRAGMA page_count").fetchone()[0]
    freelist = c.execute("PRAGMA freelist_count").fetchone()[0]
    fsize = os.path.getsize(db)
    wal = os.path.getsize(db + "-wal") if os.path.exists(db + "-wal") else 0
    print("== 文件与页（只读打开，mode=ro）==")
    print(f"  文件: {mb(fsize)}（{db}）")
    print(f"  page_size={page_size}  page_count={page_count}  "
          f"逻辑={mb(page_size * page_count)}  freelist={freelist} 页（{mb(page_size * freelist)}）")
    print(f"  WAL(-wal): {mb(wal)}" + ("   ⚠ 未 checkpoint（构建末尾 wal_checkpoint(TRUNCATE) 可回收）"
                                       if wal > 64 * 1024 * 1024 else ""))
    print(f"  空闲磁盘: {mb(shutil.disk_usage(os.path.dirname(os.path.abspath(db))).free)}")
    print("== pragma（P0-2/P2-6 决策输入）==")
    print("  · 库内持久值（换进程也生效）：")
    for pragma in ("journal_mode", "auto_vacuum", "page_size"):
        try:
            print(f"      {pragma:<22} = {c.execute('PRAGMA ' + pragma).fetchone()[0]}")
        except sqlite3.Error as e:
            print(f"      {pragma:<22} = (err {e})")
    print("  · 本诊断连接的默认值（**非持久**——构建期由 DSN 覆盖，见")
    print("    internal/infrastructure/sqlite/db.go：cache_size=-131072、")
    print("    synchronous(NORMAL)、foreign_keys(1)、journal_size_limit(64MB)（Q254）；")
    print("    wal_autocheckpoint 仍为默认 1000 页≈4MB——WAL 涨到 GB 级的背景）：")
    for pragma in ("synchronous", "cache_size", "foreign_keys", "wal_autocheckpoint",
                   "journal_size_limit", "temp_store", "busy_timeout"):
        try:
            print(f"      {pragma:<22} = {c.execute('PRAGMA ' + pragma).fetchone()[0]}")
        except sqlite3.Error as e:
            print(f"      {pragma:<22} = (err {e})")
    try:
        c.execute("SELECT COUNT(*) FROM sqlite_stat1").fetchone()
        print("  ⚠ 存在 sqlite_stat1（ANALYZE 统计）——查询计划可能受统计影响")
    except sqlite3.Error:
        pass

    sizes = dbstat_sizes(c, db)
    print("== dbstat：逐表/索引体量 ==")
    if sizes is None:
        print("  (本机 SQLite 未编译 dbstat——请改用系统 sqlite3 执行："
              "sqlite3 <db> \"SELECT name,sum(pgsize) FROM dbstat GROUP BY name ORDER BY 2 DESC;\")")
    else:
        total = sum(sizes.values())
        for name, s in sizes.items():
            print(f"  {name:<36} {mb(s):>9}  {100.0 * s / total:5.1f}%")
        print(f"  {'合计':<36} {mb(total):>9}")

    print("== canonical ID 体量（估算索引条目的关键输入）==")
    for tbl, col in (("edges", "source_id"), ("edges", "target_id"), ("nodes", "id")):
        try:
            n, avg, mx = c.execute(
                f"SELECT COUNT(*), AVG(LENGTH({col})), MAX(LENGTH({col})) FROM {tbl}").fetchone()
            print(f"  {tbl}.{col}: {n} 行 平均 {avg:.0f}B 最长 {mx}B")
        except sqlite3.Error as e:
            print(f"  {tbl}.{col}: (err {e})")

    edge_rows = 0
    try:
        edge_rows = c.execute("SELECT COUNT(*) FROM edges").fetchone()[0] or 0
    except sqlite3.Error:
        pass

    print("== 结构占比（P1-3 整数代理键的决策输入）==")
    if sizes:
        def agg(prefix_ok) -> int:
            return sum(v for k, v in sizes.items() if prefix_ok(k))
        edge_all = agg(lambda k: k == "edges" or k == "sqlite_autoindex_edges_1" or k.startswith("idx_edges"))
        node_all = agg(lambda k: k == "nodes" or k == "sqlite_autoindex_nodes_1" or k.startswith("idx_nodes"))
        tot = sum(sizes.values())
        print(f"  edges 表 + 边索引合计   {mb(edge_all):>9}  {100.0 * edge_all / tot:5.1f}%")
        print(f"  nodes 表 + 节点索引合计 {mb(node_all):>9}  {100.0 * node_all / tot:5.1f}%")
        print(f"  其余（摘要/来源/元数据）{mb(tot - edge_all - node_all):>9}  "
              f"{100.0 * (tot - edge_all - node_all) / tot:5.1f}%")

    print("== 冗余索引候选（Q254 Stage 1）==")
    drop_total = 0
    for idx, why in DROP_CANDIDATES.items():
        sz = (sizes or {}).get(idx)
        drop_total += sz or 0
        per = f"{sz / edge_rows:.0f}B/条" if (sz and edge_rows) else "—"
        state = mb(sz) if sz else ("已删除 ✓" if not hasIndex(c, idx) else "未知")
        print(f"  {idx:<24} {state:>9} {per:>8}  ← {why}")
    print(f"  → 预计可回收（需一次 VACUUM）: {mb(drop_total)}")
    if freelist:
        print(f"  → 当前 freelist 已有 {mb(page_size * freelist)} 可直接回收（DROP 后 VACUUM）")
    need = fsize * 1.2
    free = shutil.disk_usage(os.path.dirname(os.path.abspath(db))).free
    print(f"  → VACUUM 需 ≈{mb(fsize)} 临时空间 + 校验余量；当前空闲 {mb(free)}"
          + ("   ⚠ 不足，VACUUM 前先清理磁盘" if free < need else "   ✓ 充足"))


def plans_of(c: sqlite3.Cursor, queries: list[tuple[str, str]], samples: dict[str, str]) -> None:
    for name, sql in queries:
        q = sql
        for key, val in samples.items():
            q = q.replace(f":{key}:", "'" + val.replace("'", "''") + "'")
        try:
            rows = c.execute("EXPLAIN QUERY PLAN " + q).fetchall()
        except sqlite3.Error as e:
            print(f"  {name}: (err {e})")
            continue
        print(f"  {name}:")
        for r in rows:
            detail = r[3] if len(r) > 3 else str(r)
            print(f"      {detail}")


def mem_variant(con: sqlite3.Connection, drop: bool) -> sqlite3.Connection:
    """内存副本：复制真实 schema（可选去掉候选索引），插少量样本行后跑计划。

    注意：先把 sqlite_master 行**全部取回**再执行 DDL（同连接迭代中执行
    Query 会锁表/中断迭代——本项目 sqlite 坑清单里的一条）。
    """
    mem = sqlite3.connect(":memory:")
    mc = mem.cursor()
    rows = con.cursor().execute(
        "SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL").fetchall()
    for typ, name, sql in rows:
        if typ == "table" and not name.startswith("sqlite_"):
            mc.execute(sql)
    for typ, name, sql in rows:
        if typ != "index" or (drop and name in DROP_CANDIDATES):
            continue
        mc.execute(sql)
    for i in range(200):  # 少量样本行（计划选择主要看索引可用性）
        mc.execute("INSERT INTO nodes(id, kind, name, file_path) VALUES(?,?,?,?)",
                   (f"symbol:go:example.com/m:fn{i}", "function", f"fn{i}", f"f{i}.go"))
        mc.execute("INSERT INTO edges(id, source_id, target_id, kind, tool_source, confidence)"
                   " VALUES(?,?,?,?,?,?)",
                   (i + 1, f"symbol:go:example.com/m:fn{i}", f"symbol:go:example.com/m:fn{(i + 1) % 200}",
                    "calls", "ssa", 0.8))
    return mem


def plans(con: sqlite3.Connection, c: sqlite3.Cursor) -> None:
    samples = {"EDGE": "symbol:go:example.com/m:main", "NODE": "symbol:go:example.com/m:main"}
    for key, sql in (("EDGE", "SELECT source_id FROM edges LIMIT 1"),
                     ("NODE", "SELECT id FROM nodes LIMIT 1")):
        try:
            row = c.execute(sql).fetchone()
            if row and row[0]:
                samples[key] = str(row[0])
        except sqlite3.Error:
            pass
    print(f"== 查询计划（真实库；样本 EDGE={samples['EDGE'][:50]} NODE={samples['NODE'][:50]}）==")
    plans_of(c, PLAN_QUERIES, samples)
    print("== 查询计划（内存副本：去掉冗余索引候选后）==")
    try:
        mem = mem_variant(con, drop=True)
        plans_of(mem.cursor(), PLAN_QUERIES, samples)
        print("  说明：计划按 schema 选索引（样本行少，用于结构判定）；"
              "若同一查询在两侧都仍走索引 → 删除安全。")
    except sqlite3.Error as e:
        print(f"  (内存副本构造失败：{e}——跳过；以真实库计划 + EXPLAIN 结果人工判定)")


def self_test() -> int:
    import tempfile
    with tempfile.TemporaryDirectory() as d:
        db = os.path.join(d, "t.db")
        c = sqlite3.connect(db).cursor()
        c.executescript(MINIMAL_DDL)
        for i in range(50):
            c.execute("INSERT INTO nodes(id, kind, name) VALUES(?,?,?)",
                      (f"symbol:go:x:fn{i}", "function", f"fn{i}"))
            c.execute("INSERT INTO edges(id,source_id,target_id,kind,tool_source) "
                      "VALUES(?,?,?,?,?)", (i + 1, f"symbol:go:x:fn{i}", f"symbol:go:x:fn{(i + 1) % 50}",
                                            "calls", "ssa"))
        c.connection.commit()
        c.connection.close()
        con = connect_ro(db)
        cur = con.cursor()
        health(con, cur)
        size_report(db, cur)
        plans(con, cur)
        con.close()
    print("self-test: OK")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--db")
    ap.add_argument("--mode", default="health", choices=["health", "size", "plans", "all"])
    ap.add_argument("--self-test", action="store_true")
    a = ap.parse_args()
    if a.self_test:
        return self_test()
    if not a.db:
        print("error: 需要 --db <path>", file=sys.stderr)
        return 2
    con = connect_ro(a.db)
    c = con.cursor()
    if a.mode in ("health", "all"):
        health(con, c)
    if a.mode in ("size", "all"):
        size_report(a.db, c)
    if a.mode in ("plans", "all"):
        plans(con, c)
    con.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
