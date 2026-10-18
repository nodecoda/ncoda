package tool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nodecoda/ncoda/pkg/ast"
	"github.com/nodecoda/ncoda/pkg/parser"
	"github.com/nodecoda/ncoda/pkg/token"
)

// CapabilityHeaderExt is the extension of ncoda capability header files
// (the registry-as-source format). One file declares exactly one package;
// the file name (stem) is the provider slug.
const CapabilityHeaderExt = ".ncoda"

// LoadCapabilityDir loads a Tool capability registry from a directory of
// ncoda capability header files — the language-homogeneous replacement for
// the YAML registry. A header is an ordinary ncoda source restricted to
// declarations:
//
//	// capabilities/tts.ncoda  ->  provider = tts (file name)
//	type TTSData = { duration: float; ... };
//	type TTSResult = { data: TTSData; status_code: float; ... };
//
//	// empty body = declaration only (the C header analogy: signature, no impl)
//	@capability retry=true, failure_branch=true
//	function speech_synthesis(any text, string language, ...) -> TTSResult {}
//
// The @capability annotation (header-only, stripped before parsing so the
// ncoda language stays untouched) declares retry / failure_branch support.
//
// Mapping rules:
//   - file name stem                    -> provider
//   - empty-body `function <op>(...)`   -> one operation (api)
//   - parameter `<type> <name> [= d]`   -> Parameter{Type, Name, Required: no default}
//   - return type is a named `type` graph, flattened into dotted output paths
//     (array/map elements stop the flattening, mirroring the YAML registry)
//
// LoadCapabilityDir 批量加载一个目录下的能力头文件。支持两种形态：
//  1. 规范根（--tool-path 指向的根）：自动下钻 <root>/capabilities-ncoda/
//     子目录（CapabilityHeadersDir）——根本身无 *.ncoda 时启用；
//  2. 旧平铺：<dir>/<provider>.ncoda 直接平铺（向后兼容）。
//
// 与按 provider 的 loadCapabilityPackage 保持同一布局语义。
func LoadCapabilityDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("tool capability dir %q: %w", dir, err)
	}
	flat := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := capabilityHeaderFileName(e.Name()); ok {
			flat = true
			break
		}
	}
	if !flat {
		sub := filepath.Join(dir, CapabilityHeadersDir)
		if fi, serr := os.Stat(sub); serr == nil && fi.IsDir() {
			dir = sub
			entries, err = os.ReadDir(dir)
			if err != nil {
				return nil, fmt.Errorf("tool capability dir %q: %w", dir, err)
			}
		}
	}
	definitions := make([]Definition, 0)
	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		stem, ok := capabilityHeaderFileName(entry.Name())
		if !ok {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("tool capability dir %q: reading %s: %w", dir, entry.Name(), err)
		}
		cleaned, policies, err := stripCapabilityAnnotations(string(data), entry.Name())
		if err != nil {
			return nil, fmt.Errorf("tool capability dir %q: %s: %w", dir, entry.Name(), err)
		}
		program, parseErrors := parser.ParseSource(cleaned, entry.Name())
		if len(parseErrors) > 0 {
			return nil, fmt.Errorf("tool capability dir %q: %s: parse error: %s",
				dir, entry.Name(), parseErrors[0].Message)
		}
		fileDefs, err := capabilityDefinitionsFromProgram(stem, program, policies)
		if err != nil {
			return nil, fmt.Errorf("tool capability dir %q: %s: %w", dir, entry.Name(), err)
		}
		if len(fileDefs) == 0 {
			return nil, fmt.Errorf("tool capability dir %q: %s: no api declarations found", dir, entry.Name())
		}
		definitions = append(definitions, fileDefs...)
		loaded++
	}
	if loaded == 0 {
		return nil, fmt.Errorf("tool capability dir %q: %w", dir, ErrNoCapabilityHeaders)
	}
	registry, err := NewRegistry(definitions...)
	if err != nil {
		return nil, fmt.Errorf("tool capability dir %q: %w", dir, err)
	}
	return registry, nil
}

// ErrNoCapabilityHeaders 表示目录存在但没有任何能力头文件（*.ncoda）。
// 搜索链（LoadCapabilitySearchPaths）中视为"非能力目录"，静默跳过；
// 直接加载（LoadCapabilityDir）时仍是硬错误——显式指向的目录必须可加载。
var ErrNoCapabilityHeaders = errors.New("no capability header files found")

