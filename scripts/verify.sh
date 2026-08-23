#!/usr/bin/env bash
# 全量验证基线（事后树 troubleshooting-tree.md 主干步骤 5 / 事前树
# prevention-tree.md 分支 D 固化）。AI 改完代码跑这个，不再每次现写
# go test 命令。
#
# 用法：
#   scripts/verify.sh          全量：build + vet + -race -count=1 -p 1
#                              逐包（每包 timeout 300s，挂起自动终止——
#                              事后树分支 B）
#   scripts/verify.sh --quick  build + vet + 非 race 全量单测
#                              （pre-commit hook 用；提交前快速把关）
#
# 环境：/tmp 配额满时自动切 TMPDIR（runbook #1）。
set -uo pipefail
# pre-commit hook 环境下 git 会设置 GIT_INDEX_FILE 等变量指向主仓库
# index——嵌套 git 命令（测试里的 git worktree add / 增量构建 git 检测）
# 继承后报 "index file open failed: Not a directory"（防忘机制实战
# 抓到的坑，runbook #17）。此处 unset 双保险。
unset GIT_INDEX_FILE GIT_DIR GIT_WORK_TREE 2>/dev/null || true
cd "$(dirname "$0")/.." || exit 1

if [ ! -d /home/schaepher/.tmp-build ]; then
  mkdir -p /home/schaepher/.tmp-build
fi
if [ "${TMPDIR:-}" != "/home/schaepher/.tmp-build" ]; then
  export TMPDIR=/home/schaepher/.tmp-build
  echo "TMPDIR -> $TMPDIR（runbook #1：/tmp 配额满）"
fi

echo "== go build ./... =="
go build ./... || { echo "FAIL: build"; exit 1; }

echo "== go vet ./... =="
go vet ./... || { echo "FAIL: vet"; exit 1; }

if [ "${1:-}" = "--quick" ]; then
  echo "== go test ./...（非 race）=="
  go test -count=1 -p 1 ./... || { echo "FAIL: test"; exit 1; }
  echo "OK: quick 验证通过"
  exit 0
fi

echo "== go test -race -count=1 -p 1 逐包（每包 timeout 300s）=="
fail=0
for pkg in $(go list ./...); do
  if ! timeout 300 go test -race -count=1 -p 1 "$pkg"; then
    echo "FAIL: $pkg（挂起已被 timeout 终止，事后树分支 B）"
    fail=1
  fi
done
if [ $fail -eq 0 ]; then
  echo "OK: 全量验证通过（13 包基线）"
  exit 0
fi
echo "失败包见上；修复后重跑 scripts/verify.sh"
exit 1
