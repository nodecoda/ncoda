package ir

// builtin_result_port_gate_test.go —— R1 第二半判据：结果端口名的**唯一声明点**。
//
// 为什么需要它：R1 实测的失效模式不是「没有表」，而是「表只被 7 处读」。
// 表建好之后仍会在消费者里长出第二份字面量副本，副本不同步就是 C2-H6 /
// A12-H5（`status_code ⇄ statusCode`）那类「编译发的名字 ≠ 反编译认的名字」。
//
// 两条判据（缺一都会被错规则满足）：
//
//	① **对账面**：IR 声明的 http 结果口集 == 内建契约声明的口集。正向锚点
//	   （每个声明口都必须解析出语言类型）与**负向锚点**（契约外的名字必须解析
//	   失败，如 dify 专属的 `files`）同时锁；只锁正向会被「一律接受」满足，
//	   只锁负向会被「一律拒绝」满足。
//	② **文本面**：结果口名不得在消费者包里以**字符串字面量**出现，除非命中
//	   豁免表（豁免 = 另一套词表：http() 的请求配置键、平台请求参数键）。
//	   豁免必须**被命中**（没有命中的豁免 = 腐烂的豁免 ⇒ 红）——否则豁免表会
//	   像旧账本一样慢慢变成一句空话。
//
// 扫描用 go/scanner 取 STRING token：注释里的名字不是字面量，不误报（这也是
// 判据自身的负向自检：撤回实现改动后，junction_phi.go 的 `"body", "headers"`
// 会重新出现并被本测试报出）。

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/builtin"
)

