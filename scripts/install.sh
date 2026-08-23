#!/bin/sh
# 一键安装 codeintel（#227 分发简化）。
# 用法：
#   curl -sfL https://github.com/schaepher/codeintel/releases/latest/download/install.sh | sh
#   scripts/install.sh [--prefix <dir>] [--version <tag>] [--from-dir <dir>]
# 流程：检测 OS/ARCH → GitHub Releases 下载 tar.gz（默认 latest）→
#       解压安装到 PREFIX（默认 ~/.local/bin）→ codeintel version 验证。
# 下载失败降级：提示 go install。
# 本地模式（测试/离线）：--from-dir <dir> 从目录取产物，跳过网络。
set -eu

VERSION="latest"
PREFIX="${PREFIX:-$HOME/.local/bin}"
FROM_DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --prefix) PREFIX="$2"; shift 2 ;;
    --from-dir) FROM_DIR="$2"; shift 2 ;;
    *) echo "error: 未知参数 $1" >&2; exit 2 ;;
  esac
done

# 检测平台（支持 linux/darwin × amd64/arm64，与 make release 产物对应）
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin|linux) ;;
  *) echo "error: 不支持的 OS: $OS（支持 linux/darwin）" >&2; exit 1 ;;
esac
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "error: 不支持的架构: $ARCH（支持 amd64/arm64）" >&2; exit 1 ;;
esac

TARBALL="codeintel-${OS}-${ARCH}.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if [ -n "$FROM_DIR" ]; then
  SRC="$FROM_DIR/$TARBALL"
  [ -f "$SRC" ] || { echo "error: 本地产物不存在 $SRC" >&2; exit 1; }
  cp "$SRC" "$TMP/"
else
  if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/schaepher/codeintel/releases/latest/download/$TARBALL"
  else
    URL="https://github.com/schaepher/codeintel/releases/download/$VERSION/$TARBALL"
  fi
  echo "下载 $URL ..."
  if ! curl -sfL "$URL" -o "$TMP/$TARBALL"; then
    echo "下载失败（网络不可用或版本不存在）。降级方案：" >&2
    echo "  go install github.com/schaepher/codeintel@latest" >&2
    exit 1
  fi
fi

mkdir -p "$PREFIX"
tar -xzf "$TMP/$TARBALL" -C "$TMP"
BIN="$TMP/codeintel"
[ -f "$BIN" ] || { echo "error: 包内无 codeintel 二进制" >&2; exit 1; }
chmod +x "$BIN"
mv "$BIN" "$PREFIX/codeintel"

echo "已安装: $PREFIX/codeintel"
"$PREFIX/codeintel" version
echo "下一步: codeintel init --repo <你的仓库>"
echo "PATH 不含 $PREFIX 时: export PATH=\"\$PATH:$PREFIX\""
