//go:build integration

package integration

// todo37 共用公开示例（调用方解构别名经包装函数）+ 反例夹具。

import (
	"path/filepath"
	"testing"
)

const u37FlowSrc = `package remaining
import("context";"database/sql";"net/http")
var DB *sql.DB
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){var kind string;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return};if kind=="profile"{Complete(id)}}
func SubmitHTTP(w http.ResponseWriter,r *http.Request){Submit(r.Context(),r.URL.Query().Get("id"))}
func ReviewHTTP(w http.ResponseWriter,r *http.Request){Review(r.Context(),r.URL.Query().Get("id"))}
func Register(){http.HandleFunc("/requests",SubmitHTTP);http.HandleFunc("/reviews/finish",ReviewHTTP)}
`

// u37Repo 建 todo37 公开示例 + 反例夹具（一次 init）。
func u37Repo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/remaining

go 1.26.2
`)
	writeFile(t, filepath.Join(dir, "flow.go"), u37FlowSrc)
	u37WriteFrontend(t, dir)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u37WriteFrontend B09e.4 调用方解构公开示例 + 反例目录。
func u37WriteFrontend(t *testing.T, dir string) {
	t.Helper()
	// 公开示例：解构别名（click）、普通字段（clickPlain）、调用前改写（clickMutated）。
	writeFile(t, filepath.Join(dir, "caller-destructure", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function click(ignored: string, {profile: selected}: Envelope){return send(selected);}
export function clickPlain(ignored: string, req: Envelope){return send(req.profile);}
export function clickMutated(ignored: string, {profile: selected}: Envelope){selected.id='changed';return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure", "view.tsx"),
		`import {click, clickPlain, clickMutated, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <><button onClick={() => click(id,req)}>Submit</button><button onClick={() => clickPlain(id,req)}>Plain</button><button onClick={() => clickMutated(id,req)}>Changed</button></>;}
`)
	// 交换形参：解构声明在第 0 位，来源必须随真实声明变化。
	writeFile(t, filepath.Join(dir, "caller-destructure-swapped", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function click({profile: selected}: Envelope, ignored: string){return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-swapped", "view.tsx"),
		`import {click, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => click(req,id)}>Submit</button>;}
`)
	// 局部同名对象遮蔽解构别名。
	writeFile(t, filepath.Join(dir, "caller-destructure-shadow", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickShadow(ignored: string, {profile: selected}: Envelope){const selected={id:'x'};return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-shadow", "view.tsx"),
		`import {clickShadow, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickShadow(id,req)}>Submit</button>;}
`)
	// 局部同名/另一个对象（没有解构声明）。
	writeFile(t, filepath.Join(dir, "caller-destructure-local", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickLocal(ignored: string, {profile: selected}: Envelope){const other={id:'x'};return send(other);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-local", "view.tsx"),
		`import {clickLocal, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickLocal(id,req)}>Submit</button>;}
`)
	// 跨文件同名变量：selected 来自导入，不是解构声明。
	writeFile(t, filepath.Join(dir, "caller-destructure-crossfile", "a.ts"),
		`export type Input = {id: string};
export const selected = {id:'x'};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-crossfile", "b.ts"),
		`import {send, selected, type Input} from './a';
export type Envelope = {profile: Input};
export function clickCross(ignored: string, req: Envelope){return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-crossfile", "view.tsx"),
		`import {clickCross} from './b';
export function Screen({id, req}: {id:string,req:{profile:{id:string}}}){return <button onClick={() => clickCross(id,req)}>Submit</button>;}
`)
	// 嵌套解构 + 多层包装：路径逐次合并。
	writeFile(t, filepath.Join(dir, "caller-destructure-nested", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {outer: {profile: Input}};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function mid(req: Input){return send(req);}
export function clickNested(ignored: string, {outer: {profile: selected}}: Envelope){return mid(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-nested", "view.tsx"),
		`import {clickNested, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickNested(id,req)}>Submit</button>;}