// LoadCapabilitySearchPaths 按 C 头文件搜索模型（CPATH/PYTHONPATH）从
// 优先级从高到低的路径链加载能力注册表：
//
//  1. 每个路径按序 LoadCapabilityDir（规范根自动下钻 capabilities-ncoda/，
//     旧平铺兼容）；
//  2. 同 (provider, operation) 取第一个命中（first-wins shadowing：高优先
//     级路径的 provider 覆盖低优先级同名 provider）；
//  3. 缺失目录 / 非能力目录（无头文件）静默跳过——陈旧 env 条目不算错误；
//  4. 目录存在但头文件损坏（解析错误等）硬报错——损坏的真实内容必须修复。
//
// 与编译端 LoadCapabilityPackages 的逐 provider 扫描（loadCapabilityPackage）
// 保持同一 first-wins 语义；本函数是全量合并（反编译端需要整表 lookup，
// 运行时才知道工作流里会出现哪些 provider）。
func LoadCapabilitySearchPaths(paths []string) (*Registry, error) {
	seen := make(map[string]bool)
	var defs []Definition
	for _, dir := range paths {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue // 缺失目录静默跳过（CPATH 语义）
		}
		reg, err := LoadCapabilityDir(dir)
		if err != nil {
			if errors.Is(err, ErrNoCapabilityHeaders) {
				continue // 存在但非能力目录，跳过
			}
			return nil, err
		}
		for _, d := range reg.Definitions() {
			key := SymbolKey(d.Provider, d.Operation)
			if seen[key] {
				continue
			}
			seen[key] = true
			defs = append(defs, d)
		}
	}
	if len(defs) == 0 {
		// 整条链没有能力头文件：返回空注册表（nil 注册表语义由调用方保持）
		return nil, nil
	}
	registry, err := NewRegistry(defs...)
	if err != nil {
		return nil, fmt.Errorf("tool capability search paths %v: %w", paths, err)
	}
	return registry, nil
}

func capabilityHeaderFileName(name string) (string, bool) {
	if strings.HasSuffix(name, CapabilityHeaderExt) {
		stem := strings.TrimSuffix(name, CapabilityHeaderExt)
		if stem != "" {
			return stem, true
		}
	}
	return "", false
}

