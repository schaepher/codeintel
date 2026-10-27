//go:build integration

package integration

// todo22 §9.2：二十个后续流程形态（Web / App / Partner App / 宿主 / 支付 /
// API）+ 审核 finish 次数规则。每项独立断言入口身份、共享/专用处理器、
// 审核分支与持久化写入，"提交→审核→完成"的连接必须来自实际调用链。

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// formCase 一个流程形态的期望。
type formCase struct {
	num        int
	name       string
	scene      string // 默认 Source=7
	wantFinish int    // FinishReview 次数（人工完成审核）
	wantAuto   bool   // 是否出现 AutoApprove（自动审核通过）
	eventually []string
	never      []string
}

// 公共链：每个形态都应从入口到 RPC 服务端、装饰器/事务、存储（除纯审核形态）。
var commonChain = []string{
	"SaveTrace", "ApplyRemote", "(AccountServer).Apply", "tx.Run", "BeforeFirst", "AfterFirst",
}

func TestTodo22FlowForms(t *testing.T) {
	if !scipGoAvailable() {
		t.Skip("scip-go not found")
	}
	bin, dir := flowMatrixCLI(t)
	const pkg = "symbol:go:example.com/flowmatrix:"

	cases := []formCase{
		// 01–03 Web：提交 RPC → 申请持久化 → 资料审核 → 服务审核 → 完成
		{1, "FlowWebNewEntity", "Source=7", 2, false,
			append(commonChain, "(App).webSubmit", "FinishDataReview", "FinishServiceReview",
				"PersistDataReview", "PersistServiceReview", "ApproveTrace"),
			[]string{"(App).appSubmit"}},
		{2, "FlowWebNewService", "Source=7", 2, false,
			append(commonChain, "(App).webSubmit", "FinishDataReview", "FinishServiceReview",
				"(Store).SaveService", "ApproveTrace"), []string{"(App).appSubmit"}},
		{3, "FlowWebNewChild", "Source=7", 2, false,
			append(commonChain, "(App).webSubmit", "FinishDataReview", "FinishServiceReview",
				"(Store).InsertChild", "ApproveTrace"), []string{"(App).appSubmit"}},
		// 04–06 App：入口与 Web 区分，后端共享链不串源
		{4, "FlowAppNewEntity", "Source=7", 2, false,
			append(commonChain, "(App).appSubmit", "FinishDataReview", "FinishServiceReview", "ApproveTrace"),
			[]string{"(App).webSubmit"}},
		{5, "FlowAppNewService", "Source=7", 2, false,
			append(commonChain, "(App).appSubmit", "(Store).SaveService", "ApproveTrace"),
			[]string{"(App).webSubmit"}},
		{6, "FlowAppNewChild", "Source=7", 2, false,
			append(commonChain, "(App).appSubmit", "(Store).InsertChild", "ApproveTrace"),
			[]string{"(App).webSubmit"}},
		// 07–09 Partner App：来源分派（A 自动 / B 人工）
		{7, "FlowPartnerNewEntity", "Source=7", 0, true,
			append(commonChain, "(App).partnerSubmit", "AutoApprove"), []string{"FinishDataReview"}},
		{8, "FlowPartnerNewService", "Source=7", 0, true,
			append(commonChain, "(App).partnerSubmit", "AutoApprove", "(Store).SaveService"),
			[]string{"FinishDataReview"}},
		{9, "FlowPartnerNewChild", "Source=7", 0, true,
			append(commonChain, "(App).partnerSubmit", "AutoApprove", "(Store).InsertChild"),
			[]string{"FinishDataReview"}},
		// 10 编辑子项：审核完成与基本资料实际更新分别证明
		{10, "FlowEditChild", "Source=7", 1, false,
			append(commonChain, "FinishDataReview", "PersistDataReview", "(Store).UpdateChild"), nil},
		// 11 App 新银行账户：自动/人工两条路径 + 状态与主数据分别证明
		{11, "FlowAppNewBankAccount", "Source=7", 0, true,
			append(commonChain, "(Store).SaveAccount", "(Store).UpdateEntityState"), []string{"FinishDataReview"}},
		// 12–13 编辑主体（App/Web）：共享审核 + 实际更新
		{12, "FlowAppEditEntity", "Source=7", 1, false,
			append(commonChain, "(App).appSubmit", "FinishServiceReview", "(Store).UpdateEntity"),
			[]string{"(App).webSubmit"}},
		{13, "FlowWebEditEntity", "Source=7", 1, false,
			append(commonChain, "(App).webSubmit", "FinishServiceReview", "(Store).UpdateEntity"),
			[]string{"(App).appSubmit"}},
		// 14–16 三种宿主的新银行账户：宿主专用处理器 + 共享处理器 + 审核分支
		{14, "FlowHostANewBankAccount", "Source=7", 0, true,
			append(commonChain, "(App).hostAHandler", "(App).hostApply", "(Store).SaveAccount"),
			[]string{"hostBHandler", "hostCHandler", "FinishDataReview"}},
		{15, "FlowHostBNewBankAccount", "Source=7", 2, false,
			append(commonChain, "(App).hostBHandler", "(App).hostApply", "(Store).SaveAccount",
				"FinishDataReview", "FinishServiceReview"), []string{"hostAHandler", "hostCHandler"}},
		{16, "FlowHostCNewBankAccount", "Source=7", 1, false,
			append(commonChain, "(App).hostCHandler", "(App).hostApply", "(Store).SaveAccount",
				"FinishDataReview"), []string{"hostAHandler", "hostBHandler", "FinishServiceReview"}},
		// 17 支付 App 新实体：条件自动审核 + 人工资料审核 + 注册状态同步
		{17, "FlowPaymentNewEntity", "Source=7", 1, true,
			append(commonChain, "AutoApprove", "FinishDataReview", "SyncRegistry"),
			[]string{"FinishServiceReview"}},
		// 18 宿主转入的新服务：提交 → 资料审核 → 外部注册/回调 → 申请完成
		{18, "FlowHostTransferNewService", "Source=7", 1, false,
			append(commonChain, "(App).hostTransferSubmit", "FinishDataReview", "SyncRegistry",
				"(Store).SaveService", "ApproveTrace"), []string{"FinishServiceReview"}},
		// 19–20 API：有审核流程 / 直接主数据（不虚构审核节点）
		{19, "FlowAPINewEntityReview", "Source=7", 2, false,
			append(commonChain, "(App).apiSubmit", "FinishDataReview", "FinishServiceReview"), nil},
		{20, "FlowAPINewChildDirect", "Source=7", 0, false,
			append(commonChain, "(App).apiSubmit", "(Store).InsertChild"),
			[]string{"FinishReview", "FinishDataReview", "FinishServiceReview", "AutoApprove"}},
	}
	// 分支形态补 Source=19（人工路径）：07–09 与 11 人工两次；17 人工一次。
	manualOnly := func(c formCase) formCase {
		c.scene = "Source=19"
		c.wantAuto = false
		c.never = nil
		var keep []string
		for _, w := range c.eventually {
			if w != "AutoApprove" {
				keep = append(keep, w)
			}
		}
		c.eventually = keep
		return c
	}
	for _, i := range []int{6, 7, 8, 10} {
		c := manualOnly(cases[i])
		c.wantFinish = 2
		cases = append(cases, c)
	}
	cases = append(cases, manualOnly(cases[16]))

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%02d-%s", tc.num, tc.scene), func(t *testing.T) {
			code, out := flowRun(t, bin, "query", "sequence", pkg+"(App)."+tc.name,
				"--repo", dir, "--code", "--depth", "24", "--scene", tc.scene, "--json")
			if code != 0 {
				t.Fatalf("sequence exit=%d\n%s", code, out)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(out), &root); err != nil {
				t.Fatalf("JSON: %v\n%s", err, out)
			}
			var evs []seqEv
			collectSeqEv(root, &evs)
			syms := map[string]int{}
			for _, e := range evs {
				syms[e.sym]++
			}
			// 有些节点只有标签（MakeFirst 作变参装饰器元素、tx.Run 的符号在
			// tx 模块）——按符号或标签后缀匹配。
			requireSym := func(suffix string) {
				t.Helper()
				for s := range syms {
					if strings.HasSuffix(s, suffix) {
						return
					}
				}
				for _, e := range evs {
					if strings.HasSuffix(e.label, suffix) {
						return
					}
				}
				t.Errorf("链路缺少 %s", suffix)
			}
			for _, want := range tc.eventually {
				requireSym(want)
			}
			for _, bad := range tc.never {
				for s := range syms {
					if strings.HasSuffix(s, bad) {
						t.Errorf("不应出现 %s（%s）", bad, s)
					}
				}
			}
			// 人工 finish 次数（FinishReview 是唯一的"人工完成审核"入口）
			if got := syms[pkg+"FinishReview"]; got != tc.wantFinish {
				t.Errorf("人工 FinishReview = %d，期望 %d", got, tc.wantFinish)
			}
			// 资料/服务节点各一次且身份不同（不同持久化行；node_id 101/202）
			if tc.wantFinish >= 2 {
				for _, want := range []string{"FinishDataReview", "PersistDataReview",
					"FinishServiceReview", "PersistServiceReview"} {
					if syms[pkg+want] == 0 {
						t.Errorf("两次人工审核缺 %s", want)
					}
				}
			}
			// 自动审核分支不得调用人工完成
			if tc.wantAuto {
				if syms[pkg+"AutoApprove"] == 0 {
					t.Errorf("应出现自动审核 AutoApprove")
				}
			} else if syms[pkg+"AutoApprove"] != 0 {
				t.Errorf("人工路径不应出现 AutoApprove")
			}
			// 外部注册状态同步是自动步骤（不增加人工 finish）
			if tc.num == 17 || tc.num == 18 {
				if syms[pkg+"SyncRegistry"] == 0 {
					t.Errorf("应出现外部注册状态同步 SyncRegistry")
				}
				if syms[pkg+"FinishReview"] != 1 {
					t.Errorf("注册同步不得增加人工 finish（当前 %d）", syms[pkg+"FinishReview"])
				}
			}
		})
	}
}
