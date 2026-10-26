package action

import "strings"

// remoteTarget 跨仓续接目标：仓库 + 服务端方法入口 + 匹配依据。
type remoteTarget struct {
	acts      *Actions
	repo      string
	methodID  string
	serviceID string // 目标服务节点 canonical ID（proto 包 + 服务）
	reason    string // proto-identity | proto-name | go-method
	// todo12-2：目标查询视图的所有权——由调用方在递归结束后 release
	closeFn func() error
	diags   []SeqDiagCount // 候选探测/打开失败的诊断（目标可用但搜索不完整）
}

// resolveRemoteMethod 在"当前仓库 + 注册表候选仓库"里找该服务的**同名 RPC
// 方法**实现（起始仓库优先）。找不到时返回带 code 的诊断（方法缺失时附
// 该服务的方法清单，便于判断是没实现还是名字不同）。
func resolveRemoteMethod(req ExtChainRequest, startActs *Actions, startRepo, svcID, svc, rpcMethod string) (*remoteTarget, *SeqStop) {
	type repoView struct {
		acts    *Actions
		repo    string
		closeFn func() error
	}
	views := []repoView{{startActs, startRepo, nil}}
	searched := []string{startRepo}
	var diags []SeqDiagCount
	if req.Open != nil {
		for _, p := range req.CandidateRepos {
			if p == "" || p == startRepo {
				continue
			}
			acts, closeFn, err := req.Open(p)
			if err != nil || acts == nil {
				searched = append(searched, p+"（不可用）")
				detail := p
				if err != nil {
					detail += "：" + err.Error()
				}
				diags = append(diags, SeqDiagCount{Code: ExtStopRepoMissing, Count: 1, Detail: "候选仓库打开失败（" + detail + "）"})
				continue
			}
			views = append(views, repoView{acts, p, closeFn})
			searched = append(searched, p)
		}
	}

	selected := -1
	defer func() {
		for i, v := range views {
			if i == selected || v.closeFn == nil {
				continue
			}
			_ = v.closeFn()
		}
	}()

	serviceSeen, implFound := false, false
	var available []string
	// todo13-16：按完整服务身份优先消歧——先收集全部匹配方法，再分两档：
	//   exact = 服务节点 ID 完全一致（proto 包 + 服务）
	//   name  = 仅短服务名一致（无完整身份时的回退，多候选不静默取首个）
	type implCandidate struct {
		svc    GrpcRouteService
		vi     int
		goName string
		method string
	}
	var exact, byName []implCandidate
	for vi, v := range views {
		res, err := v.acts.GrpcRoutes(v.repo)
		if err != nil {
			diags = append(diags, SeqDiagCount{Code: ExtStopQueryFailed, Count: 1,
				Detail: "读取仓库路由失败（" + v.repo + "）：" + err.Error()})
			continue
		}
		for _, s := range res.Services {
			if !strings.EqualFold(s.Name, svc) {
				continue
			}
			serviceSeen = true
			if s.ImplID == "" {
				continue
			}
			implFound = true
			for _, m := range s.Methods {
				goName := GrpcHandlerGoMethod(m.Handler, s.Name)
				if goName == "" {
					goName = m.Name
				}
				available = append(available, m.Name)
				if !strings.EqualFold(rpcMethod, m.Name) && !strings.EqualFold(rpcMethod, goName) {
					continue
				}
				cand := implCandidate{svc: s, vi: vi, goName: goName, method: m.Name}
				if svcID != "" && s.ID == svcID {
					exact = append(exact, cand)
				} else {
					byName = append(byName, cand)
				}
			}
		}
	}
	picked, reason := exact, "proto-identity"
	if len(picked) == 0 {
		picked, reason = byName, "proto-name"
	}
	if len(picked) == 1 {
		c := picked[0]
		selected = c.vi
		v := views[c.vi]
		return &remoteTarget{acts: v.acts, repo: v.repo, serviceID: c.svc.ID,
			methodID: GrpcMethodEntryID(c.svc.ImplID, c.goName), reason: reason,
			closeFn: v.closeFn, diags: diags}, nil
	}
	if len(picked) > 1 {
		// 多候选：列出各自服务身份与仓库；不静默任选。
		var cs []string
		seenCand := map[string]bool{}
		for _, c := range picked {
			id := c.svc.ID
			if id == "" {
				id = "（缺服务 ID：仅短名）"
			}
			line := views[c.vi].repo + " → " + id
			if !seenCand[line] {
				seenCand[line] = true
				cs = append(cs, line)
			}
		}
		detail := "服务 " + svc + " 有多个实现候选，缺少可唯一匹配的完整身份"
		if svcID == "" {
			detail += "（调用侧未记录服务节点 ID）"
		} else {
			detail += "（调用侧身份 " + svcID + " 不在候选范围）"
		}
		detail += "；候选：" + strings.Join(cs, " | ")
		return nil, &SeqStop{Code: ExtStopServiceAmbiguous, Detail: detail}
	}
	if !implFound {
		detail := "没有任何仓库实现服务 " + svc + "（已查：" + strings.Join(searched, ", ") + "）"
		if serviceSeen {
			detail = "服务 " + svc + " 只有调用侧节点、无服务端实现（已查：" + strings.Join(searched, ", ") + "）"
		}
		return nil, &SeqStop{Code: ExtStopRepoMissing, Detail: detail}
	}
	detail := "服务 " + svc + " 无方法 " + rpcMethod
	if len(available) > 0 {
		detail += "（已实现：" + strings.Join(uniqueStrings(available), ", ") + "）"
	}
	return nil, &SeqStop{Code: ExtStopMethodNotFound, Detail: detail}
}

// uniqueStrings 去重（保持顺序）——诊断里的方法清单用。
func uniqueStrings(xs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