func capabilityDefinitionsFromProgram(provider string, program *ast.Program, policies map[string]capabilityPolicies) ([]Definition, error) {
	if program.Entry() != nil {
		return nil, fmt.Errorf("capability header must not declare a main function")
	}
	if len(program.Imports()) > 0 {
		return nil, fmt.Errorf("capability header must not import other packages")
	}
	if len(program.ConstDecls()) > 0 || len(program.EnumDecls()) > 0 ||
		len(program.CodeFunctions()) > 0 || len(program.ConversationDecls()) > 0 {
		return nil, fmt.Errorf("capability header allows only `type` and empty-body `function` declarations")
	}

	types := make(map[string][]*ast.FieldDecl, len(program.TypeDecls()))
	for _, td := range program.TypeDecls() {
		if _, exists := types[td.Name()]; exists {
			return nil, fmt.Errorf("duplicate type %q", td.Name())
		}
		types[td.Name()] = td.Fields()
	}

	definitions := make([]Definition, 0, len(program.Functions()))
	for _, fn := range program.Functions() {
		if len(fn.Body().Stmts()) != 0 {
			return nil, fmt.Errorf("function %q must have an empty body (capability header is a declaration)", fn.Name())
		}
		definition := Definition{
			Provider:    provider,
			Operation:   fn.Name(),
			Description: "",
		}
		if p, ok := policies[fn.Name()]; ok {
			definition.SupportsRetry = p.retry
			definition.SupportsFailureBranch = p.failureBranch
			definition.TimeoutMS = p.timeoutMS
			definition.Idempotent = p.idempotent
		}
		for _, param := range fn.Params() {
			paramType, err := typeRefToTypeString(param.TypeRef())
			if err != nil {
				return nil, fmt.Errorf("function %q parameter %q: %w", fn.Name(), param.Name(), err)
			}
			definition.Parameters = append(definition.Parameters, Parameter{
				Name:     param.Name(),
				Type:     paramType,
				Required: param.Default() == nil,
			})
		}
		if fn.ReturnType() == nil {
			// 没有 `-> T` = **输出表这一栏根本不存在**（R-29，2026-09-24 用户
			// 裁定「收紧」）：输出表是头文件的义务（类比 C 函数声明缺返回类型
			// = 声明方的错误）⇒ 标为未声明，调用点 fail loud，不再静默回落
			// `{result: any}`。
			definition.OutputsUndeclared = true
		} else if prim, ok := fn.ReturnType().(*ast.PrimTypeRef); ok && prim.Name() == "void" {
			// `-> void` = **显式声明空输出表**（本次调用没有结果值，如
			// time_waiter 这类纯等待 op）。void 是 ncoda 既有类型关键字
			// （types.VoidType；`-> void` 早已在普通函数声明上合法，见
			// parser.parseTypeRef 的 KW_VOID），此处只是不再把头文件里的
			// 合法拼写误判成「void is not a value type」。空输出表的地是
			// void 结果，不是凭空捏造的 `{result: any}` 字段。（polarity：本字段
			// 零值 = 已声明 ⇒ 这一支不写任何东西。）
		} else {
			outputs, err := flattenOutputs(fn.ReturnType(), "", types, make(map[string]bool))
			if err != nil {
				return nil, fmt.Errorf("function %q return type: %w", fn.Name(), err)
			}
			definition.Outputs = outputs
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// typeRefToTypeString renders a type reference into the registry's semantic
// type name (primitive name, array<T>, map<string,T>, file).
func typeRefToTypeString(ref ast.TypeRef) (string, error) {
	switch r := ref.(type) {
	case *ast.PrimTypeRef:
		switch r.Name() {
		case "void":
			return "", fmt.Errorf("void is not a value type")
		}
		return r.Name(), nil
	case *ast.ArrayTypeRef:
		elem, err := typeRefToTypeString(r.Element())
		if err != nil {
			return "", err
		}
		return "array<" + elem + ">", nil
	case *ast.MapTypeRef:
		val, err := typeRefToTypeString(r.Value())
		if err != nil {
			return "", err
		}
		return "map<string," + val + ">", nil
	case *ast.FileTypeRef:
		return fileRegistryTypeString(r.Constraints()), nil
	case *ast.OptionalTypeRef:
		return "", fmt.Errorf("optional type is not a registry type")
	case *ast.NamedTypeRef:
		return "", fmt.Errorf("named type %q is only valid as a struct output, not a leaf type", r.Name())
	default:
		return "", fmt.Errorf("unsupported type %T", ref)
	}
}

// fileRegistryTypeString 把 file 类型的**约束轴**带进注册表类型串。
//
// 规范形与渲染侧同一规则（A-2，2026-09-21）：**只有单值才携带**（多值 /
// 无值 ⇒ 裸 "file"），token 原样、不去重以外不做归一。族落在 ncoda 闭集外
// 的判断属于 lowering（yamlFileKindName 表外 ⇒ 放宽裸 file），此处只做承载。
//
// 发射的是**源码拼写**（`token.EscapedSpelling`）：关键字族名（`code`）写成
// `$code`，否则头文件里的 `file<$code>` 会解析不出来（裸 `file<code>` 是语法错误）。
//
// 只带 `types` 轴：coze-biz 对 extensions / upload_methods **无承载位**
// （docs/coze-biz-schema-audit.md §6.9 F-AXES-6 已登记丢弃），带进来会让
// 注册表串宣称一个下游兑现不了的约束。
func fileRegistryTypeString(c ast.FileTypeConstraints) string {
	seen := map[string]bool{}
	var toks []string
	for _, t := range c.Types {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		toks = append(toks, t)
	}
	sort.Strings(toks)
	if len(toks) != 1 {
		return "file"
	}
	return "file<" + token.EscapedSpelling(toks[0]) + ">"
}

// flattenOutputs expands a named struct type graph into dotted output paths.
// Named fields recurse (cycle-checked); primitive/array/map/file fields are
// leaves (arrays stop flattening, mirroring the YAML registry).
func flattenOutputs(ref ast.TypeRef, prefix string, types map[string][]*ast.FieldDecl, visiting map[string]bool) ([]OutputField, error) {
	named, ok := ref.(*ast.NamedTypeRef)
	if !ok {
		return nil, fmt.Errorf("return type must be a named struct type (got %T)", ref)
	}
	return flattenNamedFields(named.Name(), prefix, types, visiting)
}

// flattenNamedFields expands one named struct type into dotted output paths.
// Named record fields recurse (cycle-checked); primitive/array/map/file
// fields are leaves. array-of-record fields (array<T> where T is a named
// record) emit the array leaf ("array<record:T>") plus the element record's
// fields as descendant paths (prefix.field.<elem-field>), so consumers can
// address element members precisely (E5-b).
func flattenNamedFields(name, prefix string, types map[string][]*ast.FieldDecl, visiting map[string]bool) ([]OutputField, error) {
	if visiting[name] {
		return nil, fmt.Errorf("recursive type %q", name)
	}
	fields, exists := types[name]
	if !exists {
		return nil, fmt.Errorf("undefined type %q", name)
	}
	visiting[name] = true
	defer delete(visiting, name)

	var outputs []OutputField
	for _, field := range fields {
		path := field.Name()
		if prefix != "" {
			path = prefix + "." + field.Name()
		}
		ref := field.TypeRef()
		if arr, ok := ref.(*ast.ArrayTypeRef); ok {
			if namedElem, isNamed := arr.Element().(*ast.NamedTypeRef); isNamed {
				outputs = append(outputs, OutputField{Name: path, Type: arrayRecordType(namedElem.Name())})
				elems, err := flattenNamedFields(namedElem.Name(), path, types, visiting)
				if err != nil {
					return nil, err
				}
				outputs = append(outputs, elems...)
				continue
			}
		}
		if namedField, isNamed := ref.(*ast.NamedTypeRef); isNamed {
			nested, err := flattenNamedFields(namedField.Name(), path, types, visiting)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, nested...)
			continue
		}
		leafType, err := typeRefToTypeString(ref)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", path, err)
		}
		outputs = append(outputs, OutputField{Name: path, Type: leafType})
	}
	return outputs, nil
}

// arrayRecordType is the registry leaf type for an array whose element is a
// named record ("array<record:<name>>"). Element record fields travel as
// descendant dotted paths (see flattenNamedFields).
func arrayRecordType(name string) string { return "array<record:" + name + ">" }

// capabilityPolicies carries the @capability annotation of one api.
type capabilityPolicies struct {
	retry         bool
	failureBranch bool
	timeoutMS     int
	idempotent    bool
}

var capabilityAnnotationRe = regexp.MustCompile(`(?m)^[ \t]*@capability[ \t]+([^\n]+)\n`)
var functionDeclRe = regexp.MustCompile(`(?m)^[ \t]*function[ \t]+([A-Za-z_][A-Za-z0-9_]*)`)

// stripCapabilityAnnotations removes @capability annotation lines from a
// header source (replacing them with blank lines to preserve line numbers)
// and returns the per-function policies they declared. The annotation is
// header-only syntax, handled here so the ncoda language (lexer/parser/AST)
// remains untouched and reference-compiler parity is unaffected.
func stripCapabilityAnnotations(source, filename string) (string, map[string]capabilityPolicies, error) {
	policies := make(map[string]capabilityPolicies)
	cleaned := source
	// Find the next `function <name>` after each annotation line so the
	// annotation binds to the declaration that follows it.
	annotationSpans := capabilityAnnotationRe.FindAllStringSubmatchIndex(source, -1)
	for i := len(annotationSpans) - 1; i >= 0; i-- {
		span := annotationSpans[i]
		attrText := source[span[2]:span[3]]
		p, err := parseCapabilityAttributes(attrText)
		if err != nil {
			return "", nil, fmt.Errorf("capability annotation: %w", err)
		}
		after := source[span[1]:]
		loc := functionDeclRe.FindStringSubmatchIndex(after)
		if loc == nil {
			return "", nil, fmt.Errorf("capability annotation is not followed by a function declaration")
		}
		name := after[loc[2]:loc[3]]
		if _, dup := policies[name]; dup {
			return "", nil, fmt.Errorf("duplicate capability annotation for function %q", name)
		}
		policies[name] = p
		// Replace the annotation line with a blank line of equal length.
		lineEnd := span[1]
		replacement := strings.Repeat(" ", lineEnd-span[0])
		cleaned = cleaned[:span[0]] + replacement + cleaned[lineEnd:]
	}
	return cleaned, policies, nil
}

func parseCapabilityAttributes(text string) (capabilityPolicies, error) {
	var p capabilityPolicies
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return p, fmt.Errorf("expected key=true, got %q", part)
		}
		key = strings.TrimSpace(key)
		if key == "timeout_ms" {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return p, fmt.Errorf("attribute %q: expected a non-negative integer, got %q", key, strings.TrimSpace(value))
			}
			p.timeoutMS = n
			continue
		}
		flag, err := parseBoolLiteral(strings.TrimSpace(value))
		if err != nil {
			return p, fmt.Errorf("attribute %q: %w", key, err)
		}
		switch key {
		case "retry":
			p.retry = flag
		case "failure_branch":
			p.failureBranch = flag
		case "idempotent":
			p.idempotent = flag
		default:
			return p, fmt.Errorf("unknown attribute %q (allowed: retry, failure_branch, timeout_ms, idempotent)", key)
		}
	}
	return p, nil
}

