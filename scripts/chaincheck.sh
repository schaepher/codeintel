#!/usr/bin/env bash
# Q252c 跨函数链完整性检查（大仓口径）：对一个**已建索引**的仓库量化
# "值流链能否从图上查出来"——不是"图里有没有边"，而是"query path 找不找
# 得到"。Q252b 抓到的两个真 bug 都属于这一类静默断链：
#   ① 同一 SSA 值分裂成两个节点（alias 边与 argument/returns 边分家）
#   ② GetPath 把 maxDepth 当节点预算（可达的两点报"无路径"）
# 两者都不会让既有断言变红，只在用户查询时表现为"链断了"。
#
# 检查项：
#   0 容忍  一跳对（argument/returns 边两端）经 action.Path 必须 100% 可达
#   基线项  多跳抽样可达率（≥2 跳） / alias 连通数 / 分裂候选数
#           —— 存在 scripts/baselines/chain-<label>.json 时比较（容差默认 2%）
#
# 注意：本脚本**不建索引**——度量反映"当前库 + 当前查询实现"。改动图构建
# （ssa/scip/ast 适配器）后要先重新 init/reindex，再跑本脚本。
#
# 用法：
#   scripts/chaincheck.sh --repo /path/to/repo [--label go2o]   # 比较基线
#   scripts/chaincheck.sh --repo /path/to/repo --update         # 写/更新基线
#   scripts/chaincheck.sh --repo /path/to/repo --one-hop 500 --multi-hop 200
# 退出码：0 = 通过；1 = 0 容忍项不达标或基线回归；2 = 环境问题（未索引等）
set -uo pipefail
REPO=""
LABEL=""
ONE_HOP=200
MULTI_HOP=100
HOPS=4
TOLERANCE=0.02
UPDATE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --repo) REPO=${2:?}; shift 2 ;;
    --label) LABEL=${2:?}; shift 2 ;;
    --one-hop) ONE_HOP=${2:?}; shift 2 ;;
    --multi-hop) MULTI_HOP=${2:?}; shift 2 ;;
    --hops) HOPS=${2:?}; shift 2 ;;
    --tolerance) TOLERANCE=${2:?}; shift 2 ;;
    --update) UPDATE=1; shift ;;
    -h|--help) sed -n '2,22p' "$0"; exit 0 ;;
    *) echo "未知参数: $1（-h 看用法）"; exit 2 ;;
  esac
done
[ -n "$REPO" ] || { echo "用法: scripts/chaincheck.sh --repo <已索引仓库> [--label X] [--update]"; exit 2; }
[ -d "$REPO" ] || { echo "仓库不存在: $REPO"; exit 2; }
REPO=$(cd "$REPO" && pwd)
[ -f "$REPO/.codeintel/codeintel.db" ] || {
  echo "未建索引: $REPO/.codeintel/codeintel.db 不存在（先 codeintel init --repo $REPO）"; exit 2; }
[ -n "$LABEL" ] || LABEL=$(basename "$REPO")
cd "$(dirname "$0")/.." || exit 2
[ -d "$PWD/.tmp" ] && export TMPDIR="$PWD/.tmp"
[ -d "$HOME/go/bin" ] && PATH="$HOME/go/bin:$PATH"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
echo "链完整性检查：$REPO（label=$LABEL，一跳 $ONE_HOP / 多跳 $MULTI_HOP，hops=$HOPS，容差 $TOLERANCE）"
CHAIN_REPO="$REPO" CHAIN_LABEL="$LABEL" CHAIN_ONE_HOP="$ONE_HOP" \
  CHAIN_MULTI_HOP="$MULTI_HOP" CHAIN_HOPS="$HOPS" CHAIN_TOLERANCE="$TOLERANCE" \
  CHAIN_UPDATE="$UPDATE" \
  go test -count=1 -tags integration -run TestChainIntegrityBaseline -v ./integration/ \
  >"$TMP/out.txt" 2>&1
RC=$?
# 度量汇总（CHAIN-RESULT 行由测试打印；失败时也打印，便于对照）
if grep -aq "CHAIN-RESULT" "$TMP/out.txt"; then
  python3 - "$TMP/out.txt" <<'PY'
import json, sys, re
raw = open(sys.argv[1], encoding='utf-8', errors='replace').read()
m = json.loads(re.search(r'CHAIN-RESULT (\{.*\})', raw).group(1))
one = m['one_hop_ok'] / m['one_hop_sampled'] if m['one_hop_sampled'] else 0
multi = m['multi_hop_ok'] / m['multi_hop_sampled'] if m['multi_hop_sampled'] else 0
print(f"  一跳对可达   {m['one_hop_ok']}/{m['one_hop_sampled']} = {one:.4f}（0 容忍：必须 1.0）")
print(f"  多跳对可达   {m['multi_hop_ok']}/{m['multi_hop_sampled']} = {multi:.4f}（≥2 跳抽样）")
print(f"  alias 连通数 {m['alias_connected']}（别名边落点同时挂数据流/传参边）")
print(f"  分裂候选     {m['split_candidates']}（同一 SSA 值被两条路径写成分裂节点）")
print(f"  图规模       nodes={m['nodes']} edges={m['edges']} ssa_value={m['ssa_values']}")
print(f"  耗时         {m['elapsed_ms']}ms")
PY
else
  echo "（未取得度量——见下方测试输出）"
fi
grep -aE "^--- FAIL|_test\.go:[0-9]+: " "$TMP/out.txt" | grep -av "CHAIN-RESULT" | sed 's/^/  /' | head -20
if [ "$RC" -eq 0 ]; then
  echo "链完整性：通过 ✓"
else
  echo "链完整性：不通过 ✗（退出码 $RC）"; tail -5 "$TMP/out.txt" | sed 's/^/  /'
fi
exit "$RC"
