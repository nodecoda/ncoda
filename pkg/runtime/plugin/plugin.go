// Package plugin — ncoda plugin 运行机制。
//
// 三阶段模型：声明 → 注册 → 运行。
//
//   - 声明（manifest）：Plugin 描述一组 Tool 与共享执行配置（server url、
//     auth、超时）。http 型可从 JSON/YAML 加载；callable 型以 Go 闭包
//     注册（内建插件 / 脚本桥）。
//   - 注册：Manager 提供注册管理（Register/Unregister/Lookup），并实现
//     nodes.ToolRegistry（Names/Call），供 tool 节点与 agent 节点消费。
//   - 运行：Manager.Call 按 PluginType 分派 Invocation（http/callable），
//     注入超时，向外广播观察者事件（start/success/error 审计）。
//
// 设计对齐（参照 coze-studio domain/plugin/service/tool）：
//   - Invocation 抽象返回 (Request, Result)，request 供审计/调试；
//   - 参数按位置分组（query/header/path/body），与 OpenAPI location 对应；
//   - 认证注入集中到 invocation 层（none / api_key / bearer）。
package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// PluginType 决定 Invocation 分派。
type PluginType string

const (
	// PluginHTTP — 按 method + path + server_url 发 HTTP 请求。
	PluginHTTP PluginType = "http"
	// PluginCallable — Go 闭包实现（内建插件、脚本桥）。
	PluginCallable PluginType = "callable"
)

// AuthType 认证注入方式。
type AuthType string

const (
	// AuthNone 不注入。
	AuthNone AuthType = "none"
	// AuthAPIKey 在指定 header 注入静态值。
	AuthAPIKey AuthType = "api_key"
	// AuthBearer 注入 Authorization: Bearer <token>。
	AuthBearer AuthType = "bearer"
)

// AuthConfig 简单认证配置（无 UI/多租户场景，只支持静态注入；
// 值支持 ${ENV_VAR} 模板，运行时从环境解析）。
type AuthConfig struct {
	Type      AuthType `json:"type" yaml:"type"`
	KeyHeader string   `json:"key_header,omitempty" yaml:"key_header,omitempty"`
	KeyValue  string   `json:"key_value,omitempty" yaml:"key_value,omitempty"`
	Token     string   `json:"token,omitempty" yaml:"token,omitempty"`
}

// ParamIn 参数位置（http 型）。
type ParamIn string

const (
	InQuery  ParamIn = "query"
	InHeader ParamIn = "header"
	InPath   ParamIn = "path"
	InBody   ParamIn = "body"
)

// ParamSchema 声明一个工具参数（JSON Schema 子集）。
type ParamSchema struct {
	Name     string  `json:"name" yaml:"name"`
	Type     string  `json:"type" yaml:"type"`
	Desc     string  `json:"desc,omitempty" yaml:"desc,omitempty"`
	Required bool    `json:"required,omitempty" yaml:"required,omitempty"`
	Default  any     `json:"default,omitempty" yaml:"default,omitempty"`
	In       ParamIn `json:"in,omitempty" yaml:"in,omitempty"`
	// Enum 是参数取值白名单（对齐参考目标 ToolParameter.options / JSON Schema
	// enum）:模型可见,且调用侧可据此约束生成合法参数;空 = 不约束。
	Enum []any `json:"enum,omitempty" yaml:"enum,omitempty"`
	// Min / Max 是数值参数边界（对齐参考目标 min/max → JSON Schema
	// minimum/maximum）。nil = 不约束。
	Min *float64 `json:"min,omitempty" yaml:"min,omitempty"`
	Max *float64 `json:"max,omitempty" yaml:"max,omitempty"`
}

// CallFunc — callable 型工具实现。
type CallFunc func(ctx context.Context, args map[string]any) (any, error)

