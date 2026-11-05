//go:build integration

package integration

// todo30 字段槽测试夹具（源码常量；测试逻辑见 todo30_field_slots_test.go）。

const u30FieldModelSrc = `package model

type Leaf struct{ Kind string }

func (l Leaf) GetKind() string { return l.Kind }
`

const u30FieldSrc = `package scanfield
import("context";"database/sql";"example.com/scanfield/model")
var DB *sql.DB
var Global *Record
var otherVal Record

type Record struct{ Kind string }
type Alias = Record

func (r *Record) Current() string { return r.Kind }
func (r *Record) Reset()          { r.Kind = "reset" }
func (r *Record) GetOther() string { return otherVal.Kind }
func (r *Record) WithArg(string) string { return r.Kind }

func Complete(string){}
func take(kind string){if kind=="profile"{Complete(kind)}}
func other(p *Record){}

func Value(ctx context.Context,id string){
 var record Record
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func ValueLit(ctx context.Context,id string){
 record:=Record{}
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func ValueParam(ctx context.Context,id string,record Record){
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func ModelField(ctx context.Context,id string){
 var record model.Leaf
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func BadPointer(ctx context.Context,id string,record *Record){
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func AliasRoot(ctx context.Context,id string){
 var record Alias
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func Nested(ctx context.Context,id string){
 var box struct{ Inner Record }
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&box.Inner.Kind);err!=nil{return}
 if box.Inner.Kind=="profile"{Complete(id)}
}
func Custom(ctx context.Context,id string){
 var c model.Leaf
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&c);err!=nil{return}
 if c.Kind=="profile"{Complete(id)}
}
func Conditional(ctx context.Context,id string,flag bool){
 var record Record
 if flag {
  if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 }
 if record.Kind=="profile"{Complete(id)}
}
func Loop(ctx context.Context,id string){
 var record Record
 for i:=0;i<0;i++ {
  if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 }
 if record.Kind=="profile"{Complete(id)}
}
func PtrAlias(ctx context.Context,id string){
 record:=new(Record)
 alias:=record
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&alias.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func PtrAliasWrite(ctx context.Context,id string){
 record:=new(Record)
 alias:=record
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&alias.Kind);err!=nil{return}
 alias.Kind="other"
 if record.Kind=="profile"{Complete(id)}
}
func PtrDerefWrite(ctx context.Context,id string){
 record:=new(Record)
 alias:=record
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&alias.Kind);err!=nil{return}
 *alias=Record{}
 if record.Kind=="profile"{Complete(id)}
}
func PtrEscape(ctx context.Context,id string){
 record:=new(Record)
 other(record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.Kind=="profile"{Complete(id)}
}
func PtrGlobal(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 Global=record
 if record.Kind=="profile"{Complete(id)}
}
func PtrOther(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 x:=new(Record)
 if x.Kind=="profile"{Complete(id)}
}
func Getter(ctx context.Context,id string){
 record:=new(Record)
 alias:=record
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&alias.Kind);err!=nil{return}
 if record.Current()=="profile"{Complete(id)}
}
func GetterMutated(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 record.Reset()
 if record.Current()=="profile"{Complete(id)}
}
func WrongGetter(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 if record.GetOther()=="profile"{Complete(id)}
}
func DeferArg(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 defer take(record.Current())
 record.Kind="other"
}
func DeferBody(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 defer func(){take(record.Current())}()
 record.Kind="other"
}
func Immediate(ctx context.Context,id string){
 record:=new(Record)
 if err:=DB.QueryRowContext(ctx,"SELECT kind FROM nodes WHERE id=?",id).Scan(&record.Kind);err!=nil{return}
 defer take(func()string{return record.Current()}())
 record.Kind="other"
}
`