`)
	// 传另一子对象：路径必须随实参改变（不能重复/沿用 profile）。
	writeFile(t, filepath.Join(dir, "caller-destructure-other", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input, other: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickOther(ignored: string, {profile: selected, other: alt}: Envelope){return send(alt);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-other", "view.tsx"),
		`import {clickOther, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickOther(id,req)}>Submit</button>;}
`)
	// 动态下标 / 缺声明：保持 unknown。
	writeFile(t, filepath.Join(dir, "caller-destructure-badpath", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickIndex(ignored: string, {profile: selected}: Envelope){return send(selected[0]);}
export function clickMissing(ignored: string, {profile: selected}: Envelope){return send(missing);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-badpath", "view.tsx"),
		`import {clickIndex, clickMissing, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <><button onClick={() => clickIndex(id,req)}>Index</button><button onClick={() => clickMissing(id,req)}>Missing</button></>;}
`)
	// 请求之后的改写不倒推。
	writeFile(t, filepath.Join(dir, "caller-destructure-after", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickAfter(ignored: string, {profile: selected}: Envelope){const r=send(selected);selected.id='changed';return r;}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-after", "view.tsx"),
		`import {clickAfter, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickAfter(id,req)}>Submit</button>;}
`)
	// 覆盖父对象使旧来源失效。
	writeFile(t, filepath.Join(dir, "caller-destructure-parent", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickParent(ignored: string, req: Envelope){req.profile={id:'changed'};return send(req.profile);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-parent", "view.tsx"),
		`import {clickParent, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickParent(id,req)}>Submit</button>;}
`)
	// 改写无关兄弟字段：不污染当前字段来源。
	writeFile(t, filepath.Join(dir, "caller-destructure-sibling", "actions.ts"),
		`export type Profile = {id: string; name: string};
export type Envelope = {profile: Profile};
export function send(req: {id: string}){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickSibling(ignored: string, {profile: selected}: Envelope){selected.name='changed';return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-sibling", "view.tsx"),
		`import {clickSibling, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickSibling(id,req)}>Submit</button>;}
`)
	// 同一解构参数的多个别名各自按声明路径；被调函数在另一文件。
	writeFile(t, filepath.Join(dir, "caller-destructure-identity", "api.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input, other: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-identity", "actions.ts"),
		`import {send, type Envelope} from './api';
export function clickA(ignored: string, {profile: a, other: b}: Envelope){return send(a);}
export function clickB(ignored: string, {profile: a, other: b}: Envelope){return send(b);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-identity", "view.tsx"),
		`import {clickA, clickB} from './actions';
import type {Envelope} from './api';
export function Screen({id, req}: {id:string,req:Envelope}){return <><button onClick={() => clickA(id,req)}>A</button><button onClick={() => clickB(id,req)}>B</button></>;}
`)
	// 多别名 + 改写：根名必须取真实实参（不能按 map 遍历的第一个别名）。
	writeFile(t, filepath.Join(dir, "caller-destructure-multi", "actions.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input, other: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickMutateB(ignored: string, {profile: a, other: b}: Envelope){b.id='changed';return send(b);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-multi", "view.tsx"),
		`import {clickMutateB, type Envelope} from './actions';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => clickMutateB(id,req)}>Submit</button>;}
`)
	// 同名不同声明：只解析实际导入文件里的 click（other 路径）。
	writeFile(t, filepath.Join(dir, "caller-destructure-samename", "a.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input, other: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function click(ignored: string, {profile: selected}: Envelope){return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-samename", "b.ts"),
		`export type Input = {id: string};
export type Envelope = {profile: Input, other: Input};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function click(ignored: string, {other: selected}: Envelope){return send(selected);}
`)
	writeFile(t, filepath.Join(dir, "caller-destructure-samename", "view.tsx"),
		`import {click, type Envelope} from './b';
export function Screen({id, req}: {id:string,req:Envelope}){return <button onClick={() => click(id,req)}>Submit</button>;}
`)
}
