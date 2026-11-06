//go:build integration

package integration

// todo31 共用公开示例（remaining 模块 + orm stub + observer + frontend）。

import (
	"path/filepath"
	"testing"
)

const u31FlowSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm")
var DB *sql.DB
var Session *xorm.Session
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){var kind string;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return};if kind=="profile"{Complete(id)}}
type Detail struct{Kind string}
type Envelope struct{Detail *Detail}
func Pointer(ctx context.Context,id string){record:=Envelope{Detail:new(Detail)};if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Detail.Kind);err!=nil{return};if record.Detail.Kind=="profile"{Complete(id)}}
func PointerRebind(ctx context.Context,id string){record:=Envelope{Detail:new(Detail)};if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Detail.Kind);err!=nil{return};record.Detail=new(Detail);if record.Detail.Kind=="profile"{Complete(id)}}
func PointerOverwrite(ctx context.Context,id string){record:=Envelope{Detail:new(Detail)};if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Detail.Kind);err!=nil{return};record=Envelope{};if record.Detail.Kind=="profile"{Complete(id)}}
var GlobalEnv = Envelope{Detail:new(Detail)}
func GlobalPointer(ctx context.Context,id string){if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&GlobalEnv.Detail.Kind);err!=nil{return};if GlobalEnv.Detail.Kind=="profile"{Complete(id)}}
type Record struct{ID int64 ` + "`xorm:\"pk autoincr 'id'\"`" + `;Kind string ` + "`xorm:\"'kind'\"`" + `}
func ORMUpdate(id int64){record:=Record{Kind:"profile"};Session.Table("nodes").Where("id=?",id).Update(&record)}
func ORMFind(id int64){var records []Record;if err:=Session.Table("nodes").Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
func ORMGenerated(){record:=&Record{Kind:"profile"};if _,err:=Session.Table("nodes").Insert(record);err!=nil{return};DB.Exec("INSERT INTO audits(node_id) VALUES(?)",record.ID)}
func ORMGeneratedExplicit(){record:=&Record{ID:1,Kind:"profile"};if _,err:=Session.Table("nodes").Insert(record);err!=nil{return};DB.Exec("INSERT INTO audits(node_id) VALUES(?)",record.ID)}
func SubmitTx(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitTxUnchecked(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};_ = tx.Commit();return nil}
func SubmitHTTP(w http.ResponseWriter,r *http.Request){Submit(r.Context(),r.URL.Query().Get("id"))}
func ReviewHTTP(w http.ResponseWriter,r *http.Request){Review(r.Context(),r.URL.Query().Get("id"))}
func Register(){http.HandleFunc("/requests",SubmitHTTP);http.HandleFunc("/reviews/finish",ReviewHTTP)}
type Platform string
type SubmissionType string
const Web Platform="web"
const New SubmissionType="new"
func Supported(p Platform,t SubmissionType,region string)bool{return p==Web&&t==New&&(region=="ID"||region=="MY")}
`

const u31CrossSrc = `package remaining
import("context";"example.com/remaining/observer")
func CrossPackage(ctx context.Context,id string){record:=new(observer.Record);if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return};observer.Observe(record)}
`

const u31ObserverSrc = `package observer
type Record struct{Kind string}
func Observe(record *Record){if record.Kind=="profile"{}}
`

const u31AliasesSrc = `package remaining
import "context"
type Alias = Detail
func AliasRoot(ctx context.Context,id string){var record Alias;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return};if record.Kind=="profile"{Complete(id)}}
type Promoted struct{Detail}
func Embedded(ctx context.Context,id string){var record Promoted;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return};if record.Kind=="profile"{Complete(id)}}
`

const u31ScenariosSrc = `package remaining
import "context"
func Entry(ctx context.Context,id string,p Platform,t SubmissionType,region string){if !Supported(p,t,region){return};if region=="ID"{Submit(ctx,id)}else{Submit(ctx,id)}}
`

const u31ProofSrc = `package remaining
import "context"
func Reset(){DB=nil}
func SubmitKnown(ctx context.Context){Submit(ctx,"n1")}
func ReviewRebound(ctx context.Context){Reset();Review(ctx,"n1")}
type Keys struct{First,Second string}
func ReviewTwo(ctx context.Context,req *Keys){
 var first,second string
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",req.First).Scan(&first);err!=nil{return}
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",req.Second).Scan(&second);err!=nil{return}
 if first=="profile"{Complete("first")}
 if second=="profile"{Complete("second")}
}
`

const u31Runtime = `{"version":1,"environment":"test","region":"ID","captured_at":"2026-11-05T00:00:00Z","bindings":[{"symbol":"symbol:go:example.com/remaining:DB","database":"review"}],"databases":[{"id":"review","tables":[{"name":"nodes","scope":{"id":"n1"},"complete":true,"rows":[{"id":"n1","kind":"profile"}]}]}]}
`

const u31FrontendActions = `export async function request(path: string, id: string) {
  return fetch(path, {method: 'POST', body: JSON.stringify({id})});
}
export function submit(id: string) { return request('/requests', id); }
export function finish(id: string) { return request('/reviews/finish', id); }
export function onSubmitClick(id: string) { return submit(id); }
export function onFinishClick(id: string) { return finish(id); }
`

const u31FrontendView = `import {onSubmitClick, onFinishClick} from './actions';
export function Screen({id}: {id: string}) {
  return <><button onClick={() => onSubmitClick(id)}>Submit</button>
    <button onClick={() => onFinishClick(id)}>Finish review</button></>;
}
`

// u31Repo 建 remaining 示例仓库（orm stub + observer + frontend + runtime）。
func u31Repo(t *testing.T) string {
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
	writeFile(t, filepath.Join(dir, "flow.go"), u31FlowSrc)
	writeFile(t, filepath.Join(dir, "cross.go"), u31CrossSrc)
	writeFile(t, filepath.Join(dir, "observer", "observer.go"), u31ObserverSrc)
	writeFile(t, filepath.Join(dir, "aliases.go"), u31AliasesSrc)
	writeFile(t, filepath.Join(dir, "scenarios.go"), u31ScenariosSrc)
	writeFile(t, filepath.Join(dir, "proof.go"), u31ProofSrc)
	writeFile(t, filepath.Join(dir, "runtime.json"), u31Runtime)
	writeFile(t, filepath.Join(dir, "frontend", "actions.ts"), u31FrontendActions)
	writeFile(t, filepath.Join(dir, "frontend", "view.tsx"), u31FrontendView)
	writeFile(t, filepath.Join(dir, "orm", "go.mod"), "module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "orm", "orm.go"), `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Update(any)(int64,error){return 0,nil}
func(s *Session)Get(any)(bool,error){return false,nil}
func(s *Session)Find(any)error{return nil}
`)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}
