package sqlite

// P1-04：节点属性序列化（池化编码器）的等价性与并发回归。

import (
	"encoding/json"
	"sync"
	"testing"
)

// TestMarshalPropsJSONParity 与 json.Marshal 逐字节相等（含 HTML 转义、
// 多字节字符、数值/嵌套/各种值类型）；nil 与空映射都是 "{}"。
func TestMarshalPropsJSONParity(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"a": 1, "b": "x<y>&z", "c": []any{1.5, "q", nil, true}, "d": map[string]any{"k": "v"}},
		{"中文": "值", "esc": "\u2028\u2029"},
		{"n": int64(1 << 62), "f": 1.0, "neg": -0.0},
		{"html": "<script>alert('x')</script>", "quote": `a"b\c`},
		{"func_id": "symbol:go:example.com/m:(T).M", "origin_kind": "call_result", "ssa_op": "Call"},
	}
	for i, props := range cases {
		want, err := json.Marshal(props)
		if err != nil {
			t.Fatalf("case %d json.Marshal: %v", i, err)
		}
		if len(props) == 0 {
			want = []byte("{}") // nil 映射：json_patch 需要对象操作数
		}
		got, err := marshalPropsJSON(props)
		if err != nil {
			t.Fatalf("case %d marshalPropsJSON: %v", i, err)
		}
		if got != string(want) {
			t.Errorf("case %d 输出不一致:\n got %q\nwant %q", i, got, want)
		}
	}
}

// TestMarshalPropsJSONError 不可序列化的值返回错误（不 panic），且池化
// 编码器在错误后仍可复用（下一次调用输出正确）。
func TestMarshalPropsJSONError(t *testing.T) {
	bad := map[string]any{"fn": func() {}}
	if _, err := marshalPropsJSON(bad); err == nil {
		t.Fatal("chan/func 值应返回错误")
	}
	good := map[string]any{"a": 1}
	got, err := marshalPropsJSON(good)
	if err != nil {
		t.Fatalf("错误后复用编码器失败: %v", err)
	}
	want, _ := json.Marshal(good)
	if got != string(want) {
		t.Errorf("错误后输出不一致: got %q want %q", got, want)
	}
}

// TestMarshalPropsJSONConcurrent 并发调用（sync.Pool 复用 + -race）。
func TestMarshalPropsJSONConcurrent(t *testing.T) {
	props := map[string]any{"func_id": "symbol:go:example.com/m:f", "origin_kind": "local", "n": 7}
	want, _ := json.Marshal(props)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				got, err := marshalPropsJSON(props)
				if err != nil || got != string(want) {
					t.Errorf("并发输出不一致: %q (err=%v)", got, err)
					return
				}
				if s, err := marshalPropsJSON(nil); err != nil || s != "{}" {
					t.Errorf("nil 属性 = %q (err=%v)", s, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
