package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
)

// renderWiki 生成 index.md + 模块页 + tables.md + er.md + commands.md +
// api.md（全量覆盖）。
func renderWiki(repoAbs, outDir string, rc *wikiRenderCtx) error {
	acts, data, cfg, cols, rels, freshNote, pkgs, degradeStats := rc.acts, rc.data, rc.cfg, rc.cols, rc.rels, rc.freshNote, rc.pkgs, rc.degradeStats
	logger := zap.L()
	logger.Debug("enter renderWiki", zap.Int("modules", len(data)))
	defer logger.Debug("exit renderWiki")
	eg, egErr := acts.Entities() // R9：实体协作图（概览/模块页渲染）
	_ = egErr
	schemas := wikiSchemas(acts) // R19 表 schema 事实源（列类型/默认值）
	ormStructs := scanORMStructs(repoAbs) // R20 表关联结构体
	goTypes := ormColTypes(ormStructs) // R21 结构体 Go 类型 fallback

	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	meta, tableAlias, hidden := wikiMetaIndex(cfg)
	tableCfgs := tableCfgsFrom(cfg)

	hideTable := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			hideTable[t.Name] = true
		}
	}
	if len(hideTable) > 0 {
		for _, wm := range data {
			var kept []string
			for _, t := range wm.Tables {
				if !hideTable[t] {
					kept = append(kept, t)
				}
			}
			wm.Tables = kept
		}
	}

	ordered := append([]*domain.WikiModule(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := meta[ordered[i].Name].order, meta[ordered[j].Name].order
		if oi != oj {
			return oi != 0 && (oj == 0 || oi < oj)
		}
		return ordered[i].Name < ordered[j].Name
	})
	var idx strings.Builder
	idx.WriteString("# " + filepath.Base(repoAbs) + " 业务 wiki\n\n")
	if cfg.Project.Description != "" {
		idx.WriteString(cfg.Project.Description + "\n\n")
	}
	// R14 布局重排（新人认知路径）：架构图 → 实体协作 → 核心业务流程
	// → 命令 → 系统流程 → 枚举/术语 → 模块 → 数据/实现细节
	idx.WriteString("**快速开始**：① 看[架构图](#整体架构图)了解系统组成 → ② 看[实体协作](#实体协作对象设计视角)了解对象怎么协作 → ③ 看[命令清单](commands.md)上手 → ④ 深入[模块](index.md#模块)与[表](tables.md)。\n\n")
	curated := archMermaidCurated(data)
	if cfg.Architecture != "" {
		idx.WriteString("## 整体架构图\n\n> 来源：wiki.yaml architecture\n\n```mermaid\n" + cfg.Architecture + "\n```\n\n")
	} else if arch := archMermaidFallback(data); arch != "" {
		idx.WriteString("## 整体架构图\n\n> 自动生成：包间调用聚合（yaml architecture 可覆盖）\n\n```mermaid\n" + arch + "\n```\n\n")
	}
	// R7：AI 整理架构图（过滤 logging/seed 等基础包 + 分层分组）
	if curated != "" {
		idx.WriteString("## 架构图（AI 整理）\n\n> 过滤基础工具包（logging 等）+ 临时包（seed），分层分组\n> （入口/核心/支撑）——快速建立分层心智模型。\n\n```mermaid\n" + curated + "\n```\n\n")
	}
	// R9：实体协作区块（对象设计视角——比数据层更先建立设计心智）
	if sec := renderEntitiesSectionMD(eg); sec != "" {
		idx.WriteString(sec)
	}
	// R14：核心业务流程图（yaml flows 手写，AI 只排位不生成内容）
	if sec := renderBusinessFlowsSectionMD(cfg); sec != "" {
		idx.WriteString(sec)
	}
	idx.WriteString("由 `codeintel wiki` 生成（全量覆盖；业务描述/别名维护在 wiki.yaml）\n\n")
	idx.WriteString("\n## 命令与接口\n\n")
	idx.WriteString("- [命令清单](commands.md)\n")
	idx.WriteString("- [HTTP 接口](api.md)\n")
	idx.WriteString("- [系统流程](processes.md)\n")
	if len(cfg.Glossary) > 0 {
		idx.WriteString("\n## 术语表\n\n")
		for _, g := range cfg.Glossary {
			idx.WriteString(fmt.Sprintf("- **%s**：%s\n", g.Term, g.Definition))
		}
	}
	idx.WriteString("\n## 模块\n\n")
	for _, wm := range ordered {
		idx.WriteString(fmt.Sprintf("- [%s](%s.md)", wm.Name, wm.ShortName))
		if d := meta[wm.Name].desc; d != "" {
			idx.WriteString(" — " + d)
		}
		idx.WriteString("\n")
	}
	idx.WriteString("\n## 数据与实现细节\n\n")
	idx.WriteString("- [ER 图（表间关系）](er.md)\n")
	idx.WriteString("- [表清单](tables.md)\n")
	if len(pkgs) > 0 {
		idx.WriteString("\n" + renderPackagesMD(pkgs))
	}
	if degradeStats != "" {
		idx.WriteString("> 构建 SQL 解析降级统计：" + degradeStats + "（AST 降级率异常高时检查解析器）\n\n")
	}
	for _, wm := range data {
		keyFlows := wikiModuleKeyFlows(acts, wm) // R17 关键数据流
		page := renderModulePage(wm, eg, keyFlows, meta[wm.Name].desc, tableAlias, hidden, cfg)
		if err := os.WriteFile(filepath.Join(outDir, wm.ShortName+".md"), []byte(page), 0o644); err != nil {
			return err
		}
	}

	idx.WriteString("\n---\n\n由 codeintel wiki 生成 · 重新生成前请确认 wiki.yaml")
	if freshNote != "" {
		idx.WriteString("\n（" + freshNote + "）\n")
	} else {
		idx.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(outDir, "index.md"), []byte(idx.String()), 0o644); err != nil {
		return err
	}
	tables := renderTablesPage(data, tableAlias, tableCfgs, cols, schemas, ormStructs, goTypes)
	if err := os.WriteFile(filepath.Join(outDir, "tables.md"), []byte(tables), 0o644); err != nil {
		return err
	}

	er := renderERPage(rels, hideTable)
	if err := os.WriteFile(filepath.Join(outDir, "er.md"), []byte(er), 0o644); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(outDir, "commands.md"), []byte(renderCommandsMD()), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "api.md"), []byte(renderAPIMD(repoAbs)), 0o644); err != nil {
		return err
	}
	// R2：系统流程页（进程视角）
	if err := os.WriteFile(filepath.Join(outDir, "processes.md"), []byte(renderProcessesMD(acts)), 0o644); err != nil {
		return err
	}
	// R5：枚举与工具函数（源码事实——AI 权威值来源）
	return os.WriteFile(filepath.Join(outDir, "enums.md"), []byte(renderEnumsMD(repoAbs)), 0o644)
}
