package action

// B05b：ORM Insert 成功后自动主键回填到模型对象的准确字段槽。
//
// 条件化来源：`generated_key` 带 insert_success 前提；只对带 pk autoincr
// 声明、未显式给主键、且已证明 Session.Insert 身份的插入生效。

import "go/ast"

// seqBindORMGeneratedKey ORM Insert 主键回填绑定到对象字段槽。
func seqBindORMGeneratedKey(req CodeSequenceRequest, node *CodeSeqNode, call *ast.CallExpr,
	orm *SeqORM) {
	if req.env == nil || node == nil || node.Evidence == nil || orm == nil || call == nil ||
		orm.Operation != SeqStorageInsert || orm.ErrorResult == nil || len(call.Args) == 0 {
		return
	}
	for _, m := range orm.Models {
		field, _, ok := seqORMPKField(m)
		if !ok || m.Argument < 0 || m.Argument >= len(call.Args) {
			continue
		}
		id, ok := runtimeUnparen(call.Args[m.Argument]).(*ast.Ident)
		if !ok || id.Obj == nil || id.Name == "_" {
			continue
		}
		if seqORMLiteralSetsField(orm, m.Argument, field) {
			continue
		}
		gk := &SeqGeneratedKey{CallID: orm.ErrorResult.CallID, Slot: 0,
			Symbol: node.Evidence.SymbolID, Access: &SeqGeneratedKeyAccess{
				Symbol: node.Evidence.SymbolID, File: node.Evidence.CallFile,
				Offset: node.Evidence.CallLine},
			BackfillField: field, BackfillType: m.SymbolID,
			Premises: []string{"insert_success", "orm_primary_key_backfill"},
			Outcome:  "unproven", Unproven: true}
		origin := &SeqStorageOrigin{Kind: SeqOriginGeneratedKey, ScalarType: "int64",
			GeneratedKey: gk}
		key := seqFieldSlotKey{obj: id.Obj, field: field}
		if alloc := seqAllocOriginOf(req, id); alloc != nil {
			key = seqFieldSlotKey{alloc: alloc.ID, field: field}
		}
		seqFieldSlotSet(req, key, origin)
	}
}

// seqORMLiteralSetsField 模型字面量是否显式设置了该字段（显式主键不回填）。
func seqORMLiteralSetsField(orm *SeqORM, arg int, field string) bool {
	if orm == nil || arg < 0 || arg >= len(orm.Arguments) {
		return false
	}
	o := orm.Arguments[arg].Origin
	if o == nil || o.Kind != SeqOriginStructLiteral || o.StructLiteral == nil {
		return false
	}
	for _, f := range o.StructLiteral.Fields {
		if f.Name == field && f.Origin != nil {
			return true
		}
	}
	return false
}
