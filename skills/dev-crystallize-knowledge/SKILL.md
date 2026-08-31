---
name: dev-crystallize-knowledge
license: 'MIT'
description: 把坑/决策/用法同步固化到 AGENTS.md、设计文档与 skill，并规范收尾汇报。踩新坑、完成功能或跨会话交接时自行固化
argument-hint: "要固化的坑/决策/用法是什么？"
disable-model-invocation: false
---

# 知识固化仪式

1. **三同步**：坑/测试基建/环境 workaround → AGENTS.md（高度浓缩）；设计决策 → 设计文档 + memory（§编号）；CLI 用法/重复流程 → skill（附新增命令）。环境 workaround 必须**同一轮改动内落档**，否则必重踩
2. **每轮收尾四要素**：HEAD/工作区状态 + 本轮交付（commit）+ 未完成/待办 + 下一步建议
3. **功能完成四件套**：代码 commit（含测试）+ 文档更新（带"必要性"护栏：有必要再更新，给 grep 证据）+ skill 落档 + 待办勾选
4. **临时脚本落盘**：一次性脚本先写 `<项目根>/tmp/`（.gitignore 已忽略）再执行，可复用；正式工具才放 scripts/
5. **术语表一次性定稿**：单独一轮定稿并锁定为引用物，后续只补充新术语——防跨会话口径漂移
6. **交接**：用 dev-handoff 生成六节交接文档（含"新教训"清单 + 脱敏）
