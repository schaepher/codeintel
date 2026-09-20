#!/usr/bin/env bash
# sqlite 库健康诊断 + 体量/查询计划分析（事后树 troubleshooting-tree.md 分支
# D1 固化；Q254 扩 --size/--plans）。
#
# 用法：scripts/dbdiag.sh [--repo <path>] [模式]
#   默认（无模式）= 健康检查：表清单与行数 / build_metadata 最新 3 条 /
#                   完整性 marker 行提示（relation_candidates from_col=''）
#   --size        体量表（超大库需数十秒：dbstat 全页扫描）：dbstat 逐表逐索引字节数 + page_size/freelist/
#                 page_count + WAL 大小 + 平均 canonical ID 长度（按 ID 估算
#                 索引体量）；末尾给"冗余索引候选 + 预计可回收"结论
#   --plans       查询计划：对热点 SQL 批量 EXPLAIN QUERY PLAN；并在内存
#                 副本里减掉冗余索引候选后**再跑一遍**，用于判定删除是否安全
#   --all         以上全部（推荐：一次输出全部决策输入）
#   --self-test   用临时小库自检脚本本身（无需真实索引库）
#
# 全程**只读**打开（mode=ro）——可在生产库上跑。
set -euo pipefail

repo="."
mode="health"
selftest=0
while [ $# -gt 0 ]; do
  case "${1:-}" in
    --repo) repo="${2:?--repo 需要路径}"; shift 2 ;;
    --size) mode="size"; shift ;;
    --plans) mode="plans"; shift ;;
    --all) mode="all"; shift ;;
    --self-test) selftest=1; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "error: 未知参数 $1（-h 看用法）" >&2; exit 2 ;;
  esac
done

if [ "$selftest" = 1 ]; then
  python3 "$(dirname "$0")/dbdiag.py" --self-test
  exit $?
fi

db="$repo/.codeintel/codeintel.db"
if [ ! -f "$db" ]; then
  echo "error: 无索引库 $db（先 codeintel init --repo $repo）" >&2
  exit 1
fi

python3 "$(dirname "$0")/dbdiag.py" --db "$db" --mode "$mode"
