//go:build integration

package integration

// todo35 共用公开示例（remaining + orm stub + frontend）+ 反例夹具。
//
// 公开示例部分与 todo35.txt「共用公开示例」一致；额外声明/目录只用于
// 反例矩阵，不改变公开示例入口的 CLI 输出。

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const u35FlowSrc = `package remaining
import("context";"database/sql";"net/http";"github.com/xormplus/xorm";"example.com/remaining/mutator")
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
func openLocalTx(ctx context.Context)(*sql.Tx,error){tx,err:=DB.BeginTx(ctx,nil);return tx,err}
func SubmitLocalHelperTx(ctx context.Context,id string)error{tx,err:=openLocalTx(ctx);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func mutate0(){mutate1()}
func mutate1(){mutate2()}
func mutate2(){mutate3()}
func mutate3(){mutate4()}
func mutate4(){mutate5()}
func mutate5(){mutate6()}
func mutate6(){mutate7()}
func mutate7(){mutate8()}
func mutate8(){mutate9()}
func mutate9(){mutate10()}
func mutate10(){DB=nil}
func ReviewDeepReset(ctx context.Context,id string){mutate0();Review(ctx,id)}
func(Record)TableName()string{return "nodes"}
func ORMNamedFind(id int64){var records []Record;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
type Platform string;type SubmissionType string;const Web Platform="web";const New SubmissionType="new"
func Supported(p Platform,t SubmissionType,region string)bool{return p==Web&&t==New&&(region=="ID"||region=="MY")}
func Step(){Complete("done")}
func Slow(){Step()}
func EntryInline(ctx context.Context,id string,p Platform,t SubmissionType,region string){if !Supported(p,t,region){return};DB.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");Slow()}
func SubmitSwitchBypass(ctx context.Context,id string,skip bool)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};switch skip{case true:return nil};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitSwallowCommit(ctx context.Context,id string)error{tx,err:=DB.BeginTx(ctx,nil);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return nil};return nil}
func ReviewBreakBeforeReset(ctx context.Context,id string){for{break;DB=nil};Review(ctx,id)}
func ReviewPanicReset(ctx context.Context,id string,stop bool){if stop{DB=nil;panic("stop")};Review(ctx,id)}
func ReviewPointerReset(ctx context.Context,id string){mutator.Clear(&DB);Review(ctx,id)}
`

const u35DAOSrc = `package remaining
func LoadRecords(id int64)([]Record,error){var records []Record;err:=Session.Table("nodes").Where("id=?",id).Find(&records);return records,err}
func ReviewDAO(id int64){records,err:=LoadRecords(id);if err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
`

// u35ExtraSrc 反例/正例辅助（B06e/U04bU/B03e/B10e）。
const u35ExtraSrc = `package remaining
import("context";"database/sql";"example.com/remaining/mutator";"example.com/remaining/txhelper")
func _unusedExtra(_ *sql.Tx){}
// B06e.4 同包跨文件 helper：局部变量/命名返回值/重绑。
func openFileTx(ctx context.Context)(*sql.Tx,error){tx,err:=DB.BeginTx(ctx,nil);return tx,err}
func SubmitFileHelperTx(ctx context.Context,id string)error{tx,err:=openFileTx(ctx);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func openNamedFileTx(ctx context.Context)(tx *sql.Tx,err error){tx,err=DB.BeginTx(ctx,nil);return}
func SubmitNamedFileTx(ctx context.Context,id string)error{tx,err:=openNamedFileTx(ctx);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
// B06e.4 跨包 helper：局部变量/命名返回值/冲突返回。
func SubmitCrossPkgTx(ctx context.Context,id string)error{tx,err:=txhelper.OpenTx(ctx,DB);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitCrossPkgNamedTx(ctx context.Context,id string)error{tx,err:=txhelper.OpenNamed(ctx,DB);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitConflictHelperTx(ctx context.Context,id string,useAlt bool)error{tx,err:=txhelper.OpenConflict(ctx,DB,AltDB,useAlt);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func SubmitHelperRebind(ctx context.Context,id string)error{tx,err:=openFileTx(ctx);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};tx,err=DB.BeginTx(ctx,nil);if err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func openConflictFile(ctx context.Context,useAlt bool)(*sql.Tx,error){if useAlt{return AltDB.BeginTx(ctx,nil)};return DB.BeginTx(ctx,nil)}
func SubmitFileConflict(ctx context.Context,id string,useAlt bool)error{tx,err:=openConflictFile(ctx,useAlt);if err!=nil{return err};if _,err=tx.ExecContext(ctx,"INSERT INTO nodes(id,kind) VALUES(?,?)",id,"profile");err!=nil{return err};if err=tx.Commit();err!=nil{return err};return nil}
func ReviewLoopOnce(ctx context.Context,id string){for true{DB=nil;break};Review(ctx,id)}
// U04bU.1 continue/带标签控制。
func ReviewContinueReset(ctx context.Context,id string){for i:=0;i<1;i++{continue;DB=nil};Review(ctx,id)}
func ReviewLabeledBreak(ctx context.Context,id string){outer:for{break outer;DB=nil};Review(ctx,id)}
func ReviewLabeledContinue(ctx context.Context,id string){for i:=0;i<1;i++{inner:for{continue inner;DB=nil};_ = i};Review(ctx,id)}
// U04bU.2 panic 遮蔽与移除终止语句。
func ReviewShadowPanicReset(ctx context.Context,id string,stop bool){panic:=func(string){};if stop{DB=nil;panic("stop")};Review(ctx,id)}
func ReviewPanicNoTerminate(ctx context.Context,id string,stop bool){if stop{DB=nil};Review(ctx,id)}
// U04bU.3 指针参数：只读/改其它资源/读取之后。
func ReviewPointerPeek(ctx context.Context,id string){mutator.Peek(&DB);Review(ctx,id)}
func ReviewPointerOtherReset(ctx context.Context,id string){mutator.ClearOther(&AltDB);Review(ctx,id)}
func ReviewPointerAfterReset(ctx context.Context,id string){Review(ctx,id);mutator.Clear(&DB)}
// U04bU.4 预算边界：短链可证明；超预算必须 fail-closed。
func ReviewShallowReset(ctx context.Context,id string){shallow0();Review(ctx,id)}
func ReviewSafeChain(ctx context.Context,id string){safeChain0();Review(ctx,id)}
func ReviewBudgetReset(ctx context.Context,id string){deepChain0();Review(ctx,id)}
`

