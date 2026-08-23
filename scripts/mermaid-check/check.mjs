// mermaid 语法验证（Q251 补）：提取 html/md 的 mermaid 块，用真实
// mermaid 解析器（jsdom 提供 DOM）逐个 parse。用法：
//   node check.mjs <index.html|*.md> [--md]
// 退出码：0 = 全部通过；1 = 有语法错误
import fs from 'fs';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!DOCTYPE html><html><body></body></html>');
global.window = dom.window;
global.document = dom.window.document;
Object.defineProperty(global, 'navigator', { value: dom.window.navigator, configurable: true });
global.SVGElement = dom.window.SVGElement;
global.HTMLElement = dom.window.HTMLElement;
global.Element = dom.window.Element;

const { default: mermaid } = await import('mermaid');
mermaid.initialize({ startOnLoad: false });

const file = process.argv[2];
const isMd = process.argv.includes('--md');
const text = fs.readFileSync(file, 'utf8');
const blocks = isMd
  ? [...text.matchAll(/```mermaid\n([\s\S]*?)```/g)].map(m => m[1])
  : [...text.matchAll(/<pre class="mermaid">([\s\S]*?)<\/pre>/g)].map(m => m[1]);
// HTML 实体还原（浏览器 DOM textContent 已还原——&gt; → >）
const unescape = s => s.replace(/&lt;/g, '<').replace(/&gt;/g, '>')
  .replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&amp;/g, '&');
for (let i = 0; i < blocks.length; i++) blocks[i] = unescape(blocks[i]);

let fail = 0;
for (let i = 0; i < blocks.length; i++) {
  const b = blocks[i].trim();
  if (!b) continue;
  try {
    await mermaid.parse(b);
    console.log(`  ✓ [${i}] ${b.split('\n')[0].slice(0, 60)}`);
  } catch (e) {
    fail++;
    const msg = (e.message || String(e)).split('\n').slice(0, 3).join(' | ').slice(0, 200);
    console.log(`  ✗ [${i}] ${b.split('\n')[0].slice(0, 60)}`);
    console.log(`      ${msg}`);
  }
}
console.log(fail === 0 ? `全部通过（${blocks.length} 块）` : `${fail}/${blocks.length} 块语法错误`);
process.exit(fail === 0 ? 0 : 1);
