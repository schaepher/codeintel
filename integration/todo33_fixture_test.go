//go:build integration

package integration

// todo33 共用公开示例（remaining + worker + orm stub + alias-frontend + DAO）。

import (
	"path/filepath"
	"testing"
)

const u33FlowSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm")
var DB *sql.DB
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
type Platform string
type SubmissionType string
const Web Platform="web"
const New SubmissionType="new"
func Supported(p Platform,t SubmissionType,region string)bool{return p==Web&&t==New&&(region=="ID"||region=="MY")}
func SubmitConditionalTx(ctx context.Context,id string,commit bool)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if commit{if err=tx.Commit();err!=nil{return err}};return nil}
func ReviewShadow(ctx context.Context,id string){DB:=DB;_ = DB;Review(ctx,id)}
func ReviewExcludedElse(ctx context.Context,id string){if true{}else{DB=nil};Review(ctx,id)}
`

const u33ScenariosSrc = `package remaining
import "context"
func Entry(ctx context.Context,id string,p Platform,t SubmissionType,region string){if !Supported(p,t,region){return};if region=="ID"{Submit(ctx,id)}else{Submit(ctx,id)}}
`

const u33DAOSrc = `package remaining
func LoadRecords(id int64)([]Record,error){var records []Record;err:=Session.Table("nodes").Where("id=?",id).Find(&records);return records,err}
func ReviewDAO(id int64){records,err:=LoadRecords(id);if err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
`

const u33WorkerSrc = `package worker
import("context";"database/sql")
var DB *sql.DB
func Submit(ctx context.Context,id string){DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile")}
func Review(ctx context.Context,id string){var kind string;if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&kind);err!=nil{return}}
func ReviewBeforeReset(ctx context.Context,id string){DB=nil;Review(ctx,id)}
`

const u33OrmSrc = `package xorm
type Session struct{}
func(s *Session)Table(any)*Session{return s}
func(s *Session)Where(string,...any)*Session{return s}
func(s *Session)Insert(...any)(int64,error){return 0,nil}
func(s *Session)Update(any)(int64,error){return 0,nil}
func(s *Session)Get(any)(bool,error){return false,nil}
func(s *Session)Find(any)error{return nil}
`

const u33FrontendA = `export function request(id: string) {return fetch('/requests', {method: 'POST', body: JSON.stringify({id})});}
export function click(id: string) {return request(id);}
`

const u33FrontendView = `import {click as submitClick} from './a';
export function Screen({id}: {id: string}) {return <button onClick={() => submitClick(id)}>Submit</button>;}
`

const u33FrontendDirect = `import {click} from './a';
export function Direct({id}: {id: string}) {return <button onClick={() => click(id)}>Submit</button>;}
`

// u33Repo 建 todo33 公开示例仓库。
func u33Repo(t *testing.T) string {
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
	writeFile(t, filepath.Join(dir, "flow.go"), u33FlowSrc)
	writeFile(t, filepath.Join(dir, "scenarios.go"), u33ScenariosSrc)
	writeFile(t, filepath.Join(dir, "dao.go"), u33DAOSrc)
	writeFile(t, filepath.Join(dir, "worker", "flow.go"), u33WorkerSrc)
	writeFile(t, filepath.Join(dir, "orm", "go.mod"),
		"module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "orm", "orm.go"), u33OrmSrc)
	writeFile(t, filepath.Join(dir, "alias-frontend", "a.ts"), u33FrontendA)
	writeFile(t, filepath.Join(dir, "alias-frontend", "view.tsx"), u33FrontendView)
	writeFile(t, filepath.Join(dir, "alias-frontend", "direct.tsx"), u33FrontendDirect)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}
