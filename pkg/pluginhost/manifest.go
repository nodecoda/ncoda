// Package pluginhost — 进程外库实现（ncoda plugin protocol v1）。
//
// 定位：`import "<lib>"; lib.fn(...)` 的实现**不必编译进 runtime**。
// 一个插件是一个独立进程，用任意语言实现，通过 stdio JSON-RPC 暴露
// 已声明的库函数；runtime 在启动时读取清单、握手、把符号注册进同一个
// plugin.Manager，并建立 `<lib>/<fn>` 别名。tool 节点、IR、前端都不变。
//
// 边界：
//   - 清单只能声明 `libraries/<lib>.ncoda` 里已有的 library/function；
//   - partial/stub 必须写 loss（与 bindings.yaml 同一条纪律）；
//   - 插件进程以当前用户权限运行，**不是沙箱**；网络/凭证策略由清单声明，
//     开源默认不提供网络能力。
package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// SchemaV1 是清单文档版本。
	SchemaV1 = "ncoda.plugin/v1"
	// ProtocolV1 是 stdio 协议版本（握手必须精确匹配）。
	ProtocolV1 = "ncoda-plugin/1"
	// ManifestName 是目录扫描时认的文件名。
	ManifestName = "ncoda-plugin.yaml"

	DefaultTimeoutMS   = 30_000
	DefaultMaxRestarts = 3
)

// Manifest 是一个进程外插件的声明。
type Manifest struct {
	Schema      string            `yaml:"schema" json:"schema"`
	ID          string            `yaml:"id" json:"id"`
	Version     string            `yaml:"version" json:"version"`
	Protocol    string            `yaml:"protocol" json:"protocol"`
	Type        string            `yaml:"type" json:"type"` // 只支持 stdio（默认）
	Command     []string          `yaml:"command" json:"command"`
	Cwd         string            `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	TimeoutMS   int               `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	MaxRestarts int               `yaml:"max_restarts,omitempty" json:"max_restarts,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Libraries   []LibraryDecl     `yaml:"libraries" json:"libraries"`

	// Dir 是清单所在目录（解析 command/cwd 用；不参与序列化）。
	Dir string `yaml:"-" json:"-"`
	// Path 是清单文件路径（诊断用）。
	Path string `yaml:"-" json:"-"`
}

// LibraryDecl 声明一个库的符号集合与保真度。
//
// Functions 支持两种写法混用：
//
//	functions: [get_url, timelines]                 # 继承库级 fidelity/loss
//	functions:
//	  - get_url
//	  - {name: wenan_split, fidelity: partial, loss: "…"}   # 函数级覆盖
//
// 函数级覆盖只收紧不放松：未写 fidelity 就继承库级；partial/stub 仍必须有 loss。
type LibraryDecl struct {
	Library   string   `yaml:"library" json:"library"`
	Version   string   `yaml:"version,omitempty" json:"version,omitempty"`
	Fidelity  string   `yaml:"fidelity" json:"fidelity"` // real|partial|stub
	Loss      string   `yaml:"loss,omitempty" json:"loss,omitempty"`
	Functions []string `yaml:"functions" json:"functions"`

	// Policies 是函数级保真度覆盖（functions 里以映射形式声明的项）。
	Policies map[string]FunctionPolicy `yaml:"-" json:"-"`
}

// FunctionPolicy 是单个函数的保真度覆盖。
type FunctionPolicy struct {
	Fidelity string `yaml:"fidelity,omitempty" json:"fidelity,omitempty"`
	Loss     string `yaml:"loss,omitempty" json:"loss,omitempty"`
}

// UnmarshalYAML 接受 functions 的两种元素形态（名字 / 带保真度的映射）。
func (l *LibraryDecl) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Library   string      `yaml:"library"`
		Version   string      `yaml:"version"`
		Fidelity  string      `yaml:"fidelity"`
		Loss      string      `yaml:"loss"`
		Functions []yaml.Node `yaml:"functions"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	l.Library, l.Version, l.Fidelity, l.Loss = raw.Library, raw.Version, raw.Fidelity, raw.Loss
	l.Functions = nil
	l.Policies = map[string]FunctionPolicy{}
	for i, item := range raw.Functions {
		switch item.Kind {
		case yaml.ScalarNode:
			var name string
			if err := item.Decode(&name); err != nil {
				return fmt.Errorf("functions[%d]: %w", i, err)
			}
			l.Functions = append(l.Functions, name)
		case yaml.MappingNode:
			var p struct {
				Name     string `yaml:"name"`
				Fidelity string `yaml:"fidelity"`
				Loss     string `yaml:"loss"`
			}
			if err := item.Decode(&p); err != nil {
				return fmt.Errorf("functions[%d]: %w", i, err)
			}
			if strings.TrimSpace(p.Name) == "" {
				return fmt.Errorf("functions[%d]: name is required in a mapping entry", i)
			}
			l.Functions = append(l.Functions, p.Name)
			if p.Fidelity != "" || p.Loss != "" {
				l.Policies[p.Name] = FunctionPolicy{Fidelity: p.Fidelity, Loss: p.Loss}
			}
		default:
			return fmt.Errorf("functions[%d]: want a function name or {name, fidelity, loss}", i)
		}
	}
	return nil
}

