package action

// 来源身份文本（从 action_seq_workflow_conn.go 拆出）。

import "strings"

// seqWFOriginKey 键来源的身份文本（用于实例分组去重）。
func seqWFOriginKey(o *SeqStorageOrigin) string {
	if o == nil {
		return ""
	}
	parts := []string{o.Kind}
	if o.Symbol != "" {
		parts = append(parts, o.Symbol)
	}
	if o.Parameter != nil {
		parts = append(parts, itoaSeq(*o.Parameter))
	}
	if o.Literal != "" {
		parts = append(parts, o.Literal)
	}
	// U04hR：字段读取/读取列/分配等来源必须保留完整身份——不能只序列化
	// kind/symbol/参数下标，否则不同字段（req.First/req.Second）会被合并。
	if fr := o.FieldRead; fr != nil {
		parts = append(parts, "field", fr.TypeID, fr.ID, strings.Join(fr.Path, "."))
		if fr.Root != nil {
			parts = append(parts, "root", fr.Root.Kind, fr.Root.Symbol, fr.Root.ID,
				itoaSeq(fr.Root.Parameter))
		}
	}
	if rc := o.ReadColumn; rc != nil && rc.Result != nil {
		parts = append(parts, "col", rc.Column, itoaSeq(rc.Index), rc.Result.ID)
	}
	if a := o.Allocation; a != nil {
		parts = append(parts, "alloc", a.ID, a.Entry)
	}
	if g := o.GeneratedKey; g != nil {
		parts = append(parts, "genkey", g.CallID)
	}
	if o.ScanResult != nil {
		parts = append(parts, "scan", o.ScanResult.ID)
	}
	for i := range o.Parts {
		parts = append(parts, seqWFOriginKey(&o.Parts[i]))
	}
	return strings.Join(parts, "|")
}