// Tool 是插件内的一个可调用工具。
type Tool struct {
	Name   string        `json:"name" yaml:"name"`
	Desc   string        `json:"desc,omitempty" yaml:"desc,omitempty"`
	Method string        `json:"method,omitempty" yaml:"method,omitempty"` // http 型
	Path   string        `json:"path,omitempty" yaml:"path,omitempty"`     // http 型，支持 {param} 模板
	Params []ParamSchema `json:"params,omitempty" yaml:"params,omitempty"`
	// Outputs 声明结果形状（可选）。Name 支持点路径（嵌套 record 字段）。
	// 缺省时视为 { "result": "any" }（开放世界默认）。
	Outputs []OutputField `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	// Capabilities 声明这个工具实现的符号（`库.函数`）。符号属于 ncoda，
	// 实现包的名字属于运行时：没有这张表，包 id 就不得不兼任库名
	// （docs/SYMBOL-SPEC.md V3）。为空时退化为 `<plugin id>/<tool name>`。
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
	Call         CallFunc `json:"-" yaml:"-"`
}

// OutputField 声明一个结果字段（对应编译期 tool.Definition.Outputs）。
type OutputField struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// InterfaceHash 返回工具声明的稳定 ABI 指纹（Tier-2 review #12,对齐
// pkg/tool 的 computeInterfaceHash——同一"长度前缀 sha256"风格）。
// 覆盖全部声明字段:Name/Desc/Method/Path/Params(含 Default/Enum/Min/Max/
// In)/Outputs;Call 是进程内实现,不参与 ABI。声明任何字段变化 → hash 变化,
// 供绑定/桥接层检测"运行期插件声明与编译期注册表漂移"。参数顺序敏感
// （平台端口按位置/顺序）,与 tool 侧一致。Desc 参与 hash（插件侧 Desc 是
// 可观测契约的一部分;与 tool 侧 ABI 的取舍不同,因为插件声明即源）。
func (t Tool) InterfaceHash() string {
	h := sha256.New()
	writeField := func(v string) {
		_, _ = h.Write([]byte(fmt.Sprintf("%d:%s", len(v), v)))
	}
	writeField(t.Name)
	writeField(t.Desc)
	writeField(t.Method)
	writeField(t.Path)
	for _, p := range t.Params {
		writeField(p.Name)
		writeField(p.Type)
		writeField(p.Desc)
		if p.Required {
			writeField("required")
		} else {
			writeField("optional")
		}
		if p.Default != nil {
			// %T:%v 类型敏感:%v 会把 int(1) 与 float64(1) 都渲染成 "1"
			// （Go json.Marshal 同样输出 "1"）,ABI 指纹必须区分 1 与 1.0。
			writeField(fmt.Sprintf("default=%T:%v", p.Default, p.Default))
		}
		for _, e := range p.Enum {
			writeField(fmt.Sprintf("enum=%T:%v", e, e))
		}
		if p.Min != nil {
			writeField("min=" + fmt.Sprintf("%v", *p.Min))
		}
		if p.Max != nil {
			writeField("max=" + fmt.Sprintf("%v", *p.Max))
		}
		writeField(string(p.In))
	}
	for _, o := range t.Outputs {
		writeField(o.Name)
		writeField(o.Type)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Plugin 是一组工具 + 共享执行配置。
type Plugin struct {
	ID        string      `json:"id" yaml:"id"`
	Version   string      `json:"version,omitempty" yaml:"version,omitempty"`
	Type      PluginType  `json:"type" yaml:"type"`
	ServerURL string      `json:"server_url,omitempty" yaml:"server_url,omitempty"`
	Auth      *AuthConfig `json:"auth,omitempty" yaml:"auth,omitempty"`
	TimeoutMS int64       `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Tools     []Tool      `json:"tools" yaml:"tools"`
}

// ToolName 返回工具的逻辑名（provider/name，与 nodes.Tool 节点一致）。
func (p *Plugin) ToolName(t Tool) string {
	if p.ID == "" {
		return t.Name
	}
	return p.ID + "/" + t.Name
}

