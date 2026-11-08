//go:build integration

package integration

// todo35 前端夹具（公开示例 + B09e 反例目录）。

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// u35WriteFrontend B09e 公开示例 + 反例目录。
func u35WriteFrontend(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "default-frontend", "actions.ts"),
		`export default function send(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}
`)
	writeFile(t, filepath.Join(dir, "default-frontend", "view.tsx"),
		`import send from './actions';
export function Screen({id}: {id: string}) {return <button onClick={() => send(id)}>Submit</button>;}
`)
	writeFile(t, filepath.Join(dir, "alias-frontend", "a.ts"),
		`export function request(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}
export function click(id: string) {return request(id);}
`)
	writeFile(t, filepath.Join(dir, "alias-frontend", "view.tsx"),
		`import {click as submitClick} from './a';
export function Screen({id}: {id: string}) {return <button onClick={() => submitClick(id)}>Submit</button>;}
`)
	writeFile(t, filepath.Join(dir, "alias-frontend", "direct.tsx"),
		`import {click} from './a';
export function Direct({id}: {id: string}) {return <button onClick={() => click(id)}>Submit</button>;}
`)
	writeFile(t, filepath.Join(dir, "deep-frontend", "actions.ts"), u35FrontendDeepActions)
	writeFile(t, filepath.Join(dir, "deep-frontend", "view.tsx"), u35FrontendDeepView)
	writeFile(t, filepath.Join(dir, "field-frontend", "actions.ts"), u35FrontendFieldActions)
	writeFile(t, filepath.Join(dir, "field-frontend", "view.tsx"), u35FrontendFieldView)
	writeFile(t, filepath.Join(dir, "destructure-frontend", "actions.ts"), u35FrontendDestructureActions)
	writeFile(t, filepath.Join(dir, "destructure-frontend", "view.tsx"), u35FrontendDestructureView)
	// 递归包装：必须保留动作 + 明确 unknown。
	writeFile(t, filepath.Join(dir, "cycle-frontend", "actions.ts"),
		`export function loop(id: string){return loop(id);}
`)
	writeFile(t, filepath.Join(dir, "cycle-frontend", "view.tsx"),
		`import {loop} from './actions';
export function Screen({id}: {id: string}){return <button onClick={() => loop(id)}>Submit</button>;}
`)
	// 超过共享声明预算（16 层）。
	var budget strings.Builder
	budget.WriteString(`export function request(id: string){return fetch('/requests', {method: 'POST',body: JSON.stringify({id})});}
`)
	for i := 20; i >= 1; i-- {
		if i == 20 {
			fmt.Fprintf(&budget, "export function w%d(id: string){return request(id);}\n", i)
		} else {
			fmt.Fprintf(&budget, "export function w%d(id: string){return w%d(id);}\n", i, i+1)
		}
	}
	budget.WriteString("export function click(id: string){return w1(id);}\n")
	writeFile(t, filepath.Join(dir, "budget-frontend", "actions.ts"), budget.String())
	writeFile(t, filepath.Join(dir, "budget-frontend", "view.tsx"),
		`import {click} from './actions';
export function Screen({id}: {id: string}){return <button onClick={() => click(id)}>Submit</button>;}
`)
	// 字段反例：另一对象字段 / 字段被覆盖 / 动态下标。
	writeFile(t, filepath.Join(dir, "other-field-frontend", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){const other={id:'x'};return fetch('/requests', {method: 'POST',body: JSON.stringify({id:other.id})});}
`)
	writeFile(t, filepath.Join(dir, "other-field-frontend", "view.tsx"), u35FrontendFieldView)
	writeFile(t, filepath.Join(dir, "mutated-field-frontend", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){req.id='x';return fetch('/requests', {method: 'POST',body: JSON.stringify({id:req.id})});}
`)
	writeFile(t, filepath.Join(dir, "mutated-field-frontend", "view.tsx"), u35FrontendFieldView)
	writeFile(t, filepath.Join(dir, "index-field-frontend", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method: 'POST',body: JSON.stringify({id:req[0]})});}
`)
	writeFile(t, filepath.Join(dir, "index-field-frontend", "view.tsx"), u35FrontendFieldView)
	// 解构别名 / 嵌套字段（正例）。
	writeFile(t, filepath.Join(dir, "alias-field-frontend", "actions.ts"),
		`export type Input = {id: string};
export function send({id: alias}: Input){return fetch('/requests', {method: 'POST',body: JSON.stringify({id:alias})});}
`)
	writeFile(t, filepath.Join(dir, "alias-field-frontend", "view.tsx"), u35FrontendFieldView)
	writeFile(t, filepath.Join(dir, "nested-field-frontend", "actions.ts"),
		`export type Input = {user: {id: string}};
export function send(req: Input){return fetch('/requests', {method: 'POST',body: JSON.stringify({id:req.user.id})});}
`)
	writeFile(t, filepath.Join(dir, "nested-field-frontend", "view.tsx"), u35FrontendFieldView)
	// 同名不同文件：导入哪个文件就只解析哪个。
	writeFile(t, filepath.Join(dir, "same-name", "a.ts"),
		`export function send(id: string) {return fetch('/alternate', {method: 'POST', body: JSON.stringify({id})});}
`)
	writeFile(t, filepath.Join(dir, "same-name", "b.ts"),
		`export function send(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}
`)
	writeFile(t, filepath.Join(dir, "same-name", "view.tsx"),
		`import {send} from './a';
export function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}
`)
}
