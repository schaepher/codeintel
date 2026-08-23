#!/usr/bin/env python3
"""wiki 交付前检查（#248 检查证据机制）——生成物逐项自检，输出
PASS/FAIL 清单作为「检查到位」的证据。交付前必跑（wiki skill）。

用法：wiki-check.py <out-dir> [--html]
  <out-dir>  wiki 生成目录（含 tables.md / *.md，或 index.html）
  退出码：0 = 全部 PASS；1 = 有 FAIL
"""
import os
import re
import subprocess
import sys

OUT = sys.argv[1]
IS_HTML = "--html" in sys.argv

results = []  # (name, ok, detail)


def check(name, ok, detail):
    results.append((name, ok, detail))
    print(("  ✓ " if ok else "  ✗ ") + name + (" — " + detail if detail else ""))


# 收集全部文本（md 文件或 html 正文）
texts = {}
if IS_HTML:
    with open(OUT + "/index.html", encoding="utf-8") as f:
        texts["index.html"] = f.read()
else:
    import glob
    for p in glob.glob(OUT + "/*.md"):
        with open(p, encoding="utf-8") as f:
            texts[p.split("/")[-1]] = f.read()

all_text = "\n".join(texts.values())

# 1. 字段名无噪音：扫描表格字段名单元格（md: | x |；html: <td>x</td>）
names = set()
if IS_HTML:
    # 字段表第一列（<tr><td>字段名</td>...）——排除表头与建表语句行
    for m in re.finditer(r"<tr><td>([a-zA-Z_][a-zA-Z_0-9.'()]*?)</td>", all_text):
        names.add(m.group(1).strip())
else:
    for m in re.finditer(r"^\| ([a-zA-Z_][a-zA-Z_0-9.']*?) \|", all_text, re.M):
        names.add(m.group(1).strip())
bad = [n for n in names if not re.fullmatch(r"[a-zA-Z_][a-zA-Z_0-9]*", n)]
KW = {"distinct", "select", "from", "where", "and", "or", "not", "in", "join",
      "left", "right", "inner", "outer", "limit", "offset", "order", "group",
      "by", "having", "as", "case", "when", "then", "else", "end", "null",
      "true", "false", "count", "sum", "avg", "max", "min", "exists"}
kw = [n for n in names if n.lower() in KW or n.isdigit()]
noise = sorted(set(bad) | set(kw))
check("字段名无噪音", not noise, f"扫描 {len(names)} 个字段名，异常 {len(noise)} 个: {noise[:5]}" if noise else f"{len(names)} 个字段名合法")

# 2. 职责非空：无描述提示次数 ≤ 模块页数（每模块至多一次）
empty = len(re.findall(r"无描述", all_text))
mods = len(re.findall(r"^# ", all_text, re.M)) + (1 if IS_HTML else 0)
check("职责区块非空", empty <= max(mods, 1), f"{empty} 处缺失（模块/页面 {mods}）")

# 3. 表详情非空：无字段信息提示次数（HTML 表小节用 id="tbl- 计数）
noinfo = len(re.findall(r"无字段信息", all_text))
if IS_HTML:
    tables = len(re.findall(r'id="tbl-', all_text))
else:
    tables = len(re.findall(r"^## ", all_text, re.M))
check("表字段信息存在", noinfo <= max(tables, 1), f"{noinfo} 张表缺字段（表小节 {tables}）")

# 4. 时序图非空：无调用链/无调用数据提示次数 ≤ 模块数
noseq = len(re.findall(r"无调用链", all_text))
noscore = len(re.findall(r"无调用数据", all_text))
check("时序图非空", noseq + noscore <= max(mods, 1),
      f"{noseq} 无调用链 + {noscore} 无调用数据")

# 5. 链接无断裂（HTML）：href 锚点目标存在
if IS_HTML:
    html = texts["index.html"]
    ids = set(re.findall(r'id="([^"]+)"', html))
    hrefs = set(re.findall(r'href="#([^"]+)"', html))
    broken = sorted(h for h in hrefs if h not in ids)
    check("链接无断裂", not broken, f"{len(hrefs)} 个锚点，断裂 {len(broken)}: {broken[:5]}" if broken else f"{len(hrefs)} 个锚点全部有效")
else:
    # md：tables.md 内锚点（## 表名）与模块页链接 tables.md#表名
    tb = texts.get("tables.md", "")
    tb_ids = set(re.findall(r"^## ([a-zA-Z_0-9]+)", tb, re.M))
    refs = set(re.findall(r"tables\.md#([a-zA-Z_0-9]+)", all_text))
    broken = sorted(r for r in refs if r not in tb_ids)
    check("链接无断裂", not broken, f"{len(refs)} 个表链接，断裂 {len(broken)}: {broken[:5]}" if broken else f"{len(refs)} 个表链接全部有效")

# 6. HTML JS 语法（模板改动后必查）
if IS_HTML:
    js = "\n".join(re.findall(r"<script>(.*?)</script>", html, re.S))
    ok = True
    detail = "JS 语法 OK"
    if js.strip():
        p = subprocess.run(["node", "--check", "-"], input=js.encode(), capture_output=True)
        ok = p.returncode == 0
        detail = (p.stderr.decode()[:120] if not ok else f"JS 语法 OK（{len(js)} 字节）")
    check("HTML JS 语法", ok, detail)

# 7. mermaid 语法（Q251 补：包间调用图 [cli] 纯方括号 / 时序图括号
# 参与者名曾渲染失败——用真实 mermaid 解析器（jsdom + mermaid）逐个
# parse 生成物的全部图）
blocks_mm = (re.findall(r'<pre class="mermaid">(.*?)</pre>', all_text, re.S)
             if IS_HTML else re.findall(r'```mermaid\n(.*?)```', all_text, re.S))
if blocks_mm:
    import tempfile
    tmp = tempfile.NamedTemporaryFile('w', suffix='.html', delete=False, encoding='utf-8')
    tmp.write("".join('<pre class="mermaid">%s</pre>' % b for b in blocks_mm))
    tmp.close()
    p = subprocess.run(["node", "scripts/mermaid-check/check.mjs", tmp.name],
                       capture_output=True, text=True)
    os.unlink(tmp.name)
    last = [l for l in p.stdout.strip().split('\n') if l.strip()][-1:] or [""]
    check("mermaid 语法", p.returncode == 0,
          last[0] if p.returncode == 0 else p.stdout.strip()[-200:])
else:
    check("mermaid 语法", True, "无 mermaid 块")

# 汇总
fails = [r for r in results if not r[1]]
print(f"\nwiki-check: {len(results) - len(fails)}/{len(results)} PASS")
if fails:
    for n, _, d in fails:
        print("  FAIL: " + n + " — " + d)
    sys.exit(1)
print("全部通过，可交付。")
