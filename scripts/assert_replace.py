#!/usr/bin/env python3
"""带断言的文本替换（runbook #10 / 事前树 prevention-tree.md 分支 D 固化）。

str.replace 找不到串时不报错——静默不生效（Q225/Q226 教训）。本工具
替换前先断言出现次数，不满足直接失败退出非零。

用法：
  assert_replace.py <file> <old> <new>            # 断言恰好 1 次
  assert_replace.py --all <file> <old> <new>       # 断言 >=1 次，全部替换
  assert_replace.py --count N <file> <old> <new>   # 断言恰好 N 次
"""
import argparse
import sys


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--all", action="store_true", help="全部替换（断言至少 1 次）")
    ap.add_argument("--count", type=int, default=None, help="断言恰好出现 N 次")
    ap.add_argument("file")
    ap.add_argument("old")
    ap.add_argument("new")
    a = ap.parse_args()

    with open(a.file, encoding="utf-8") as f:
        s = f.read()
    n = s.count(a.old)

    if a.count is not None:
        ok = n == a.count
    elif a.all:
        ok = n >= 1
    else:
        ok = n == 1
    if not ok:
        expect = (
            f"恰好 {a.count} 次" if a.count is not None
            else "至少 1 次" if a.all else "恰好 1 次")
        print(f"error: {a.file} 中 {a.old!r} 出现 {n} 次，预期 {expect}", file=sys.stderr)
        return 1

    with open(a.file, "w", encoding="utf-8") as f:
        f.write(s.replace(a.old, a.new))
    print(f"ok: {a.file} 替换 {n} 处")
    return 0


if __name__ == "__main__":
    sys.exit(main())