func parseBoolLiteral(text string) (bool, error) {
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false, got %q", text)
	}
}

// LoadCapabilityPackages resolves import declarations against search paths
// and loads only the imported packages (P3.5 symbol import). For each
// import, the provider slug is resolved to <searchPath>/<provider>.ncoda in
// path order; the first hit wins. A missing provider reports every searched
// path so the user can fix the --tool-path. Definitions from all resolved
// packages are merged through NewRegistry (provider global-uniqueness and
// duplicate checks preserved). Header files themselves may not import
// (capabilityDefinitionsFromProgram rejects them), so there is no recursive
// resolution.
func LoadCapabilityPackages(searchPaths []string, imports []string) (*Registry, error) {
	if len(imports) == 0 {
		return NewRegistry()
	}
	// Import providers may carry an explicit platform qualifier
	// ("coze-biz.web_search"); the qualifier selects the platform binding but
	// never the capability ABI — headers are platform-neutral and shared
	// (docs/PLATFORM-MECHANISM.md §5.1). Strip the qualifier before
	// resolving the header and registering definitions under the bare name.
	seen := make(map[string]bool, len(imports))
	var definitions []Definition
	var unresolved []string
	for _, provider := range imports {
		if seen[provider] {
			continue
		}
		seen[provider] = true
		_, bare, err := SplitImport(provider)
		if err != nil {
			return nil, err
		}
		resolved, defs, err := loadCapabilityPackage(searchPaths, bare)
		if err != nil {
			return nil, err
		}
		if !resolved {
			unresolved = append(unresolved, provider)
			continue
		}
		definitions = append(definitions, defs...)
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf("unresolved import(s) %q (searched: %v)",
			strings.Join(unresolved, `", "`), searchPaths)
	}
	registry, err := NewRegistry(definitions...)
	if err != nil {
		return nil, fmt.Errorf("tool capability packages: %w", err)
	}
	registry.strict = true
	return registry, nil
}

