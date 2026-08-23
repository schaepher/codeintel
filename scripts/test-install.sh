#!/usr/bin/env bash
# 安装分发测试（#227）——本地模式全链路：
#   构造产物（模拟 make release 输出）→ install.sh --from-dir 安装到
#   临时 PREFIX → 断言二进制存在 / 可执行 / version 输出含注入 commit。
# 用法：scripts/test-install.sh
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in darwin) OS=darwin;; *) OS=linux;; esac
ARCH="$(uname -m)"; case "$ARCH" in x86_64|amd64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac

# 1. 构造本地产物目录（tar 包内二进制名为 codeintel，与 make release 一致）
PKG="$WORK/pkg"
mkdir -p "$PKG"
TMPGIT="sha1234567890abcdef"
go build -ldflags "-X github.com/schaepher/codeintel/internal/cli.gitCommit=$TMPGIT" \
  -o "$WORK/codeintel" ./cmd/codeintel || { echo "FAIL: 构建产物"; exit 1; }
tar -czf "$PKG/codeintel-${OS}-${ARCH}.tar.gz" -C "$WORK" codeintel

# 2. install.sh --from-dir 安装到临时 PREFIX
PREFIX="$WORK/bin"
scripts/install.sh --from-dir "$PKG" --prefix "$PREFIX" > "$WORK/out.log" 2>&1
[ $? -eq 0 ] || { echo "FAIL: install.sh 退出非零"; cat "$WORK/out.log"; exit 1; }

# 3. 断言：二进制存在 / 可执行 / version 输出含注入 commit
[ -x "$PREFIX/codeintel" ] || { echo "FAIL: $PREFIX/codeintel 不存在或不可执行"; exit 1; }
VER="$("$PREFIX/codeintel" version)"
echo "$VER" | grep -q "$TMPGIT" || { echo "FAIL: version 未含注入 commit（$VER）"; exit 1; }

# 4. 负面：缺产物 → 非零退出
PKG2="$WORK/pkg2"; mkdir -p "$PKG2"
if scripts/install.sh --from-dir "$PKG2" --prefix "$PREFIX" > /dev/null 2>&1; then
  echo "FAIL: 缺产物应拒绝"; exit 1
fi

echo "OK: 安装分发本地模式全链路通过"
