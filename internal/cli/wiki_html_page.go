package cli

// wikiPageOpts 页面可选元素（serve 场景注入；单文件 html 全空）。
type wikiPageOpts struct {
	exploreLink string // 非空时侧栏顶部"图探索"返回链接
	freshNote   string // 非空时页面底部新鲜度标注（索引 commit）
	searchIndex string // 搜索索引 JSON（模块/表/术语；serve 版跨页搜索）
	chatPanel   bool   // W1：serve 版注入对话面板（POST /wiki/ask）
	diagram     string // R32：plantuml（默认）| mermaid——控制 mermaid CDN 引入
}

// wikiHTMLPage 组装完整页面（内嵌 CSS/JS）。guide 是快速开始引导块
// （单文件用 #锚点，wiki serve 多页用 /wiki/ 路径链接）。
func wikiHTMLPage(title, desc, guide, nav, main string, opts wikiPageOpts) string {
	return `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + htmlEsc(title) + `</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; color: #1f2329; }
#sidebar { position: fixed; left: 0; top: 0; bottom: 0; width: 280px; overflow-y: auto;
  background: #f6f8fa; border-right: 1px solid #e5e6eb; padding: 16px 0; }
#sidebar h2 { font-size: 14px; padding: 0 16px 8px; color: #86909c; }
#sidebar ul { list-style: none; }
#sidebar .mod-head { padding: 8px 16px; cursor: pointer; font-weight: 600; font-size: 13px; }
#sidebar .mod-head:hover { background: #e5e6eb; }
#sidebar .mod-sec { padding: 0 0 6px 24px; font-size: 12px; }
#sidebar .mod-sec a { display: block; padding: 3px 8px; color: #4e5969; text-decoration: none; border-radius: 4px; }
#sidebar .mod-sec a:hover { background: #e5e6eb; color: #1677ff; }
#sidebar > ul > li > a { display: block; padding: 6px 16px; font-size: 13px; color: #4e5969; text-decoration: none; }
#sidebar > ul > li > a:hover { color: #1677ff; }
#main { margin-left: 280px; padding: 24px 40px 60px; max-width: 960px; }
#main h2 { font-size: 22px; margin: 8px 0 12px; border-bottom: 1px solid #e5e6eb; padding-bottom: 8px; }
#main h3 { font-size: 15px; margin: 20px 0 8px; cursor: pointer; user-select: none; color: #1d2129; }
#main h3:hover { color: #1677ff; }
#main blockquote { margin: 8px 0 16px; padding: 8px 12px; background: #f2f3f5; border-left: 3px solid #1677ff; color: #4e5969; }
#main p { margin: 6px 0; font-size: 14px; line-height: 1.7; }
#main code { background: #f2f3f5; padding: 1px 6px; border-radius: 4px; font-size: 13px; }
#main table { border-collapse: collapse; margin: 8px 0 16px; width: 100%; font-size: 13px; }
#main th, #main td { border: 1px solid #e5e6eb; padding: 6px 10px; text-align: left; }
#main th { background: #f7f8fa; }
#main .muted { color: #86909c; }
#main section { margin-bottom: 32px; }
.fold-btn { cursor: pointer; }
.guide { margin: 12px 0 24px; padding: 10px 14px; background: #e8f3ff; border-radius: 6px; font-size: 13px; line-height: 1.8; }
.guide a { color: #1677ff; }
.nav-tools { padding: 0 16px 8px; display: flex; gap: 6px; flex-wrap: wrap; }
.nav-tools input { flex: 1; min-width: 120px; padding: 4px 8px; border: 1px solid #d0d3d9; border-radius: 4px; font-size: 12px; outline: none; }
.nav-tools input:focus { border-color: #1677ff; }
.nav-tools button { padding: 3px 8px; font-size: 11px; border: 1px solid #d0d3d9; border-radius: 4px; background: #fff; cursor: pointer; }
.nav-tools button:hover { border-color: #1677ff; color: #1677ff; }
html { scroll-behavior: smooth; }
#sidebar .mod-head.active { color: #1677ff; background: #e5e6eb; }
#sidebar .mod-sec a.active { color: #1677ff; font-weight: 600; }
#sidebar-toggle {
  position: fixed; top: 8px; left: 288px; z-index: 50;
  background: #f6f8fa; border: 1px solid #e5e6eb; border-radius: 4px;
  font-size: 12px; padding: 2px 8px; cursor: pointer; color: #4e5969;
  transition: left .2s;
}
#sidebar-toggle:hover { color: #1677ff; border-color: #1677ff; }
body.sidebar-off #sidebar-toggle { left: 8px; }
body.sidebar-off #sidebar { display: none; }
body.sidebar-off #main { margin-left: 0; }
#search-results { padding: 4px 12px; display: flex; flex-direction: column; gap: 2px; }
#search-results .sr-item { display: flex; gap: 6px; align-items: baseline; padding: 3px 8px;
  font-size: 12px; color: #4e5969; text-decoration: none; border-radius: 4px; }
#search-results .sr-item:hover { background: #e5e6eb; color: #1677ff; }
#search-results .sr-t { flex: 0 0 28px; font-size: 11px; color: #86909c; }
#search-results .sr-d { color: #86909c; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
@media (max-width: 768px) {
  #sidebar { position: static; width: auto; border-right: none; border-bottom: 1px solid #e5e6eb; max-height: 40vh; }
  #main { margin-left: 0; padding: 16px; }
}
</style>
</head>
<body>
<button id="sidebar-toggle" title="收起/展开目录"></button>
<div id="sidebar">
` + (func() string {
		if opts.exploreLink != "" {
			return `<div style="padding:0 16px 8px"><a href="` + htmlEsc(opts.exploreLink) + `" style="font-size:12px;color:#1677ff">← 图探索</a></div>`
		}
		return ""
	}()) + `<h2>目录</h2>
<div class="nav-tools">
  <input id="nav-search" type="text" placeholder="搜索模块 / 章节 / 表…" autocomplete="off">
  <button id="nav-expand-all" title="全部展开">全部展开</button>
  <button id="nav-collapse-all" title="全部收起">全部收起</button>
</div>
<ul id="nav">` + nav + `</ul>
<div id="search-results"></div>
</div>
<div id="main">
<h1>` + htmlEsc(title) + `</h1>
<div class="guide">` + guide + `</div>
` + (func() string {
		if desc != "" {
			return "<blockquote>" + htmlEsc(desc) + "</blockquote>\n"
		}
		return ""
	}()) + main + `
` + (func() string {
		if opts.freshNote != "" {
			return `<p class="muted" style="margin-top:24px;font-size:12px">` + htmlEsc(opts.freshNote) + `</p>`
		}
		return ""
	}()) + `
</div>
` + (func() string {
		if opts.searchIndex != "" {
			return "<script>var WIKI_IDX = " + opts.searchIndex + ";</script>\n"
		}
		return "<script>var WIKI_IDX = [];</script>\n"
	}()) + (func() string {
		if opts.diagram == "plantuml" {
			// R32：plantuml 模式图已渲染为 PNG——不引 mermaid CDN
			return ""
		}
		return `<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>
mermaid.initialize({ startOnLoad: true, theme: 'neutral' });`
	}()) + `
<script>
// 目录当前模块高亮（scrollspy）
// 目录当前模块高亮（scrollspy）
(function () {
  var mods = Array.prototype.slice.call(document.querySelectorAll('#main section[id^="mod-"]'));
  var navMods = Array.prototype.slice.call(document.querySelectorAll('#sidebar .mod-head'));
  var onScroll = function () {
    var pos = window.scrollY + 120;
    var cur = null;
    mods.forEach(function (sec) { if (sec.offsetTop <= pos) cur = sec; });
    navMods.forEach(function (el) { el.classList.remove('active'); });
    if (!cur) return;
    var idx = parseInt(cur.id.replace('mod-', ''), 10);
    if (navMods[idx]) navMods[idx].classList.add('active');
  };
  window.addEventListener('scroll', onScroll, { passive: true });
  onScroll();
})();
// 侧栏整体收起/展开（localStorage 持久化——阅读空间最大化）
(function () {
  var KEY = 'codeintel-wiki-sidebar';
  var btn = document.getElementById('sidebar-toggle');
  if (!btn) return;
  var off = false;
  try { off = localStorage.getItem(KEY) === '1'; } catch (e) {}
  var apply = function () {
    document.body.classList.toggle('sidebar-off', off);
    btn.textContent = off ? '\u00bb 展开目录' : '\u00ab 收起目录';
  };
  btn.addEventListener('click', function () {
    off = !off;
    try { localStorage.setItem(KEY, off ? '1' : '0'); } catch (e) {}
    apply();
  });
  apply();
})();
// D：补全引导横幅——已关闭（localStorage）则不显示
(function () {
  var b = document.getElementById('gap-banner');
  if (!b) return;
  try { if (localStorage.getItem('codeintel-wiki-gap')) b.style.display = 'none'; } catch (e) {}
})();
// 折叠交互（#246：状态 localStorage 持久化）+ 全部展开/收起 + 搜索
(function () {
  var KEY = 'codeintel-wiki-fold';
  var state = {};
  try { state = JSON.parse(localStorage.getItem(KEY) || '{}'); } catch (e) {}
  var apply = function (btn) {
    var target = document.getElementById(btn.getAttribute('data-target'));
    if (!target) return;
    var collapsed = target.style.display === 'none';
    target.style.display = collapsed ? '' : 'none';
    if (btn.getAttribute('data-label') === '1') {
      btn.textContent = (collapsed ? '▾ ' : '▸ ') + btn.textContent.replace(/^[▾▸] /, '');
    }
  };
  document.addEventListener('click', function (ev) {
    var btn = ev.target.closest('.fold-btn');
    if (!btn) return;
    var id = btn.getAttribute('data-target');
    var target = document.getElementById(id);
    if (!target) return;
    apply(btn);
    state[id] = target.style.display === 'none';
    try { localStorage.setItem(KEY, JSON.stringify(state)); } catch (e) {}
  });
  document.querySelectorAll('.fold-btn').forEach(function (btn) {
    var id = btn.getAttribute('data-target');
    var target = document.getElementById(id);
    if (target && state[id]) { target.style.display = 'none'; apply(btn); }
  });
  document.getElementById('nav-expand-all').addEventListener('click', function () {
    document.querySelectorAll('.fold-btn').forEach(function (btn) {
      var target = document.getElementById(btn.getAttribute('data-target'));
      if (target && target.style.display === 'none') apply(btn);
    });
  });
  document.getElementById('nav-collapse-all').addEventListener('click', function () {
    document.querySelectorAll('.fold-btn').forEach(function (btn) {
      var target = document.getElementById(btn.getAttribute('data-target'));
      if (target && target.style.display !== 'none') apply(btn);
    });
  });
  document.getElementById('nav-search').addEventListener('input', function () {
    var q = this.value.trim().toLowerCase();
    document.querySelectorAll('#nav .mod, #nav > li').forEach(function (li) {
      var ok = !q || li.textContent.toLowerCase().indexOf(q) >= 0;
      li.style.display = ok ? '' : 'none';
      if (ok && q) {
        var sec = li.querySelector('.mod-sec');
        if (sec && sec.style.display === 'none') {
          var btn = li.querySelector('.fold-btn');
          if (btn) apply(btn);
        }
      }
    });
    // C：跨页搜索索引（模块/表/术语；空 = 单文件 html 不启用）
    var box = document.getElementById('search-results');
    if (!box) return;
    var idx = typeof WIKI_IDX !== 'undefined' ? WIKI_IDX : [];
    box.innerHTML = '';
    if (!q || !idx.length) return;
    var hits = [];
    idx.forEach(function (it) {
      if ((it.n + ' ' + (it.d || '')).toLowerCase().indexOf(q) >= 0) hits.push(it);
    });
    hits.slice(0, 20).forEach(function (it) {
      var a = document.createElement('a');
      a.href = it.h;
      a.className = 'sr-item';
      a.innerHTML = '<span class="sr-t">' + it.t + '</span>' + (it.n.replace(/(<)/g, '&lt;')) +
        (it.d ? '<span class="sr-d">' + (it.d.slice(0, 40).replace(/(<)/g, '&lt;')) + '</span>' : '');
      box.appendChild(a);
    });
  });
})();
</script>
` + chatPanelHTML(opts.chatPanel) + `</body>
</html>
`
}
