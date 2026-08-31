#!/bin/sh
# pre-commit 文件行数检查（事前树分支 D 防忘机制扩展）：staged 的
# .go 文件行数 >300 拒绝提交——要求先用 dev-line-limit skill 拆分。
# 报错提示用 skill（asttool funcsize 看函数分布 / analyze+split 拆文件）。
#
# 用法：scripts/check-file-size.sh [仓库目录]   默认当前目录
# 退出码：0 = 全部 ≤300；1 = 有超行文件（pre-commit 据此拒绝）
# skills/ 目录排除：随技能分发的工具源码（asttool 等）非项目自身代码，
# 不受 300 行规则约束（行数治理针对项目代码）。
unset GIT_INDEX_FILE GIT_DIR GIT_WORK_TREE # pre-commit 环境变量泄漏防护

REPO="${1:-.}"
ROOT="$(cd "$REPO" && git rev-parse --show-toplevel)"
cd "$ROOT" || exit 1

# staged 的 .go 文件（新增/修改/复制；删除/改名不需检查）。
FILES="$(git diff --cached --name-only --diff-filter=ACM -- '*.go' | grep -v '^skills/') "
[ -z "$FILES" ] && exit 0

BAD=""
for f in $FILES; do
  [ -f "$f" ] || continue
  LINES=$(wc -l < "$f" | tr -d ' ')
  if [ "$LINES" -gt 300 ]; then
    BAD="$BAD
  $f（$LINES 行）"
  fi
done

if [ -n "$BAD" ]; then
  echo "✗ 提交被拒：以下 Go 文件超过 300 行，需先用 dev-line-limit skill 拆分：" >&2
  echo "$BAD" >&2
  echo >&2
  echo "  1) 看函数/方法行数分布：  cd skills/dev-line-limit/scripts/asttool && go run . funcsize <file>" >&2
  echo "  2) 按主题拆文件：          go run . analyze <file> / go run . split <src.go> <out.go>:Name1,Name2" >&2
  echo "  3) 清理孤立注释后重跑 verify.sh --quick 再提交" >&2
  exit 1
fi
exit 0
