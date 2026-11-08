//go:build integration

package integration

// todo36 共用公开示例（goto/ORM 忽略字段/前端包装字段）+ 反例夹具。

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const u36FlowSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm")
var DB *sql.DB
var AltDB *sql.DB
var Session *xorm.Session
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){var kind string;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return};if kind=="profile"{Complete(id)}}
func ReviewGotoReset(ctx context.Context,id string){goto reset;reset:DB=nil;Review(ctx,id)}
func ReviewGotoSkip(ctx context.Context,id string){goto read;DB=nil;read:Review(ctx,id)}
type Record struct{ID int64 ` + "`xorm:\"pk 'id'\"`" + `;Kind string ` + "`xorm:\"'kind'\"`" + `}
func(Record)TableName()string{return "nodes"}
func ORMUpdate(id int64){record:=Record{Kind:"profile"};Session.Table("nodes").Where("id=?",id).Update(&record)}
func ORMNamedFind(id int64){var records []Record;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
type IgnoredRecord struct{ID int64 ` + "`xorm:\"pk 'id'\"`" + `;Kind string ` + "`xorm:\"-\"`" + `}
func(IgnoredRecord)TableName()string{return "nodes"}
func ORMIgnoredFind(id int64){var records []IgnoredRecord;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
func SubmitHTTP(w http.ResponseWriter,r *http.Request){Submit(r.Context(),r.URL.Query().Get("id"))}
func ReviewHTTP(w http.ResponseWriter,r *http.Request){Review(r.Context(),r.URL.Query().Get("id"))}
func Register(){http.HandleFunc("/requests",SubmitHTTP);http.HandleFunc("/reviews/finish",ReviewHTTP)}
`

// u36ExtraSrc goto 路径变体（同包跨文件/其它资源/读取后/循环）。
const u36ExtraSrc = `package remaining
import "context"
func reviewFileGotoReset(ctx context.Context,id string){goto reset;reset:DB=nil;Review(ctx,id)}
func reviewFileGotoSkip(ctx context.Context,id string){goto read;DB=nil;read:Review(ctx,id)}
func reviewFileGotoOther(ctx context.Context,id string){goto read;read:AltDB=nil;Review(ctx,id)}
func reviewFileGotoAfter(ctx context.Context,id string){Review(ctx,id);goto reset;reset:DB=nil}
func ReviewGotoAfterReset(ctx context.Context,id string){Review(ctx,id);goto reset;reset:DB=nil}
func ReviewGotoOtherReset(ctx context.Context,id string){goto read;read:AltDB=nil;Review(ctx,id)}
func ReviewGotoBackward(ctx context.Context,id string){i:=0;again:i++;if i<2{goto again};Review(ctx,id)}
`

// u36ModelExtraSrc ORM 忽略字段通过 helper 返回 + range；重复列歧义。
const u36ModelExtraSrc = `package remaining
func LoadIgnoredRecords(id int64)([]IgnoredRecord,error){var records []IgnoredRecord;err:=Session.Where("id=?",id).Find(&records);return records,err}
func ReviewIgnoredDAO(id int64){records,err:=LoadIgnoredRecords(id);if err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
type AmbiguousRecord struct{ID int64 ` + "`xorm:\"pk 'id'\"`" + `;A string ` + "`xorm:\"'kind'\"`" + `;B string ` + "`xorm:\"'kind'\"`" + `}
func(AmbiguousRecord)TableName()string{return "nodes"}
func ORMAmbiguousFind(id int64){var records []AmbiguousRecord;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.A=="profile"{Complete("profile")}}}
type AllIgnoredRecord struct{ID int64 ` + "`xorm:\"-\"`" + `;Kind string ` + "`xorm:\"-\"`" + `}
func(AllIgnoredRecord)TableName()string{return "nodes"}
func ORMAllIgnoredFind(id int64){var records []AllIgnoredRecord;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
`

// u36GotoBudgetSrc 生成 70 个 goto 跳转（超出共享 goto 预算）。
func u36GotoBudgetSrc() string {
	var b strings.Builder
	b.WriteString("func ReviewGotoBudget(ctx context.Context,id string){goto g0;")
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&b, "g%d:", i)
		if i == 69 {
			b.WriteString("DB=nil;Review(ctx,id)}\n")
		} else {
			fmt.Fprintf(&b, "goto g%d;", i+1)
		}
	}
	return b.String()
}

