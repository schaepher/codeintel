---
name: dev-line-limit
license: 'MIT'
description: 拆分超过 300 行的 Go 文件，并清理拆分残留的孤立/错位注释。
---

# Go 文件行数治理（≤300 行 + 孤立注释清理）

1. **检测**
   - `scripts/find-large-files.sh`：列出 >300 行 Go 文件
   - `scripts/find-misplaced.py`：孤立/错位注释判定——`DUP-DEF` 删残留、`MOVE` 搬移、`SELF-DEF` 正常保留、`NO-SAME-PKG-DEF` 误报
2. **拆分**（asttool 自带 go.mod，`cd <skill>/scripts/asttool && go run .` 或 `go run ./skills/dev-line-limit/scripts/asttool`）
   - `funcsize <file>` 看函数/方法行数分布 → `analyze <file>` 看声明清单 → `split <src.go> <out.go>:Name1,Name2` 按主题搬移
   - 拆后 `goimports -w` 清理 unused import
3. **清理注释**：`fix-comments.py --apply` 删 DUP-DEF；`--move` 搬 MOVE 到定义前（带断言：源段落全为注释/空行，误伤即中断）
4. **验证**：`scripts/verify.sh`（gofmt + build + 超行/孤立复查）+ `go test ./...`

坑：跨包同名函数注释相同是复制不是残留（勿跨包处理）；方法抽取的行数开销可能使行数不减反增——移到行数余量大的文件；脚本操作前先 dry-run 预览。
