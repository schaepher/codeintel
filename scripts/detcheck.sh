#!/usr/bin/env bash
# Q250 图构建确定性自检：同一仓库连续构建两次（同样 workers），四类产物
# 必须完全一致——节点 ID 集合、节点内容（properties 键序归一化）、边集合
# （含 count）、function_field_summary。
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

for i in 1 2; do
  rm -rf "$REPO/.codeintel"
  if ! "$TMP/codeintel" init --repo "$REPO" --workers "$WORKERS" >"$TMP/run$i.log" 2>&1; then
    echo "init #$i 失败（见 $TMP/run$i.log）"; exit 2
  fi
  DB="$REPO/.codeintel/codeintel.db"
  sqlite3 "$DB" "select id from nodes order by id;" > "$TMP/ids$i"
  sqlite3 "$DB" "select id||'|'||kind||'|'||name||'|'||coalesce(file_path,'')||'|'||coalesce(line_start,0)||'|'||coalesce(line_end,0)||'|'||coalesce(properties,'') from nodes order by id;" > "$TMP/nodes$i"
  sqlite3 "$DB" "select source_id||'|'||target_id||'|'||kind||'|'||count from edges order by source_id,target_id,kind;" > "$TMP/edges$i"
  sqlite3 "$DB" "select function_id||'|'||access_kind||'|'||field_path||'|'||coalesce(instance_path,'')||'|'||coalesce(line_start,0)||'|'||coalesce(code_snippet,'') from function_field_summary order by function_id,access_kind,field_path;" > "$TMP/summ$i"
done

python3 - "$TMP" <<'PY'
import json, sys
tmp = sys.argv[1]
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
checks = [
    ("节点 ID",  lines(f"{tmp}/ids1"), lines(f"{tmp}/ids2")),
    ("节点内容", norm_nodes(f"{tmp}/nodes1"), norm_nodes(f"{tmp}/nodes2")),
    ("边(含count)", lines(f"{tmp}/edges1"), lines(f"{tmp}/edges2")),
    ("摘要", lines(f"{tmp}/summ1"), lines(f"{tmp}/summ2")),
]
bad = 0
for name, a, b in checks:
    d = diff(a, b)
    status = "OK" if not d else f"差异 {len(d)}"
    print(f"  {name}: {status}（共 {len(a)}/{len(b)}）")
    if d:
        bad += len(d)
        for x in d[:3]:
            print(f"      {x[:170]}")
print("确定性自检：" + ("全等 ✓" if bad == 0 else f"不一致（{bad} 条）✗"))
sys.exit(0 if bad == 0 else 1)
PY