// httpResultPortNames 是 http 结果端口的名字面量（口名 ∪ 语言字段名）。
// 以契约为权威枚举，避免本门禁自带一份会腐烂的清单。
func httpResultPortNames(t *testing.T) []string {
	t.Helper()
	set := map[string]bool{}
	for _, p := range builtin.PortsOf(IRPortVocabularyTarget(), "http") {
		if p.Port != "" {
			set[p.Port] = true
		}
		if p.Lang != "" {
			set[p.Lang] = true
		}
	}
	if len(set) == 0 {
		t.Fatalf("内建契约未声明 %s 的 http 口：IR 的端口词汇表以契约为权威", IRPortVocabularyTarget())
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// sameIRShape 比较本门禁用得到的类型形状（Kind + object 的字段名集）。
func sameIRShape(a, b *Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	if !sameIRShape(a.Elem, b.Elem) {
		return false
	}
	if len(a.Props) != len(b.Props) {
		return false
	}
	for k, av := range a.Props {
		if !sameIRShape(av, b.Props[k]) {
			return false
		}
	}
	return true
}

// ① 对账面：IR 的 http 端口解析面 == 契约声明面（正向 + 负向两侧）。
func TestHTTPResultPortVocabularyMatchesContract(t *testing.T) {
	ports := builtin.PortsOf(IRPortVocabularyTarget(), "http")
	if len(ports) == 0 {
		t.Fatalf("内建契约未声明 %s 的 http 口：IR 的端口词汇表以契约为权威", IRPortVocabularyTarget())
	}
	for _, p := range ports {
		byLang, okLang := httpResultPortType(p.Lang)
		byPort, okPort := httpResultPortType(p.Port)
		if !okLang || !okPort {
			t.Fatalf("契约口 %q/%q 必须能解析出 IR 语言类型（lang=%v port=%v）",
				p.Port, p.Lang, okLang, okPort)
		}
		if !sameIRShape(byLang, byPort) {
			t.Fatalf("口名与语言名必须解析到同一语言类型：%q ⇒ %+v，%q ⇒ %+v",
				p.Port, byPort, p.Lang, byLang)
		}
	}
	// IR 端口名 = 契约的**物理口名**（IRPortVocabularyTarget() 的口径）。
	if got := httpResultPortName("body"); got != ports[0].Port {
		if _, ok := builtin.PortOf(IRPortVocabularyTarget(), "http", "body"); ok {
			t.Fatalf("语言字段 body 的 IR 端口名必须取自契约，实得 %q", got)
		}
	}
	// 负向锚点：契约外的名字一律不得解析（`files` = dify 平台专属端口，语言层没有它）。
	for _, name := range []string{"files", "errorBody", "result", "output", "text", "response"} {
		if _, ok := httpResultPortType(name); ok {
			t.Fatalf("%q 不是 http 的语言结果口，必须解析失败（解析成功 = 凭空发明字段）", name)
		}
	}
}

// 消费者包：结果口名的**读取方**（IR 建图 / coze-biz lowering / coze-biz raising）。
// coze-studio 后端（internal/backend/coze）按平台优先级搁置，不在扫描面内。
var httpResultPortCensusScope = []string{
	"pkg/ir",
	"internal/backend/cozebiz",
	"platforms/cozebiz/decompile",
}

// 豁免 = 命中的是**另一套词表**或**声明点自身**。needle 必须真的命中（见测试尾部）。
var httpResultPortCensusExemptions = map[string]string{
	"pkg/ir/builtin_result_ports.go":                           "http 结果口语言类型的唯一声明点（本门禁保护的对象）",
	"pkg/ir/http_config_keys.go":                               "http() 的 config map 键词表（请求配置，不是结果口）",
	"pkg/ir/build_call.go":                                     "http() config map 的键分派（请求配置，不是结果口）",
	"pkg/ir/build_expr.go":                                     "合成 stdlib 管线按**语言字段名**取 http 结果口（契约里该口名与语言名同形）",
	"internal/backend/cozebiz/render_node.go":                  "wire 的请求参数键（body/headers 请求体与请求头，不是结果口）",
	"platforms/cozebiz/decompile/convert.go":                   "从 wire 重建 http() config map 的键（请求配置，不是结果口）",
	"platforms/cozebiz/decompile/http_body_type_audit.go":      "逐字读平台 `parameters.body` 的**请求参数键**（RS-TYP-005 报账通道；键名面同上两条 wire 词表，不是结果口）",
	"platforms/cozebiz/decompile/node_param_key_vocabulary.go": "平台**节点入参键**词汇表（HTTPRequester 的请求体/请求头键 `body`/`headers`，方向与结果口相反 —— 一个是发出去的请求参数，一个是读回来的响应字段；不是结果口）",
}

func scanStringLiterals(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile(path, fset.Base(), len(src)), src, nil, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.STRING {
			if v, err := strconv.Unquote(lit); err == nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// ② 文本面：结果口名的字面量只允许出现在声明点与豁免的「另一套词表」处。
func TestHTTPResultPortNamesHaveSingleDeclaration(t *testing.T) {
	names := httpResultPortNames(t)
	root := filepath.Join("..", "..")
	hits := map[string][]string{}
	used := map[string]bool{}

	for _, pkg := range httpResultPortCensusScope {
		dir := filepath.Join(root, pkg)
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			continue // 该目录不在本模块（O2 开放/闭源拆分）
		}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			rel = filepath.ToSlash(rel)
			lits := scanStringLiterals(t, path)
			for _, name := range names {
				n := 0
				for _, lit := range lits {
					if lit == name {
						n++
					}
				}
				if n == 0 {
					continue
				}
				if _, ok := httpResultPortCensusExemptions[rel]; ok {
					used[rel] = true
					continue
				}
				hits[rel] = append(hits[rel], fmt.Sprintf("%q×%d", name, n))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(hits) > 0 {
		keys := make([]string, 0, len(hits))
		for k := range hits {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "\n  %s: %s", k, strings.Join(hits[k], ", "))
		}
		t.Fatalf("http 结果端口名只允许有一个声明点（pkg/ir/builtin_result_ports.go "+
			"+ 契约声明源）与「另一套词表」的豁免项，实得多处副本：%s\n"+
			"（新增副本 ⇒ 表与消费者会漂移，见本文件头注）", b.String())
	}

	// 豁免表不得腐烂：没有命中的豁免 = 被豁免的副本已经消失（或路径写错）⇒ 红。
	for file, reason := range httpResultPortCensusExemptions {
		if _, statErr := os.Stat(filepath.Join(root, file)); os.IsNotExist(statErr) {
			continue // 豁免的文件不在本模块（O2 开放/闭源拆分）
		}
		if !used[file] {
			t.Fatalf("豁免项已失效（该文件里不再有结果口名字面量，或路径/理由过期）：%s —— %s\n"+
				"请删除该豁免（豁免表腐烂 = 判据慢慢变成空话）", file, reason)
		}
	}
}