// loadCapabilityPackage resolves one provider across the search paths and
// loads its header file. It returns (false, nil, nil) when no path contains
// the package so the caller can aggregate unresolved imports.
// CapabilityHeadersDir 是规范工具根（--tool-path 指向的目录）内注册表子目录
// 的名字：能力头文件（公共 ABI）统一放这里，外部工具（如 ncoda-foundry
// 的 export）直接产出该布局，编译器按规范根导入。
//
//	<root>/capabilities-ncoda/<provider>.ncoda
//	<root>/<provider>.ncoda                    # 兼容：旧平铺布局
//
// 见 docs/TOOL-REGISTRY-LAYOUT.md。
const CapabilityHeadersDir = "capabilities-ncoda"

func loadCapabilityPackage(searchPaths []string, provider string) (bool, []Definition, error) {
	for _, dir := range searchPaths {
		// 规范根：注册表子目录优先；旧平铺布局（<dir>/<provider>.ncoda）
		// 向后兼容。
		path := filepath.Join(dir, CapabilityHeadersDir, provider+CapabilityHeaderExt)
		if _, err := os.Stat(path); err != nil {
			path = filepath.Join(dir, provider+CapabilityHeaderExt)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cleaned, policies, err := stripCapabilityAnnotations(string(data), provider+CapabilityHeaderExt)
		if err != nil {
			return false, nil, fmt.Errorf("tool capability package %q: %w", provider, err)
		}
		program, parseErrors := parser.ParseSource(cleaned, provider+CapabilityHeaderExt)
		if len(parseErrors) > 0 {
			return false, nil, fmt.Errorf("tool capability package %q: parse error: %s",
				provider, parseErrors[0].Message)
		}
		defs, err := capabilityDefinitionsFromProgram(provider, program, policies)
		if err != nil {
			return false, nil, fmt.Errorf("tool capability package %q: %w", provider, err)
		}
		if len(defs) == 0 {
			return false, nil, fmt.Errorf("tool capability package %q: no api declarations found", provider)
		}
		return true, defs, nil
	}
	return false, nil, nil
}