// u35ChainSrc 生成 N 层调用链（最后一层 body）。
func u35ChainSrc(prefix string, n int, body string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "func %s%d(){", prefix, i)
		if i == n-1 {
			b.WriteString(body)
		} else {
			fmt.Fprintf(&b, "%s%d()", prefix, i+1)
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// u35ModelExtraSrc B03e.4：静态 TableName / 动态命名 / 其它模型。
const u35ModelExtraSrc = `package remaining
type OtherRecord struct{ID int64 ` + "`xorm:\"pk autoincr 'id'\"`" + `;Kind string ` + "`xorm:\"'kind'\"`" + `}
func(OtherRecord)TableName()string{return "other_nodes"}
func ORMNamedFindOther(id int64){var records []OtherRecord;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
var dynamicTable="dynamic_nodes"
type DynamicRecord struct{ID int64;Kind string}
func(DynamicRecord)TableName()string{return dynamicTable}
func ORMNamedFindDynamic(id int64){var records []DynamicRecord;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
type DecoyRecord struct{ID int64;Kind string}
func(DecoyRecord)TableName()string{return "nodes"}
type NoNameRecord struct{ID int64;Kind string}
func ORMNamedFindNoName(id int64){var records []NoNameRecord;if err:=Session.Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
func ORMNamedFindAlias(id int64){var records []Record;if err:=Session.Table("nodes").Table("other").Where("id=?",id).Find(&records);err!=nil{return};for _,r:=range records{if r.Kind=="profile"{Complete("profile")}}}
`

const u35MutatorSrc = `package mutator
import "database/sql"
func Clear(handle **sql.DB){*handle=nil}
func ClearOther(handle **sql.DB){*handle=nil}
func Peek(handle **sql.DB){_ = *handle}
`

const u35TxHelperSrc = `package txhelper
import("context";"database/sql")
func OpenTx(ctx context.Context,db *sql.DB)(*sql.Tx,error){tx,err:=db.BeginTx(ctx,nil);return tx,err}
func OpenNamed(ctx context.Context,db *sql.DB)(tx *sql.Tx,err error){tx,err=db.BeginTx(ctx,nil);return}
func OpenConflict(ctx context.Context,db,alt *sql.DB,useAlt bool)(*sql.Tx,error){if useAlt{return alt.BeginTx(ctx,nil)};return db.BeginTx(ctx,nil)}
`

const u35FrontendDeepActions = `export function request(id: string){return fetch('/requests', {method: 'POST',body: JSON.stringify({id})});}
export function f1(id: string){return request(id);}
export function f2(id: string){return f1(id);}
export function f3(id: string){return f2(id);}
export function f4(id: string){return f3(id);}
export function click(id: string){return f4(id);}
`

const u35FrontendDeepView = `import {click} from './actions';
export function Screen({id}: {id:string}){return <button onClick={() => click(id)}>Submit</button>;}
`

const u35FrontendFieldActions = `export type Input = {id: string};
export function send(req: Input){return fetch('/requests', {method: 'POST',body: JSON.stringify({id:req.id})});}
`

const u35FrontendFieldView = `import {send, type Input} from './actions';
export function Screen({req}: {req:Input}){return <button onClick={() => send(req)}>Submit</button>;}
`

const u35FrontendDestructureActions = `export type Input = {id: string};
export function send({id}: Input){return fetch('/requests', {method: 'POST',body: JSON.stringify({id})});}
`

const u35FrontendDestructureView = u35FrontendFieldView

// u35Repo 建 todo35 公开示例 + 反例夹具（一次 init）。
func u35Repo(t *testing.T) string {
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
	writeFile(t, filepath.Join(dir, "flow.go"), u35FlowSrc)
	writeFile(t, filepath.Join(dir, "dao.go"), u35DAOSrc)
	writeFile(t, filepath.Join(dir, "extra.go"), u35ExtraSrc+
		u35ChainSrc("shallow", 3, "DB=nil")+
		u35ChainSrc("safeChain", 5, `Complete("safe")`)+
		u35ChainSrc("deepChain", 40, "DB=nil"))
	writeFile(t, filepath.Join(dir, "models_extra.go"), u35ModelExtraSrc)
	writeFile(t, filepath.Join(dir, "txhelper", "txhelper.go"), u35TxHelperSrc)
	writeFile(t, filepath.Join(dir, "mutator", "clear.go"), u35MutatorSrc)
	writeFile(t, filepath.Join(dir, "orm", "go.mod"),
		"module github.com/xormplus/xorm\n\ngo 1.26.2\n")
	writeFile(t, filepath.Join(dir, "orm", "orm.go"), u34OrmSrc)
	u35WriteFrontend(t, dir)
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "fixture")
	clearIndex(t, dir)
	if code := runCLI(t, "init", "--repo", dir, "--workers", "1", "--progress", "none"); code != 0 {
		t.Fatalf("init exit = %d", code)
	}
	return dir
}
