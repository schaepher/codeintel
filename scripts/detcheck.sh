#!/usr/bin/env bash
# 图构建确定性 + 包缓存保真度自检：同一仓库连续构建**三次**（同样
# workers），四类产物必须完全一致——节点 ID 集合、节点内容（properties
# 键序归一化）、边集合（含 count）、function_field_summary。
#   构建 1（清库冷构建）vs 构建 2（清库冷构建）→ 确定性
#   构建 2（冷）vs 构建 3（**不清库**，包缓存全命中）→ 缓存重放保真度
# Q252：第 3 次对照抓到过"缓存收集用覆盖语义"导致的暖构建少 43 边 /
# 1313 摘要（冷只算一次、暖从缓存重放，两者必须一致）。
#
# 为什么需要它：Go 的 map 迭代顺序每次随机，任何"遍历 map → 决定发射
# 顺序 / 命名归属 / 先到先得传播"的代码路径都会让同一次构建两次运行
# 产出不同的图（Q249 前 go2o 实测 workers=1 双跑：nodes 1670 行、摘要
# 136 行、alias 边 492 行差异）。CI 里的 fixture 级门槛（ssa 单测 +
# integration TestBuildDeterminismFixtureApp）覆盖有限——大仓协议才是
# 权威口径（见 docs/field_trace.md §91）。
#
# 用法：
#   scripts/detcheck.sh <repo>            # 默认 workers=1（最易暴露顺序依赖）
#   scripts/detcheck.sh <repo> 8          # 生产默认并发
# 退出码：0 = 四类全等；1 = 有差异（打印前几条）；2 = 构建失败
set -uo pipefail
REPO=${1:?用法: scripts/detcheck.sh <repo> [workers]}
WORKERS=${2:-1}
[ -d "$REPO" ] || { echo "仓库不存在: $REPO"; exit 2; }
cd "$(dirname "$0")/.." || exit 2
[ -d "$PWD/.tmp" ] && export TMPDIR="$PWD/.tmp"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
go build -o "$TMP/codeintel" ./cmd/codeintel || { echo "build 失败"; exit 2; }

for i in 1 2 3; do
  # 第 3 次不清库：走包缓存命中路径（保真度对照）
  [ "$i" != "3" ] && rm -rf "$REPO/.codeintel"
  if ! "$TMP/codeintel" init --repo "$REPO" --workers "$WORKERS" >"$TMP/run$i.log" 2>&1; then
    echo "init #$i 失败（见 $TMP/run$i.log）"; exit 2
  fi
  DB="$REPO/.codeintel/codeintel.db"
  # Q254c：edges 已改整数代理键（source_ref/target_ref）——dump 走兼容视图
  # edges_v（暴露 canonical ID）。**每条 dump 都检查退出码与空集**：曾因列名
  # 变更让 dump 静默失败 → 两侧都是 0 行、看着"全等通过"（假绿）。
  dump() { # dump <out> <sql>
    if ! sqlite3 "$DB" "$2" > "$1"; then
      echo "dump 失败（SQL 与 schema 不一致?）：$2"; exit 2
    fi
    [ -s "$1" ] || { echo "dump 为空（schema/查询不匹配?）：$2"; exit 2; }
  }
  dump "$TMP/ids$i" "select id from nodes order by id;"
  dump "$TMP/nodes$i" "select id||'|'||kind||'|'||name||'|'||coalesce(file_path,'')||'|'||coalesce(line_start,0)||'|'||coalesce(line_end,0)||'|'||coalesce(properties,'') from nodes order by id;"
  dump "$TMP/edges$i" "select source_id||'|'||target_id||'|'||kind||'|'||count from edges_v order by source_id,target_id,kind;"
  dump "$TMP/summ$i" "select function_id||'|'||access_kind||'|'||field_path||'|'||coalesce(instance_path,'')||'|'||coalesce(line_start,0)||'|'||coalesce(code_snippet,'') from function_field_summary order by function_id,access_kind,field_path;"
done

python3 - "$TMP" <<'PY'
import json, sys
tmp = sys.argv[1]
import collections
def lines(p):
    return [l.rstrip("\n") for l in open(p)]
def norm_nodes(p):
    out = []
    for line in open(p):
        parts = line.rstrip("\n").split("|", 6)
        if len(parts) < 7:
            continue
        try:
            props = json.dumps(json.loads(parts[6]), sort_keys=True, ensure_ascii=False)
        except Exception:
            props = parts[6]
        out.append("|".join(parts[:6]) + "|" + props)
    return out
def diff(a, b):
    return sorted(set(a) ^ set(b))
bad = 0
for label, ia, ib in (("确定性(冷1 vs 冷2)", 1, 2), ("缓存重放保真(冷2 vs 暖3)", 2, 3)):
    checks = [
        ("节点 ID", lines(f"{tmp}/ids{ia}"), lines(f"{tmp}/ids{ib}")),
        ("节点内容", norm_nodes(f"{tmp}/nodes{ia}"), norm_nodes(f"{tmp}/nodes{ib}")),
        ("边(含count)", lines(f"{tmp}/edges{ia}"), lines(f"{tmp}/edges{ib}")),
        ("摘要", lines(f"{tmp}/summ{ia}"), lines(f"{tmp}/summ{ib}")),
    ]
    print(f"  [{label}]")
    for name, a, b in checks:
        d = diff(a, b)
        status = "OK" if not d else f"差异 {len(d)}"
        print(f"    {name}: {status}（共 {len(a)}/{len(b)}）")
        if d:
            bad += len(d)
            for x in d[:3]:
                print(f"        {x[:170]}")
print("确定性 + 保真度自检：" + ("全等 ✓" if bad == 0 else f"不一致（{bad} 条）✗"))
sys.exit(0 if bad == 0 else 1)
PY
