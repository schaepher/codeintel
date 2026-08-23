#!/usr/bin/env bash
# 提交前验证软提醒（PostToolUse on Edit/Write，事前树分支 D 防忘机制
# 兜底）。改动 Go 文件后输出一行提醒：commit 前跑 scripts/verify.sh
# （pre-commit hook 已装时提交自动跑 quick 版，此提醒覆盖未装场景）。
# stdin: hook JSON（{"tool_input": {"file_path": ...}}）
set -euo pipefail

repo="${CLAUDE_PROJECT_DIR:-$PWD}"
file_path=""
if [ -t 0 ]; then
  echo "提示：非阻断验证提醒需要 hook JSON stdin（Claude Code 自动传入）。" >&2
  exit 0
fi
file_path=$(python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('tool_input', {}).get('file_path', ''))
except Exception:
    print('')
")

case "$file_path" in
  *.go)
    echo "提示：改动 Go 代码后、git commit 前运行 \`scripts/verify.sh\`（全量：-race 逐包；--quick：build+vet+单测）。已装 pre-commit hook 时提交会自动跑 quick 版。"
    ;;
esac
exit 0
