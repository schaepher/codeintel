//go:build integration

package integration

// todo34 共用公开示例（remaining + orm stub + frontend）+ 反例夹具。
//
// 公开示例部分与 todo34.txt「共用公开示例」逐字一致；反例/正例辅助函数放在
// extra.go 与 frontend-* 目录，不改变公开示例中已规定的 CLI 输出。

import (
	"path/filepath"
	"testing"
)

const u34FlowSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm")
var DB *sql.DB
var AltDB *sql.DB
var Session *xorm.Session
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){var kind string;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return};if kind=="profile"{Complete(id)}}
type Record struct{ID int64 ` + "`xorm:\"pk autoincr 'id'\"`" + `;Kind string ` + "`xorm:\"'kind'\"`" + `}
func ORMUpdate(id int64){record:=Record{Kind:"profile"};Session.Table("nodes").Where("id=?",id).Update(&record)}
func SubmitTx(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitHTTP(w http.ResponseWriter,r *http.Request){Submit(r.Context(),r.URL.Query().Get("id"))}
func ReviewHTTP(w http.ResponseWriter,r *http.Request){Review(r.Context(),r.URL.Query().Get("id"))}
func Register(){http.HandleFunc("/requests",SubmitHTTP);http.HandleFunc("/reviews/finish",ReviewHTTP)}
func SubmitWrongError(ctx context.Context,id string,errOther error)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if commitErr:=tx.Commit();errOther!=nil{_ = commitErr;return errOther};return nil}
func SubmitReboundTx(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};tx,err=DB.BeginTx(ctx,nil);if err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func ReviewZeroLoop(ctx context.Context,id string){for false{DB=nil};Review(ctx,id)}
func ReviewEarlyReset(ctx context.Context,id string,stop bool){if stop{DB=nil;return};Review(ctx,id)}
`

const u34DAOSrc = `package remaining
func LoadRecords(id int64)([]Record,error){var records []Record;err:=Session.Table("nodes").Where("id=?",id).Find(&records);return records,err}
func ReviewDAO(id int64){records,err:=LoadRecords(id);if err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
`

// u34ExtraSrc 反例/正例辅助（B06e/U04bU/B03e）。不影响公开示例的入口输出。
const u34ExtraSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm")
func _unusedExtra(_ *sql.Tx,_ *xorm.Session){}
// 额外的 HTTP 路由（用于"改一个事实所有出口同步变化"的一致性验证）。
func RegisterExtra(){http.HandleFunc("/alternate",SubmitHTTP)}
// B06e.1 事务别名（同一来源）与别名重绑（另一来源）。
func SubmitTxAlias(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};alias:=tx;if err=alias.Commit();err!=nil{return err};return nil}
func SubmitAliasRebind(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};alias:=tx;alias,err=DB.BeginTx(ctx,nil);if err!=nil{return err};if err=alias.Commit();err!=nil{return err};return nil}
// B06e.2 错误别名（检查同一错误槽）与覆盖错误后再检查。
func SubmitErrAlias(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};commitErr:=tx.Commit();e:=commitErr;if e!=nil{return e};return nil}
func SubmitOverwriteError(ctx context.Context,id string,other error)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};commitErr:=tx.Commit();commitErr=other;if commitErr!=nil{return commitErr};return nil}
// B06e.3 条件绕过 / Rollback / 未执行闭包 / 失败分支无直接 return。
func SubmitConditionalCommit(ctx context.Context,id string,commit bool)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if commit{if err=tx.Commit();err!=nil{return err}};return nil}
func SubmitRollbackOnly(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Rollback();err!=nil{return err};return nil}
func SubmitClosureCommit(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};f:=func()error{return tx.Commit()};_ = f;return nil}
func SubmitNoDirectReturn(ctx context.Context,id string,quiet bool)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{if quiet{_ = err}};return nil}
// B06e.4 helper 返回同一事务（正例）/ 另一事务 / 冲突返回（反例）。
func openTx(ctx context.Context)(*sql.Tx,error){return DB.BeginTx(ctx,nil)}
func SubmitHelperTx(ctx context.Context,id string)error{tx,err:=openTx(ctx);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitHelperSplit(ctx context.Context,id string)error{tx,err:=openTx(ctx);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};other,err:=openTx(ctx);if err!=nil{return err};if err=other.Commit();err!=nil{return err};return nil}
func openTxConflict(ctx context.Context,a bool)(*sql.Tx,error){if a{return DB.BeginTx(ctx,nil)};return AltDB.BeginTx(ctx,nil)}
func SubmitHelperConflict(ctx context.Context,id string)error{tx,err:=openTxConflict(ctx,true);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
// U04bU：循环 / 改写时机 / 闭包 / 分支。
func ReviewLoopOnce(ctx context.Context,id string){for true{DB=nil;break};Review(ctx,id)}
func ReviewResetNoReturn(ctx context.Context,id string,stop bool){if stop{DB=nil};Review(ctx,id)}
func ReviewAfterReset(ctx context.Context,id string){Review(ctx,id);DB=nil}
func ReviewBeforeReset(ctx context.Context,id string){DB=nil;Review(ctx,id)}
func ReviewUncalledClosure(ctx context.Context,id string){f:=func(){DB=nil};_ = f;Review(ctx,id)}
func ReviewCalledClosure(ctx context.Context,id string){f:=func(){DB=nil};f();Review(ctx,id)}
func resetDB(){DB=nil}
func ReviewLoopInit(ctx context.Context,id string){for resetDB();false;{};Review(ctx,id)}
func ReviewLoopPost(ctx context.Context,id string){for i:=0;i<1;DB=nil{_ = i};Review(ctx,id)}
func ReviewShadow(ctx context.Context,id string){DB:=DB;_ = DB;Review(ctx,id)}
`

// u34DAOExtraSrc B03e：Get 的 (bool,error) 槽、返回另一集合、错误被覆盖。
const u34DAOExtraSrc = `package remaining
func ReviewOne(id int64){var r Record;ok,err:=Session.Table("nodes").Where("id=?",id).Get(&r);if err!=nil{return};if !ok{return};if r.Kind=="profile"{Complete("profile")}}
func LoadWrong(id int64)([]Record,error){var other []Record;err:=Session.Table("nodes").Where("id=?",id).Find(&other);var records []Record;return records,err}
func ReviewWrong(id int64){records,err:=LoadWrong(id);if err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
func LoadOverwrite(id int64)([]Record,error){var records []Record;err:=Session.Table("nodes").Where("id=?",id).Find(&records);err=nil;return records,err}
func ReviewOverwrite(id int64){records,err:=LoadOverwrite(id);if err!=nil{return};_ = records}
`

const u34OrmSrc = `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Update(any)(int64,error){return 0,nil}
func(s *Session)Get(any)(bool,error){return false,nil}
func(s *Session)Find(any)error{return nil}
`

const u34FrontendDefaultActions = `export default function send(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}
`

const u34FrontendDefaultView = `import send from './actions';
export function Screen({id}: {id: string}) {return <button onClick={() => send(id)}>Submit</button>;}
`

const u34FrontendAliasA = `export function request(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}
export function click(id: string) {return request(id);}
`

const u34FrontendAliasView = `import {click as submitClick} from './a';
export function Screen({id}: {id: string}) {return <button onClick={() => submitClick(id)}>Submit</button>;}
`

const u34FrontendAliasDirect = `import {click} from './a';
export function Direct({id}: {id: string}) {return <button onClick={() => click(id)}>Submit</button>;}
`

// u34Repo 建 todo34 公开示例 + 反例夹具仓库（一次 init）。
func u34Repo(t *testing.T) string {
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
	writeFile(t, filepath.Join(dir, "flow.go"), u34FlowSrc)
	writeFile(t, filepath.Join(dir, "dao.go"), u34DAOSrc)
	writeFile(t, filepath.Join(dir, "extra.go"), u34ExtraSrc)
	writeFile(t, filepath.Join(dir, "dao_extra.go"), u34DAOExtraSrc)
	writeFile(t, filepath.Join(dir, "orm", "go.mod"),
		"module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "orm", "orm.go"), u34OrmSrc)
	writeFile(t, filepath.Join(dir, "default-frontend", "actions.ts"), u34FrontendDefaultActions)
	writeFile(t, filepath.Join(dir, "default-frontend", "view.tsx"), u34FrontendDefaultView)
	writeFile(t, filepath.Join(dir, "alias-frontend", "a.ts"), u34FrontendAliasA)
	writeFile(t, filepath.Join(dir, "alias-frontend", "view.tsx"), u34FrontendAliasView)
	writeFile(t, filepath.Join(dir, "alias-frontend", "direct.tsx"), u34FrontendAliasDirect)
	u34WriteFrontendVariants(t, dir)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}

// u34WriteFrontendVariants B09e 反例：缺源码 / 非函数默认导出 / 同名函数 /
// 动态 URL / 默认导出冲突。
func u34WriteFrontendVariants(t *testing.T, dir string) {
	t.Helper()
	// 缺源码：view 导入 ./actions，但文件不存在。
	writeFile(t, filepath.Join(dir, "missing-frontend", "view.tsx"),
		"import send from './actions';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
	// 非函数默认导出：export default send（没有 export default function）。
	writeFile(t, filepath.Join(dir, "object-frontend", "actions.ts"),
		"const send = (id: string) => fetch('/requests', {method: 'POST'});\nexport default send;\n")
	writeFile(t, filepath.Join(dir, "object-frontend", "view.tsx"),
		"import send from './actions';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
	// 同名函数：两个文件都有 send，导入哪个文件就用哪个文件的路径。
	writeFile(t, filepath.Join(dir, "same-name", "a.ts"),
		"export function send(id: string) {return fetch('/alternate', {method: 'POST', body: JSON.stringify({id})});}\n")
	writeFile(t, filepath.Join(dir, "same-name", "b.ts"),
		"export function send(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}\n")
	writeFile(t, filepath.Join(dir, "same-name", "view.tsx"),
		"import {send} from './a';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
	// 默认值重导出：b.ts `export {default} from './a'`，view 导入默认。
	writeFile(t, filepath.Join(dir, "reexport-frontend", "a.ts"), u34FrontendDefaultActions)
	writeFile(t, filepath.Join(dir, "reexport-frontend", "b.ts"),
		"export {default} from './a';\n")
	writeFile(t, filepath.Join(dir, "reexport-frontend", "view.tsx"),
		"import send from './b';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
	// 动态 URL：动作实参不是字面量 → 保留动作 + unknown。
	writeFile(t, filepath.Join(dir, "dynamic-frontend", "actions.ts"),
		"export default function send(u: string) {return fetch(u, {method: 'POST'});}\n")
	writeFile(t, filepath.Join(dir, "dynamic-frontend", "view.tsx"),
		"import send from './actions';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
	// 默认导出冲突：两个 export default function。
	writeFile(t, filepath.Join(dir, "conflict-frontend", "actions.ts"),
		"export default function a(id: string) {return fetch('/a', {method: 'POST'});}\nexport default function b(id: string) {return fetch('/b', {method: 'POST'});}\n")
	writeFile(t, filepath.Join(dir, "conflict-frontend", "view.tsx"),
		"import send from './actions';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
	// 请求体字段未知：body 不是对象字面量 → 不借用形参来源。
	writeFile(t, filepath.Join(dir, "body-frontend", "actions.ts"),
		"export default function send(id: string) {const payload={id:'x'};payload.id=id;return fetch('/requests', {method: 'POST', body: JSON.stringify(payload)});}\n")
	writeFile(t, filepath.Join(dir, "body-frontend", "view.tsx"),
		"import send from './actions';\nexport function S({id}: {id: string}) {return <button onClick={() => send(id)}>S</button>;}\n")
}
