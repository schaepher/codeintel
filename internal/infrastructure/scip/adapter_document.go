package scip

// SCIP document → 图产物（节点 + IMPLEMENTS 边）与路径归一。

import (
	"github.com/schaepher/codeintel/internal/canonicalizer"
	"github.com/schaepher/codeintel/internal/domain"
	"github.com/scip-code/scip/bindings/go/scip"
	"go.uber.org/zap"
	"path"
	"path/filepath"
	"strings"
)

// processDocument 处理单个 SCIP document：符号节点（含定义行范围）+ IMPLEMENTS 边。
func (a *Adapter) processDocument(repo *domain.Repository, doc *scip.Document, moduleRel string, emit domain.EmitFunc) error {
	logger := zap.L()
	logger.Debug("enter (Adapter).processDocument")
	defer logger.Debug("exit (Adapter).processDocument")
	filePath := repoRelPath(moduleRel, doc.RelativePath)
	if domain.IsIgnoredPath(filePath, repo.IgnoreDirs) {
		return nil // 忽略目录（Repository.IgnoreDirs）：不入图
	}

	// FILE 节点：ID 为 file:<relpath>
	if err := emit(domain.Item{Node: &domain.CodeEntity{
		ID:       domain.CanonicalID("file:" + filePath),
		Kind:     domain.KindFile,
		Name:     filepath.Base(filePath),
		FilePath: filePath,
	}}); err != nil {
		return err
	}

	// pass 1: 符号 → 节点信息
	type symInfo struct {
		node *domain.CodeEntity
		occ  *scip.Occurrence
	}
	defs := map[string]*symInfo{} // SCIP symbol 字符串 → 节点信息

	for _, sym := range doc.Symbols {
		gs, err := canonicalizer.FromScipSymbol(sym.Symbol)
		if err != nil {
			continue // local / unsupported 符号跳过
		}
		if gs.IsInterfaceMethod {
			continue // 接口方法不作为独立节点（implements 只连接口类型→实现者）
		}
		kind, ok := canonicalizer.ScipKindToDomainKind(sym.Kind)
		if !ok {
			continue // 变量/常量/字段等不建节点
		}
		props := map[string]any{}
		// 注：scip-go (scip v0.7.1) 不输出 signature 字段，签名由 AST 适配器补充
		if len(sym.Documentation) > 0 {
			props["doc_comment"] = strings.Join(sym.Documentation, "\n")
		}
		defs[sym.Symbol] = &symInfo{node: &domain.CodeEntity{
			ID:         canonicalizer.GoSymbolID(gs.ImportPath, gs.Name),
			Kind:       kind,
			Name:       gs.Name,
			FilePath:   filePath,
			Properties: props,
		}}
	}

	// pass 2: 定义 occurrence → 行范围
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&int32(scip.SymbolRole_Definition) == 0 {
			continue
		}
		if si, ok := defs[occ.Symbol]; ok {
			si.occ = occ
		}
	}

	// 发出节点（带行范围；scip-go 的 range 为单行 [start_line, start_char, end_char]）
	for _, si := range defs {
		if si.occ != nil && len(si.occ.Range) > 0 {
			si.node.LineStart = int(si.occ.Range[0]) + 1
			si.node.LineEnd = si.node.LineStart
		}
		if err := emit(domain.Item{Node: si.node}); err != nil {
			return err
		}
	}

	// pass 3: IMPLEMENTS 边（is_implementation relationship）——方向：
	// 接口 → 实现者（用户确认：接口要指向实现，而非实现指向接口）
	for _, sym := range doc.Symbols {
		if len(sym.Relationships) == 0 {
			continue
		}
		src, err := canonicalizer.FromScipSymbol(sym.Symbol)
		if err != nil {
			continue
		}
		if src.IsInterfaceMethod {
			continue // 接口方法不建 implements 边（只连接口类型→实现者）
		}
		implID := canonicalizer.GoSymbolID(src.ImportPath, src.Name) // 实现者
		for _, rel := range sym.Relationships {
			if !rel.IsImplementation {
				continue
			}
			iface, err := canonicalizer.FromScipSymbol(rel.Symbol)
			if err != nil {
				continue
			}
			if iface.IsInterfaceMethod {
				continue // 方法级 implements（接口方法→实现方法）不建
			}
			if !isInModule(iface.ImportPath, repo.Modules) {
				continue // 外部接口无节点（外键约束）
			}
			if err := emit(domain.Item{Fact: &domain.Fact{
				SourceID:   canonicalizer.GoSymbolID(iface.ImportPath, iface.Name), // 接口
				TargetID:   implID,                                                 // 实现者
				Kind:       domain.FactImplements,
				ToolSource: domain.ToolSCIP,
				Confidence: 1.0,
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// isInModule 判断 importPath 是否属于任一被索引 module（自身或子包；
// P2-3 多 go.mod——任一 module 前缀匹配即项目内）。
func isInModule(importPath string, modules []string) bool {
	logger := zap.L()
	logger.Debug("enter isInModule")
	defer logger.Debug("exit isInModule")
	for _, m := range modules {
		if m == "" {
			continue
		}
		if importPath == m || strings.HasPrefix(importPath, m+"/") {
			return true
		}
	}
	return false
}

// repoRelPath 把"模块相对路径"归一到"仓库相对路径"（Q251）。
// moduleRel 为 "." 表示根 module（原样返回）；空 docRel 返回空。
func repoRelPath(moduleRel, docRel string) string {
	if docRel == "" {
		return ""
	}
	rel := filepath.ToSlash(docRel)
	if moduleRel == "" || moduleRel == "." {
		return rel
	}
	return path.Join(filepath.ToSlash(moduleRel), rel)
}