// ResolveAuth 解析 auth 值中的 ${ENV_VAR} 模板。
func (a *AuthConfig) Resolve() (*AuthConfig, error) {
	if a == nil {
		return nil, nil
	}
	out := *a
	var err error
	out.KeyValue, err = resolveEnv(out.KeyValue)
	if err != nil {
		return nil, err
	}
	out.Token, err = resolveEnv(out.Token)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func resolveEnv(s string) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	start := strings.Index(s, "${")
	end := strings.Index(s[start:], "}")
	if end < 0 {
		return "", fmt.Errorf("plugin: malformed env template %q", s)
	}
	name := s[start+2 : start+end]
	v, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("plugin: env %q referenced by auth not set", name)
	}
	return s[:start] + v + s[start+end+1:], nil
}

// ManifestFromFile 从 JSON/YAML 文件加载 http 型插件。
func ManifestFromFile(path string) (*Plugin, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &Plugin{}
	if err := unmarshalManifest(b, filepath.Ext(path), p); err != nil {
		return nil, fmt.Errorf("plugin: parse %s: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("plugin: %s: %w", path, err)
	}
	return p, nil
}

func unmarshalManifest(b []byte, ext string, p *Plugin) error {
	switch strings.ToLower(ext) {
	case ".json":
		return json.Unmarshal(b, p)
	case ".yaml", ".yml":
		return yaml.Unmarshal(b, p)
	default:
		// 无扩展名时先试 JSON，再试 YAML。
		if json.Unmarshal(b, p) == nil {
			return nil
		}
		return yaml.Unmarshal(b, p)
	}
}

// Validate 校验插件声明完整性。
// validPluginID 限定插件 ID 到安全字符集（[A-Za-z0-9_.-]）:插件 ID 会拼入
// config 目录文件路径（cli/plugin.go pluginAdd）,裸名带 / \ 或精确 . ..
// 会造成目录穿越写文件（Tier-1 review #3）。点保留以支持命名空间式 ID
// （如 search.google_search）,点不会产生路径分量。与 cli validTemplateName /
// importns slug 同语义。
func validPluginID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// capabilityNameRE is the runtime-side grammar of a symbol: `<library>.<function>`
// (docs/SYMBOL-SPEC.md R1). A capability names what the tool implements, so the
// implementation package id never has to double as the library name.
var capabilityNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*$`)

func (p *Plugin) Validate() error {
	if p.ID == "" {
		return fmt.Errorf("plugin id is required")
	}
	if !validPluginID(p.ID) {
		return fmt.Errorf("plugin %q: invalid id (use [A-Za-z0-9_.-], no path separators)", p.ID)
	}
	switch p.Type {
	case PluginHTTP, PluginCallable:
	default:
		return fmt.Errorf("plugin %s: unsupported type %q", p.ID, p.Type)
	}
	if len(p.Tools) == 0 {
		return fmt.Errorf("plugin %s: no tools declared", p.ID)
	}
	seen := map[string]bool{}
	seenCapability := map[string]string{}
	for _, t := range p.Tools {
		if t.Name == "" {
			return fmt.Errorf("plugin %s: tool name is required", p.ID)
		}
		if seen[t.Name] {
			return fmt.Errorf("plugin %s: duplicate tool %q", p.ID, t.Name)
		}
		seen[t.Name] = true
		for _, capability := range t.Capabilities {
			if !capabilityNameRE.MatchString(capability) {
				return fmt.Errorf("plugin %s: tool %q: invalid capability %q (want <library>.<function>, e.g. capcut.create_draft)", p.ID, t.Name, capability)
			}
			if owner, dup := seenCapability[capability]; dup {
				return fmt.Errorf("plugin %s: capability %q declared by both %q and %q", p.ID, capability, owner, t.Name)
			}
			seenCapability[capability] = t.Name
		}
		if p.Type == PluginHTTP {
			if t.Method == "" {
				return fmt.Errorf("plugin %s: tool %q: http method required", p.ID, t.Name)
			}
			if t.Path == "" {
				return fmt.Errorf("plugin %s: tool %q: http path required", p.ID, t.Name)
			}
		} else if t.Call == nil {
			return fmt.Errorf("plugin %s: tool %q: callable requires Call func", p.ID, t.Name)
		}
	}
	return nil
}