// FidelityFor 返回某个函数的**有效**保真度与 loss（函数级覆盖 > 库级）。
func (l *LibraryDecl) FidelityFor(fn string) (fidelity, loss string) {
	fidelity, loss = l.Fidelity, l.Loss
	if fidelity == "" {
		fidelity = "real"
	}
	if p, ok := l.Policies[fn]; ok {
		if p.Fidelity != "" {
			fidelity = p.Fidelity
		}
		if strings.TrimSpace(p.Loss) != "" {
			loss = p.Loss
		}
	}
	return fidelity, loss
}

// HeaderIndex 是 capability header 的索引：library → 声明的 function。
type HeaderIndex map[string]map[string]bool

var (
	reLibrary  = regexp.MustCompile(`(?m)^library\s+([A-Za-z_][A-Za-z0-9_]*)\s*@\s*([0-9A-Za-z._-]+)\s*;`)
	reFunction = regexp.MustCompile(`(?m)^function\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	reID       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// LoadHeaders 索引若干目录下的 header（后出现者不覆盖先出现者）。
func LoadHeaders(dirs []string) (HeaderIndex, error) {
	out := HeaderIndex{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("pluginhost: headers %q: %w", dir, err)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".ncoda") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			m := reLibrary.FindSubmatch(b)
			if m == nil {
				continue
			}
			lib := string(m[1])
			if _, seen := out[lib]; seen {
				continue
			}
			fns := map[string]bool{}
			for _, f := range reFunction.FindAllSubmatch(b, -1) {
				fns[string(f[1])] = true
			}
			out[lib] = fns
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pluginhost: no capability headers under %v", dirs)
	}
	return out, nil
}

// Load 读取并静态校验清单（不启动进程）。
func Load(path string, headers HeaderIndex) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: read manifest %q: %w", path, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("pluginhost: parse %q: %w", path, err)
	}
	m.Path = path
	m.Dir = filepath.Dir(path)
	if err := m.normalizeAndValidate(headers); err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", path, err)
	}
	return &m, nil
}

// Discover 扫描目录：<dir>/ncoda-plugin.yaml 与 <dir>/*/ncoda-plugin.yaml。
func Discover(dir string) ([]string, error) {
	var out []string
	top := filepath.Join(dir, ManifestName)
	if _, err := os.Stat(top); err == nil {
		out = append(out, top)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), ManifestName)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Manifest) normalizeAndValidate(headers HeaderIndex) error {
	if m.Schema != SchemaV1 {
		return fmt.Errorf("schema = %q, want %q", m.Schema, SchemaV1)
	}
	if !reID.MatchString(m.ID) {
		return fmt.Errorf("id %q: use [A-Za-z0-9_.-]", m.ID)
	}
	if m.Protocol == "" {
		m.Protocol = ProtocolV1
	}
	if m.Protocol != ProtocolV1 {
		return fmt.Errorf("protocol = %q, want %q", m.Protocol, ProtocolV1)
	}
	if m.Type == "" {
		m.Type = "stdio"
	}
	if m.Type != "stdio" {
		return fmt.Errorf("type = %q, only stdio is supported in v1", m.Type)
	}
	if len(m.Command) == 0 || strings.TrimSpace(m.Command[0]) == "" {
		return fmt.Errorf("command is required (e.g. [\"python3\", \"server.py\"])")
	}
	if m.TimeoutMS <= 0 {
		m.TimeoutMS = DefaultTimeoutMS
	}
	if m.MaxRestarts < 0 {
		return fmt.Errorf("max_restarts must be >= 0")
	}
	if m.MaxRestarts == 0 {
		m.MaxRestarts = DefaultMaxRestarts
	}
	if len(m.Libraries) == 0 {
		return fmt.Errorf("no libraries declared")
	}
	seen := map[string]string{}
	for i := range m.Libraries {
		lib := &m.Libraries[i]
		if lib.Library == "" {
			return fmt.Errorf("libraries[%d]: library is required", i)
		}
		fns, ok := headers[lib.Library]
		if !ok {
			return fmt.Errorf("library %q: no capability header declares it", lib.Library)
		}
		if base := lib.Fidelity; base != "" && base != "real" && base != "partial" && base != "stub" {
			return fmt.Errorf("library %q: fidelity = %q, want real|partial|stub", lib.Library, base)
		}
		if len(lib.Functions) == 0 {
			return fmt.Errorf("library %q: no functions declared", lib.Library)
		}
		inLib := map[string]bool{}
		for _, fn := range lib.Functions {
			if !fns[fn] {
				return fmt.Errorf("library %q: function %q is not declared in the header", lib.Library, fn)
			}
			if inLib[fn] {
				return fmt.Errorf("library %q: function %q declared twice", lib.Library, fn)
			}
			inLib[fn] = true
			fid, loss := lib.FidelityFor(fn)
			switch fid {
			case "real":
			case "partial", "stub":
				if strings.TrimSpace(loss) == "" {
					return fmt.Errorf("library %q: function %q fidelity %q requires a non-empty loss "+
						"(set it on the function entry or on the library)", lib.Library, fn, fid)
				}
			default:
				return fmt.Errorf("library %q: function %q fidelity = %q, want real|partial|stub", lib.Library, fn, fid)
			}
			key := lib.Library + "." + fn
			if prev, dup := seen[key]; dup {
				return fmt.Errorf("symbol %q declared twice (also by %q)", key, prev)
			}
			seen[key] = lib.Library
		}
	}
	return nil
}

// Symbols 返回清单声明的全部 `<library>.<function>`。
func (m *Manifest) Symbols() []string {
	var out []string
	for _, lib := range m.Libraries {
		for _, fn := range lib.Functions {
			out = append(out, lib.Library+"."+fn)
		}
	}
	return out
}

// ResolveCommand 返回可执行的绝对路径与参数（相对路径按清单目录解析）。
func (m *Manifest) ResolveCommand() (string, []string, error) {
	prog := m.Command[0]
	if !strings.ContainsRune(prog, os.PathSeparator) && !strings.Contains(prog, "/") {
		return prog, append([]string(nil), m.Command[1:]...), nil
	}
	if !filepath.IsAbs(prog) {
		prog = filepath.Join(m.Dir, prog)
	}
	prog = filepath.Clean(prog)
	if _, err := os.Stat(prog); err != nil {
		return "", nil, fmt.Errorf("command %q: %w", prog, err)
	}
	return prog, append([]string(nil), m.Command[1:]...), nil
}

// WorkDir 返回插件进程的工作目录。
func (m *Manifest) WorkDir() string {
	if m.Cwd == "" {
		return m.Dir
	}
	if filepath.IsAbs(m.Cwd) {
		return m.Cwd
	}
	return filepath.Join(m.Dir, m.Cwd)
}
