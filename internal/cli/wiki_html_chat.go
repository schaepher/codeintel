package cli

// W1 对话面板（serve wiki 版）：固定右侧可折叠 chat——浏览时随时
// 追问（POST /wiki/ask）。独立文件（行数治理）。

// chatPanelHTML W1 对话面板（serve 版）：固定右侧可折叠——浏览 wiki
// 时随时追问项目问题（POST /wiki/ask，回答收集进 qa_history 作
// wiki --with-qa 参考资料）。非 serve 版（单文件 html）返回空串。
func chatPanelHTML(enabled bool) string {
	if !enabled {
		return ""
	}
	return `<style>
#chat-toggle { position: fixed; right: 16px; bottom: 16px; z-index: 60;
  background: #1677ff; color: #fff; border: none; border-radius: 20px;
  padding: 8px 16px; font-size: 13px; cursor: pointer; box-shadow: 0 2px 8px rgba(0,0,0,.15); }
#chat-panel { position: fixed; right: 16px; bottom: 56px; z-index: 60; width: 360px;
  max-height: 60vh; display: flex; flex-direction: column;
  background: #fff; border: 1px solid #e5e6eb; border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0,0,0,.12); overflow: hidden; }
#chat-panel.collapsed { display: none; }
#chat-panel .ch-head { padding: 8px 12px; font-size: 13px; font-weight: 600;
  border-bottom: 1px solid #e5e6eb; display: flex; justify-content: space-between; }
#chat-panel .ch-body { flex: 1; overflow-y: auto; padding: 10px 12px; font-size: 13px; line-height: 1.7;
  display: flex; flex-direction: column; gap: 8px; max-height: 45vh; }
#chat-panel .ch-msg { padding: 6px 10px; border-radius: 6px; white-space: pre-wrap; word-break: break-word; }
#chat-panel .ch-q { background: #e8f3ff; align-self: flex-end; }
#chat-panel .ch-a { background: #f2f3f5; align-self: flex-start; }
#chat-panel .ch-input { display: flex; gap: 6px; padding: 8px; border-top: 1px solid #e5e6eb; }
#chat-panel .ch-input input { flex: 1; padding: 5px 8px; border: 1px solid #d0d3d9; border-radius: 4px; font-size: 12px; outline: none; }
#chat-panel .ch-input button { padding: 4px 12px; background: #1677ff; color: #fff;
  border: none; border-radius: 4px; font-size: 12px; cursor: pointer; }
#chat-panel .ch-input button:disabled { background: #a0c4ff; }
</style>
<div id="chat-panel" class="collapsed">
  <div class="ch-head"><span>AI 助手（对话将收集为 wiki 参考资料）</span><span id="chat-close" style="cursor:pointer">✕</span></div>
  <div class="ch-body" id="chat-body"></div>
  <div class="ch-input">
    <input id="chat-q" placeholder="深入问项目问题（如：cmdWiki 的入口在哪）" onkeydown="if(event.key==='Enter')chatAsk()">
    <button id="chat-send" onclick="chatAsk()">发送</button>
  </div>
</div>
<button id="chat-toggle" onclick="document.getElementById('chat-panel').classList.toggle('collapsed')">💬 问 AI</button>
<script>
function chatAsk() {
  var q = document.getElementById('chat-q').value.trim();
  if (!q) return;
  var body = document.getElementById('chat-body');
  var mk = function (cls, txt) {
    var d = document.createElement('div');
    d.className = 'ch-msg ' + cls;
    d.textContent = txt;
    body.appendChild(d);
    body.scrollTop = body.scrollHeight;
    return d;
  };
  mk('ch-q', q);
  document.getElementById('chat-q').value = '';
  var send = document.getElementById('chat-send');
  send.disabled = true;
  mk('ch-a', '思考中…');
  fetch('/wiki/ask', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ question: q }) })
    .then(function (r) { return r.json(); })
    .then(function (d) {
      body.removeChild(body.lastChild);
      mk('ch-a', d.answer || ('错误: ' + (d.error || '未知')));
    })
    .catch(function (e) { body.removeChild(body.lastChild); mk('ch-a', '请求失败: ' + e); })
    .finally(function () { send.disabled = false; });
}
document.getElementById('chat-close').addEventListener('click', function () {
  document.getElementById('chat-panel').classList.add('collapsed');
});
</script>
`
}
