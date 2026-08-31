#!/usr/bin/env bash
# 全量验证基线（事后树 troubleshooting-tree.md 主干步骤 5 / 事前树
# prevention-tree.md 分支 D 固化）。AI 改完代码跑这个，不再每次现写
# go test 命令。
#
# 用法：
#   scripts/verify.sh          全量：build + vet + -race 逐包（每包
#                              timeout 300s，挂起自动终止——事后树分支 B）
#   scripts/verify.sh --quick  build + vet + 非 race 全量单测（并行 +
#                              缓存——R59：去 -p 1 串行与 -count=1 禁缓存，
#                              51.9s → 首次 ~29s / 缓存命中 0.5s）
#   scripts/verify.sh --changed
#                              build + vet + 增量单测（git diff 变更包 +
#                              依赖它们的包——pre-commit hook 用；quick
#                              全量兜底）
#
# 环境：临时目录用仓库 .tmp/（R67：不再写 /tmp——配额满；.gitignore
# 已排除 .tmp/）。
set -uo pipefail
# pre-commit hook 环境下 git 会设置 GIT_INDEX_FILE 等变量指向主仓库
# index——嵌套 git 命令（测试里的 git worktree add / 增量构建 git 检测）
# 继承后报 "index file open failed: Not a directory"（防忘机制实战
# 抓到的坑，runbook #17）。此处 unset 双保险。
unset GIT_INDEX_FILE GIT_DIR GIT_WORK_TREE 2>/dev/null || true
cd "$(dirname "$0")/.." || exit 1

# R67：TMPDIR 指向仓库 .tmp（存在才设置——CI/其他机器无该目录保持默认）
if [ -d "$PWD/.tmp" ]; then
  export TMPDIR="$PWD/.tmp"
fi

echo "== go build ./... =="
go build ./... || { echo "FAIL: build"; exit 1; }

echo "== go vet ./... =="
go vet ./... || { echo "FAIL: vet"; exit 1; }

if [ "${1:-}" = "--quick" ]; then
  # R59：并行 + 测试缓存（go test 默认）——实测 51.9s → 缓存命中 0.5s
  echo "== go test ./...（非 race，并行 + 缓存）=="
  go test ./... || { echo "FAIL: test"; exit 1; }
  echo "OK: quick 验证通过"
  exit 0
fi

if [ "${1:-}" = "--changed" ]; then
  # R59：增量验证——git 变更（已跟踪 diff + 未跟踪新文件）的 Go 文件
  # 所属包 + 依赖这些包的包（go list -f Deps 一次取全）。无受影响包
  # 时回退全量（安全）。pre-commit hook 用；quick 全量兜底。
  echo "== go test（增量：变更包 + 依赖它们的包）=="
  changed_pkgs=$(
    { git diff --name-only --diff-filter=ACMR HEAD; git ls-files --others --exclude-standard; } 2>/dev/null \
      | grep '\.go$' \
      | while read -r f; do
          [ -f "$f" ] || continue # 已删除文件无目录可 cd
          d=$(dirname "$f")
          [ -f "$d/go.mod" ] && continue # go.mod 所在目录是模块根非包
          (cd "$d" && go list . 2>/dev/null)
        done \
      | sort -u
  )
  if [ -z "$changed_pkgs" ]; then
    echo "无变更 Go 文件——全量（缓存命中）"
    go test ./... || { echo "FAIL: test"; exit 1; }
    echo "OK: 增量验证通过"
    exit 0
  fi
  echo "变更包: $(echo "$changed_pkgs" | tr '\n' ' ')"
  targets=""
  while read -r p rest; do
    for cp in $changed_pkgs; do
      if [ "$p" = "$cp" ] || echo " $rest " | grep -q " $cp "; then
        targets="$targets $p"
        break
      fi
    done
  done < <(go list -f '{{.ImportPath}} {{join .Deps " "}}' ./... | grep -v '/tmp/' )
  if [ -z "$targets" ]; then
    echo "无包受影响——全量（缓存命中）"
    go test ./... || { echo "FAIL: test"; exit 1; }
  else
    # shellcheck disable=SC2086
    go test $targets || { echo "FAIL: test"; exit 1; }
  fi
  echo "OK: 增量验证通过"
  exit 0
fi

# R59：race 模式去 -count=1（结果也走缓存，二次命中秒回）；保留 -p 1
# 逐包（timeout 300s 防挂起——并行时无法定位挂起包）
echo "== go test -race -p 1 逐包（每包 timeout 300s，缓存）=="
fail=0
for pkg in $(go list ./...); do
  if ! timeout 300 go test -race -p 1 "$pkg"; then
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
