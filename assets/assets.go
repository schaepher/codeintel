// Package assets 嵌入前端静态资源（AntV G6 依赖探索页面 + mermaid
// 渲染引擎）。
package assets

import "embed"

// WebFS 前端静态文件（web/ 目录）。
//
//go:embed web
var WebFS embed.FS

// MermaidJS mermaid 渲染引擎（Q251：wiki html 离线自包含——CDN 不可达
// 时架构图/时序图/ER 图全部无法渲染；内嵌后单文件完全离线可用）。
// 来源：cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js。
//
//go:embed mermaid.min.js
var MermaidJS string
