package cli

// P2b wiki 网页版：集成进 `codeintel serve`（/wiki/ 前缀路由）——多页
// 浏览（overview / 每模块一页 / ER 图 / 表清单）+ 左侧目录导航（复用
// 单文件 html 的折叠/搜索/持久化 JS）。请求时从索引 db 内存渲染
// （永不 stale）；数据快照按 build_id + wiki.yaml mtime 失效——增量
// update 后内容自动跟上。wiki.yaml 自动加载（仓库根，存在即用）。
// 页面渲染在 wiki_serve_pages.go（按主题拆）。

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/schaepher/codeintel/internal/action"
	"github.com/schaepher/codeintel/internal/domain"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// wikiServe 持有一个仓库的 wiki 数据快照缓存。
type wikiServe struct {
	repoAbs string
	acts    *action.Actions
	mu      sync.Mutex
	snap    *wikiSnapshot
}

// wikiSnapshot 一次有效数据快照（渲染入参，与 renderWikiHTML 同源）。
type wikiSnapshot struct {
	buildID    string
	commitSHA  string
	yamlMod    int64
	data       []*domain.WikiModule
	ordered    []*domain.WikiModule
	cfg        wikiConfig
	meta       map[string]wikiMeta
	tableAlias map[string]string
	hidden     map[string]bool
	tableCfgs  map[string]wikiTableConfig
	cols       []*domain.TableColumn
	rels       []*domain.TableRelation
}

// wikiServeHandler 生成 /wiki/ 前缀 handler（serve 注入点）。
func wikiServeHandler(repoAbs string, acts *action.Actions) http.Handler {
	ws := &wikiServe{repoAbs: repoAbs, acts: acts}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/wiki"), "/")
		if path == "" {
			http.Redirect(w, r, "/wiki/overview", http.StatusFound)
			return
		}
		snap, err := ws.snapshot()
		if err != nil {
			http.Error(w, "wiki 数据加载失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		switch {
		case path == "overview":
			serveWikiHTML(w, ws.overviewPage(snap))
		case strings.HasPrefix(path, "mod/"):
			name := strings.TrimPrefix(path, "mod/")
			for _, wm := range snap.ordered {
				if wm.ShortName == name {
					serveWikiHTML(w, ws.modulePage(snap, wm))
					return
				}
			}
			http.NotFound(w, r)
		case path == "er":
			// E：模块过滤（?mod=<短名>）——只看某模块相关表的关系
			serveWikiHTML(w, ws.erPage(snap, r.URL.Query().Get("mod")))
		case path == "tables":
			serveWikiHTML(w, ws.tablesPage(snap))
		case path == "commands":
			serveWikiHTML(w, ws.pageHTML(snap, "/wiki/commands", renderCommandsHTML()))
		case path == "api":
			serveWikiHTML(w, ws.pageHTML(snap, "/wiki/api", renderAPIHTML(ws.repoAbs)))
		default:
			http.NotFound(w, r)
		}
	})
}

func serveWikiHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// snapshot 取快照（缓存命中返回；build_id/yaml mtime 变化时重载）。
// Latest 失败（无 build_metadata 的轻量测试库）时降级空 buildID——
// 数据收集本身不依赖它，仅作缓存失效信号。
func (ws *wikiServe) snapshot() (*wikiSnapshot, error) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	buildID, commitSHA := "", ""
	if latest, err := ws.acts.Latest(); err == nil {
		buildID, commitSHA = latest.BuildID, latest.CommitSHA
	}
	yamlMod := wikiYAMLModTime(ws.repoAbs)
	if ws.snap != nil && ws.snap.buildID == buildID && ws.snap.yamlMod == yamlMod {
		return ws.snap, nil
	}
	snap, err := ws.load(buildID, commitSHA, yamlMod)
	if err != nil {
		return nil, err
	}
	ws.snap = snap
	return snap, nil
}

// load 收集全部 wiki 数据（与 cmdWiki 同源，yaml 自动发现）。
func (ws *wikiServe) load(buildID, commitSHA string, yamlMod int64) (*wikiSnapshot, error) {
	logger := zap.L()
	logger.Debug("load wiki snapshot", zap.String("build_id", buildID))
	repo, err := buildRepo(ws.repoAbs)
	if err != nil {
		return nil, err
	}
	data, err := ws.acts.WikiData(repo.Modules)
	if err != nil {
		return nil, err
	}
	cfg, err := wikiYAMLConfig(ws.repoAbs, yamlMod)
	if err != nil {
		return nil, err
	}
	data = filterWikiModules(data, cfg)
	cols, err := ws.acts.GetAllTableColumns()
	if err != nil {
		return nil, err
	}
	rels, err := wikiRelations(ws.acts)
	if err != nil {
		return nil, err
	}
	meta, tableAlias, hidden := wikiMetaIndex(cfg)
	applyHideTable(data, cfg)
	ordered := append([]*domain.WikiModule(nil), data...)
	sort.SliceStable(ordered, func(i, j int) bool {
		oi, oj := meta[ordered[i].Name].order, meta[ordered[j].Name].order
		if oi != oj {
			return oi != 0 && (oj == 0 || oi < oj)
		}
		return ordered[i].Name < ordered[j].Name
	})
	return &wikiSnapshot{
		buildID: buildID, commitSHA: commitSHA, yamlMod: yamlMod, data: data, ordered: ordered,
		cfg: cfg, meta: meta, tableAlias: tableAlias, hidden: hidden,
		tableCfgs: tableCfgsFrom(cfg), cols: cols, rels: rels,
	}, nil
}

// wikiYAMLModTime 仓库根 wiki.yaml mtime（-1 = 不存在）。
func wikiYAMLModTime(repoAbs string) int64 {
	fi, err := os.Stat(filepath.Join(repoAbs, "wiki.yaml"))
	if err != nil {
		return -1
	}
	return fi.ModTime().Unix()
}

// wikiYAMLConfig 加载仓库根 wiki.yaml（yamlMod >= 0 表示存在）。
func wikiYAMLConfig(repoAbs string, yamlMod int64) (wikiConfig, error) {
	cfg := wikiConfig{}
	if yamlMod < 0 {
		return cfg, nil
	}
	b, err := os.ReadFile(filepath.Join(repoAbs, "wiki.yaml"))
	if err != nil {
		return cfg, nil
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 wiki.yaml: %v", err)
	}
	return cfg, nil
}

// filterWikiModules yaml 模块白名单：列出则只保留这些模块。
func filterWikiModules(data []*domain.WikiModule, cfg wikiConfig) []*domain.WikiModule {
	if len(cfg.Modules) == 0 {
		return data
	}
	want := map[string]bool{}
	for _, m := range cfg.Modules {
		want[m.Name] = true
	}
	var filtered []*domain.WikiModule
	for _, wm := range data {
		if want[wm.Name] {
			filtered = append(filtered, wm)
		}
	}
	return filtered
}

// applyHideTable yaml 隐藏表：从模块相关表移除。
func applyHideTable(data []*domain.WikiModule, cfg wikiConfig) {
	hide := map[string]bool{}
	for _, t := range cfg.Tables {
		if t.Hidden {
			hide[t.Name] = true
		}
	}
	if len(hide) == 0 {
		return
	}
	for _, wm := range data {
		var kept []string
		for _, t := range wm.Tables {
			if !hide[t] {
				kept = append(kept, t)
			}
		}
		wm.Tables = kept
	}
}
