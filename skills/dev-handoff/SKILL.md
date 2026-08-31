---
name: dev-handoff
license: 'MIT'
description: 把当前会话总结成一份新 agent 可续作的交接文档
argument-hint: "What will the next session be used for?"
disable-model-invocation: false
---

把当前会话总结成交接文档，保存到 OS 临时目录（非 workspace），供新 agent 续作。

- **commit 范围**：读最新一份交接文档取 HEAD 为起点，`git log <起点>..HEAD --oneline` 收集本会话 commit（无上一份则从会话起点开始）
- **文档结构（六节）**：本会话交付（commit 表 + 端到端验证）/ 当前状态（HEAD、验证基线、项目状态）/ 已知遗留（按优先级）/ 工作方式约定（引用 AGENTS.md，不重复）/ Suggested Skills（必填，列出相关 skill 及新增命令）/ 敏感信息
- **文件名**：`/tmp/<项目>-handoff-<YYYYMMDD>-<字母>.md`（字母按当天已有文件递增）；/tmp 配额满时改存 `$HANDOFF_FALLBACK_DIR`（默认 `$HOME/.tmp-build/`）
- **不重复工件**：specs/设计文档/commit 已记录的内容只引用路径或 §编号，不复制正文
- **脱敏**：无 API 密钥/密码/PII；私有仓库名用「验证仓库（私有）」指代；fixture 脱敏表名勿还原
- 若用户传入参数，将其视为下一会话的聚焦点并据此调整文档
