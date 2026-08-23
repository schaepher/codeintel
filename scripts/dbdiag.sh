#!/usr/bin/env bash
# sqlite 库健康诊断（事后树 troubleshooting-tree.md 分支 D1 固化）——
# 查询结果不对时先跑它确认数据在不在，不再每次临时写 sqlite3 查询
# 或临时 diag 测试文件。
#
# 用法：scripts/dbdiag.sh [--repo <path>]   默认当前目录
#
# 输出：表清单与行数 / build_metadata 最新 3 条 / 完整性 marker 行提示
#       （relation_candidates from_col='' 是完整性判定，诊断勿当数据）
set -euo pipefail

repo="."
if [ "${1:-}" = "--repo" ] && [ -n "${2:-}" ]; then
  repo="$2"
fi
db="$repo/.codeintel/codeintel.db"
if [ ! -f "$db" ]; then
  echo "error: 无索引库 $db（先 codeintel init --repo $repo）" >&2
  exit 1
fi

python3 - "$db" <<'PY'
import sqlite3, sys
db = sys.argv[1]
con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
c = con.cursor()
tables = [r[0] for r in c.execute(
    "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]
print("== 表与行数 ==")
for t in tables:
    n = c.execute(f'SELECT COUNT(*) FROM "{t}"').fetchone()[0]
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
    n = c.execute(
        "SELECT COUNT(*) FROM relation_candidates WHERE from_col=''").fetchone()[0]
    print(f"  relation_candidates from_col='' marker 行: {n}"
          "（完整性判定，非数据——诊断勿当表列；fixture 噪音过滤见 field_trace.md）")
except sqlite3.Error:
    pass
con.close()
PY
