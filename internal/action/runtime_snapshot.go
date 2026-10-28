package action

// todo25 §13.1：运行时快照（通用 v2 / v1 归一化）的加载与严格校验。
// 纯离线：不连接数据库、不保存凭据；所有失败都在分析前返回，不产出半棵树。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	runtimeSnapshotMaxBytes = 8 << 20
	runtimeSnapshotMaxRows  = 50000
)

// RuntimeSnapshot 归一化后的快照事实（v1/v2 共用同一内部模型）。
type RuntimeSnapshot struct {
	Version      int
	Context      map[string]rtValue
	Sources      map[string]string // id → kind
	Bindings     map[string]string // target symbol → source id
	Observations []*runtimeObservation
	Hash         string
	Path         string
}

type runtimeObservation struct {
	SourceID   string
	Table      string
	Scope      []runtimePredicate
	Complete   bool
	Columns    map[string]bool
	Rows       []rtValue // 每项 rtRow
	ObservedAt string
	Revision   string
}

type runtimePredicate struct {
	Field string
	Op    string
	Value rtValue
}

// LoadRuntimeSnapshot 读取并校验快照（严格未知字段/重复键/重复身份）。
func LoadRuntimeSnapshot(path string) (*RuntimeSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--runtime-data 需要非空路径")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("运行时快照不可读: %s: %w", path, err)
	}
	if fi.Size() > runtimeSnapshotMaxBytes {
		return nil, fmt.Errorf("运行时快照超过 %d MiB 限制: %s", runtimeSnapshotMaxBytes>>20, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("运行时快照不可读: %s: %w", path, err)
	}
	if err := checkJSONDuplicateKeys(data); err != nil {
		return nil, fmt.Errorf("运行时快照 JSON 无效: %w", err)
	}
	var head map[string]json.RawMessage
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("运行时快照结构无效: %w", err)
	}
	rawVersion, ok := head["version"]
	if !ok {
		return nil, errors.New("运行时快照缺少 version")
	}
	var version int
	if err := json.Unmarshal(rawVersion, &version); err != nil {
		return nil, fmt.Errorf("运行时快照 version 无效: %w", err)
	}
	var snap *RuntimeSnapshot
	switch version {
	case 2:
		snap, err = parseRuntimeV2(data)
	case 1:
		snap, err = parseRuntimeV1(data)
	default:
		return nil, fmt.Errorf("不支持的运行时快照版本 %d（支持 1/2）", version)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	snap.Hash = hex.EncodeToString(sum[:])
	snap.Path = path
	if err := snap.validate(); err != nil {
		return nil, err
	}
	return snap, nil
}

// ValidateRuntimeData 供 CLI 在打开数据库前调用（验证逻辑仍在 action）。
func ValidateRuntimeData(path string) error {
	_, err := LoadRuntimeSnapshot(path)
	return err
}

func (s *RuntimeSnapshot) validate() error {
	if len(s.Sources) == 0 && len(s.Observations) > 0 {
		return errors.New("运行时快照含 observation 但没有 sources")
	}
	for sym, src := range s.Bindings {
		if strings.TrimSpace(sym) == "" {
			return errors.New("运行时快照 binding.target.symbol 为空")
		}
		if _, ok := s.Sources[src]; !ok {
			return fmt.Errorf("binding %s 引用未知 source %q", sym, src)
		}
	}
	seen := map[string]bool{}
	for _, obs := range s.Observations {
		key := obs.identityKey()
		if seen[key] {
			// U01：scope 归一化（IN 去重 + 稳定排序）后相同即视为重复——两份
			// 冲突/重复 observation 不得拼成一份结果。
			return fmt.Errorf("runtime-data: duplicate normalized scope (source=%s, table=%s, %s)",
				obs.SourceID, obs.Table, obs.scopeDisplay())
		}
		seen[key] = true
	}
	return nil
}

// decodeStrictJSON 拒绝未知结构字段（rows/context 内部键由各自校验处理）。
func decodeStrictJSON(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("JSON 后存在多余内容")
		}
		return err
	}
	return nil
}

// checkJSONDuplicateKeys 递归拒绝任何对象里的重复键（encoding/json 默认后者覆盖）。
func checkJSONDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := checkJSONValue(dec, "$"); err != nil {
		return err
	}
	return ensureJSONEOF(dec)
}

func checkJSONValue(dec *json.Decoder, path string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyTok.(string)
			if seen[key] {
				return fmt.Errorf("%s 重复键 %q", path, key)
			}
			seen[key] = true
			if err := checkJSONValue(dec, path+"."+key); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		i := 0
		for dec.More() {
			if err := checkJSONValue(dec, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
			i++
		}
		_, err := dec.Token()
		return err
	}
	return nil
}

func decodeJSONValue(raw json.RawMessage) (rtValue, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return rtValue{}, err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return rtValue{}, err
	}
	return rtFromJSON(v)
}

func decodeJSONRows(raws []map[string]json.RawMessage) ([]rtValue, map[string]bool, error) {
	rows := make([]rtValue, 0, len(raws))
	cols := map[string]bool{}
	for i, raw := range raws {
		colsMap := map[string]rtValue{}
		order := make([]string, 0, len(raw))
		for k, rv := range raw {
			v, err := decodeJSONValue(rv)
			if err != nil {
				return nil, nil, fmt.Errorf("第 %d 行列 %s: %w", i+1, k, err)
			}
			colsMap[k] = v
			cols[k] = true
			order = append(order, k)
		}
		sort.Strings(order)
		rows = append(rows, rtRowValue(colsMap, order))
	}
	return rows, cols, nil
}

func runtimeTimeValid(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}
