//go:build integration

package integration

// todo32 共用公开示例（remaining + orm stub + identity-frontend + DAO）。

import (
	"path/filepath"
	"testing"
)

const u32FlowSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm")
var DB *sql.DB
var Session *xorm.Session
func Complete(string){}
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){var kind string;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return};if kind=="profile"{Complete(id)}}
type Detail struct{Kind string}
type Envelope struct{Detail *Detail}
func Pointer(ctx context.Context,id string){record:=Envelope{Detail:new(Detail)};if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Detail.Kind);err!=nil{return};if record.Detail.Kind=="profile"{Complete(id)}}
type Record struct{ID int64 ` + "`xorm:\"pk autoincr 'id'\"`" + `;Kind string ` + "`xorm:\"'kind'\"`" + `}
func ORMUpdate(id int64){record:=Record{Kind:"profile"};Session.Table("nodes").Where("id=?",id).Update(&record)}
func ORMFind(id int64){var records []Record;if err:=Session.Table("nodes").Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
func ORMGenerated(){record:=&Record{Kind:"profile"};if _,err:=Session.Table("nodes").Insert(record);err!=nil{return};DB.Exec("INSERT INTO audits(node_id) VALUES(?)",record.ID)}
func SubmitTx(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitHTTP(w http.ResponseWriter,r *http.Request){Submit(r.Context(),r.URL.Query().Get("id"))}
func ReviewHTTP(w http.ResponseWriter,r *http.Request){Review(r.Context(),r.URL.Query().Get("id"))}
func Register(){http.HandleFunc("/requests",SubmitHTTP);http.HandleFunc("/reviews/finish",ReviewHTTP)}
type Platform string
type SubmissionType string
const Web Platform="web"
const New SubmissionType="new"
func Supported(p Platform,t SubmissionType,region string)bool{return p==Web&&t==New&&(region=="ID"||region=="MY")}
func SubmitOtherTx(ctx context.Context,id string,other *sql.Tx)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=other.Commit();err!=nil{return err};return nil}
func SubmitTxIgnored(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};_ = tx.Commit();return nil}
func ReviewUnusedReset(ctx context.Context,id string){unused:=func(){DB=nil};_ = unused;Review(ctx,id)}
func ReviewAfterReset(ctx context.Context,id string){Review(ctx,id);DB=nil}
func ReviewBeforeReset(ctx context.Context,id string){DB=nil;Review(ctx,id)}
func ReviewClosureExec(ctx context.Context,id string){f:=func(){DB=nil};f();Review(ctx,id)}
`

const u32ScenariosSrc = `package remaining
import "context"
func Entry(ctx context.Context,id string,p Platform,t SubmissionType,region string){if !Supported(p,t,region){return};if region=="ID"{Submit(ctx,id)}else{Submit(ctx,id)}}
`

const u32DAOSrc = `package remaining
func LoadRecords(id int64)([]Record,error){var records []Record;err:=Session.Table("nodes").Where("id=?",id).Find(&records);return records,err}
func ReviewDAO(id int64){records,err:=LoadRecords(id);if err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
`

const u32FrontendA = `export function request() {return fetch('/requests', {method: 'POST'});}
export function click() {return request();}
`

const u32FrontendZ = `export function request() {return fetch('/reviews/finish', {method: 'POST'});}
`

const u32FrontendView = `import {click} from './a';
export function Screen(){return <button onClick={() => click()}>Submit</button>;}
`

const u32OrmSrc = `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Update(any)(int64,error){return 0,nil}
func(s *Session)Get(any)(bool,error){return false,nil}
func(s *Session)Find(any)error{return nil}
`

// u32Repo 建 todo32 公开示例仓库。
func u32Repo(t *testing.T) string {
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
	writeFile(t, filepath.Join(dir, "flow.go"), u32FlowSrc)
	writeFile(t, filepath.Join(dir, "scenarios.go"), u32ScenariosSrc)
	writeFile(t, filepath.Join(dir, "dao.go"), u32DAOSrc)
	writeFile(t, filepath.Join(dir, "identity-frontend", "a.ts"), u32FrontendA)
	writeFile(t, filepath.Join(dir, "identity-frontend", "z.ts"), u32FrontendZ)
	writeFile(t, filepath.Join(dir, "identity-frontend", "view.tsx"), u32FrontendView)
	writeFile(t, filepath.Join(dir, "orm", "go.mod"),
		"module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "orm", "orm.go"), u32OrmSrc)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}