const u36OrmSrc = `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Update(any)(int64,error){return 0,nil}
func(s *Session)Get(any)(bool,error){return false,nil}
func(s *Session)Find(any)error{return nil}
`

// u36Repo 建 todo36 公开示例 + 反例夹具（一次 init）。
func u36Repo(t *testing.T) string {
	t.Helper()
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `module example.com/remaining

go 1.26.2

require github.com/xormplus/xorm v0.0.0
replace github.com/xormplus/xorm => ./orm
`)
	writeFile(t, filepath.Join(dir, "flow.go"), u36FlowSrc)
	writeFile(t, filepath.Join(dir, "extra.go"), u36ExtraSrc+u36GotoBudgetSrc())
	writeFile(t, filepath.Join(dir, "models_extra.go"), u36ModelExtraSrc)
	writeFile(t, filepath.Join(dir, "orm", "go.mod"),
		"module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "orm", "orm.go"), u36OrmSrc)
	u36WriteFrontend(t, dir)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u36WriteFrontend B09e.4 公开示例 + 反例目录。
func u36WriteFrontend(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "wrapper-field", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function click(ignored: string, req: Input){return send(req);}
export function clickMutated(ignored: string, req: Input){req.id='changed';return send(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-field", "view.tsx"),
		`import {click, clickMutated, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <><button onClick={() => click(id,req)}>Submit</button><button onClick={() => clickMutated(id,req)}>Changed</button></>;}
`)
	// 调换包装函数形参顺序：来源必须随真实实参变化。
	writeFile(t, filepath.Join(dir, "wrapper-reordered", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function click(req: Input, ignored: string){return send(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-reordered", "view.tsx"),
		`import {click, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => click(req,id)}>Submit</button>;}
`)
	// 请求之后的改写不倒推。
	writeFile(t, filepath.Join(dir, "wrapper-after", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickAfter(ignored: string, req: Input){const r=send(req);req.id='changed';return r;}
`)
	writeFile(t, filepath.Join(dir, "wrapper-after", "view.tsx"),
		`import {clickAfter, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => clickAfter(id,req)}>Submit</button>;}
`)
	// 覆盖整个对象 / 父字段 / 局部遮蔽 / 动态下标。
	writeFile(t, filepath.Join(dir, "wrapper-whole", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickWhole(ignored: string, req: Input){req={id:'changed'};return send(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-whole", "view.tsx"),
		`import {clickWhole, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => clickWhole(id,req)}>Submit</button>;}
`)
	writeFile(t, filepath.Join(dir, "wrapper-parent", "actions.ts"),
		`export type Input = {user: {id: string}};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.user.id})});}
export function clickParent(ignored: string, req: Input){req.user={id:'changed'};return send(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-parent", "view.tsx"),
		`import {clickParent, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => clickParent(id,req)}>Submit</button>;}
`)
	writeFile(t, filepath.Join(dir, "wrapper-shadow", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
export function clickShadow(ignored: string, req: Input){const req={id:'changed'};return send(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-shadow", "view.tsx"),
		`import {clickShadow, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => clickShadow(id,req)}>Submit</button>;}
`)
	writeFile(t, filepath.Join(dir, "wrapper-index", "actions.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req[0]})});}
export function clickIndex(ignored: string, req: Input){return send(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-index", "view.tsx"),
		`import {clickIndex, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => clickIndex(id,req)}>Submit</button>;}
`)
	// 多层包装 + 解构别名路径。
	writeFile(t, filepath.Join(dir, "wrapper-destructure", "actions.ts"),
		`export type Input = {id: string};
export function send({id: alias}: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:alias})});}
export function mid(req: Input){return send(req);}
export function click(ignored: string, req: Input){return mid(req);}
`)
	writeFile(t, filepath.Join(dir, "wrapper-destructure", "view.tsx"),
		`import {click, type Input} from './actions';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => click(id,req)}>Submit</button>;}
`)
	// 同名不同文件：只解析实际导入的 send。
	writeFile(t, filepath.Join(dir, "wrapper-same-name", "a.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/alternate', {method:'POST',body:JSON.stringify({id:req.id})});}
`)
	writeFile(t, filepath.Join(dir, "wrapper-same-name", "b.ts"),
		`export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method:'POST',body:JSON.stringify({id:req.id})});}
`)
	writeFile(t, filepath.Join(dir, "wrapper-same-name", "view.tsx"),
		`import {send, type Input} from './a';
export function Screen({id, req}: {id:string,req:Input}){return <button onClick={() => send(req)}>Submit</button>;}
`)
}
