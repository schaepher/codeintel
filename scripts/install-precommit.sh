#!/bin/sh
# 安装 pre-commit hook（事前树 prevention-tree.md 分支 D 固化——防忘
# 机制硬拦截）：git commit 前自动跑 scripts/verify.sh --quick
# （build + vet + 非 race 全量单测），失败拒绝提交。
# AI 或人提交时都绕不过——避免「改完忘记验证」。
#
# 用法：scripts/install-precommit.sh [仓库目录]   默认当前目录
# 幂等：重复安装覆盖。
set -e

REPO="${1:-.}"
ROOT="$(cd "$REPO" && git rev-parse --show-toplevel)"
HOOK_DIR="$ROOT/.git/hooks"
HOOK="$HOOK_DIR/pre-commit"

cat > "$HOOK" << HOOK_EOF
#!/bin/sh
# codeintel 提交前快速验证（自动生成，scripts/install-precommit.sh）
unset GIT_INDEX_FILE GIT_DIR GIT_WORK_TREE # pre-commit 环境变量泄漏防护
if ! "$ROOT/scripts/verify.sh" --quick; then
  echo "✗ 提交被拒：快速验证未通过（见上；修复后重新提交）" >&2
  exit 1
fi
HOOK_EOF
chmod +x "$HOOK"
echo "已安装 pre-commit hook → $HOOK"
echo "提交前自动跑 scripts/verify.sh --quick；全量（-race 逐包）仍手动跑 scripts/verify.sh"
