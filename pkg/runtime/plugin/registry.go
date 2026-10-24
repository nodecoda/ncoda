package plugin

// registry.go — 注册管理（Manager）。
//
// 职责：
//   - Register：插件注册，冲突检测（插件 ID 重复 / 工具逻辑名冲突）；
//     同 ID 同版本视为幂等（重新注册 = 覆盖），不同版本拒绝。
//   - Unregister：按插件 ID 级联注销其全部工具。
//   - Lookup/Names/Plugin：查询。
//
// Manager 实现 nodes.ToolRegistry（Names/Call），因此可直接注入
// compile.WithDeps(nodes.Deps{Tools: m})，供 tool 节点与 agent 节点消费。

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ToolEntry 是注册后的工具条目：插件上下文 + 工具声明。
type ToolEntry struct {
	Plugin *Plugin
	Tool   *Tool
}

// Manager 是插件注册表 + 工具分派入口。
type Manager struct {
	mu        sync.RWMutex
	plugins   map[string]*Plugin    // by plugin ID
	tools     map[string]*ToolEntry // by logical name ("id/name" 或退化 "name")
	aliases   map[string]string     // 平台名 → 已注册逻辑名（绑定层；见 Alias）
	observers []Observer
}

// New 创建空 Manager。
func New() *Manager {
	return &Manager{
		plugins: map[string]*Plugin{},
		tools:   map[string]*ToolEntry{},
		aliases: map[string]string{},
	}
}

// Register 注册一个插件。失败时保持注册表原状（先校验后提交）。
func (m *Manager) Register(p *Plugin) error {
	if err := p.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.plugins[p.ID]; ok {
		if old.Version != p.Version {
			return fmt.Errorf("plugin %q already registered with version %q (want %q); unregister first",
				p.ID, old.Version, p.Version)
		}
	}

	// 预检工具名冲突（不含自己）。
	staged := make(map[string]*ToolEntry, len(p.Tools))
	for i := range p.Tools {
		t := &p.Tools[i]
		entry := &ToolEntry{Plugin: p, Tool: t}
		// 每个工具至少以 <id>/<tool> 注册（存量写法），声明了 capabilities
		// 时同时以符号注册：符号属于 ncoda，包 id 不再兼任库名。
		keys := append([]string{logicalName(p.ID, t.Name)}, t.Capabilities...)
		for _, name := range keys {
			if _, clash := m.tools[name]; clash {
				// 同 ID 重注册时允许覆盖自己。
				cur := m.tools[name]
				if cur == nil || cur.Plugin.ID != p.ID {
					return fmt.Errorf("plugin %q: tool %q conflicts with existing tool %q (plugin %q)",
						p.ID, t.Name, name, existingPluginID(m.plugins, m.tools, name))
				}
			}
			if prev, dup := staged[name]; dup && prev.Tool != t {
				return fmt.Errorf("plugin %q: name %q declared by both tools %q and %q",
					p.ID, name, prev.Tool.Name, t.Name)
			}
			staged[name] = entry
		}
	}

	// 提交：先注销旧版本同 ID 的条目，再写新条目。
	if old, ok := m.plugins[p.ID]; ok {
		for i := range old.Tools {
			t := &old.Tools[i]
			delete(m.tools, logicalName(old.ID, t.Name))
			for _, capability := range t.Capabilities {
				delete(m.tools, capability)
			}
		}
	}
	m.plugins[p.ID] = p
	for name, entry := range staged {
		m.tools[name] = entry
	}
	return nil
}

func existingPluginID(plugins map[string]*Plugin, tools map[string]*ToolEntry, name string) string {
	if e, ok := tools[name]; ok {
		return e.Plugin.ID
	}
	return "?"
}

// RegisterAll 批量注册；任一失败则整体回滚。
func (m *Manager) RegisterAll(ps ...*Plugin) error {
	// 先全部校验，再逐个提交（Register 内部无状态残留于失败路径）。
	for _, p := range ps {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	for _, p := range ps {
		if err := m.Register(p); err != nil {
			return err
		}
	}
	return nil
}

// Unregister 注销插件及其全部工具。未注册的 ID 返回错误。
func (m *Manager) Unregister(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plugins[id]
	if !ok {
		return fmt.Errorf("plugin %q not registered", id)
	}
	for i := range p.Tools {
		t := &p.Tools[i]
		delete(m.tools, logicalName(id, t.Name))
		for _, capability := range t.Capabilities {
			delete(m.tools, capability)
		}
	}
	owned := make(map[string]bool, len(p.Tools))
	for i := range p.Tools {
		owned[logicalName(id, p.Tools[i].Name)] = true
	}
	for alias, target := range m.aliases {
		if owned[target] {
			delete(m.aliases, alias)
		}
	}
	delete(m.plugins, id)
	return nil
}

// Alias 把平台侧逻辑名（如 plugin_7547770968681447458/image_generator）绑定到
// 已注册的实现（逻辑名或 capability 符号）。绑定是平台侧概念：实现包用
// 平台无关的 id 注册，运行时不必知道平台 provider 名（docs/SYMBOL-SPEC.md）。
//
// 约束：alias 不得遮蔽真实注册的工具；target 必须已注册（允许后缀名，歧义则拒绝）。
// 重复声明同一 alias 且 target 相同 → 幂等。
func (m *Manager) Alias(name, target string) error {
	name, target = strings.TrimSpace(name), strings.TrimSpace(target)
	if name == "" || target == "" {
		return fmt.Errorf("alias: name and target are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, real := m.tools[name]; real {
		return fmt.Errorf("alias %q: a real tool with that name is registered", name)
	}
	entry, ok := m.lookupLocked(target)
	if !ok {
		return fmt.Errorf("alias %q: target %q is not registered", name, target)
	}
	canonical := logicalName(entry.Plugin.ID, entry.Tool.Name)
	if name == canonical {
		return fmt.Errorf("alias %q: target is itself", name)
	}
	if prev, exists := m.aliases[name]; exists && prev != canonical {
		return fmt.Errorf("alias %q already points to %q (want %q); unregister first", name, prev, canonical)
	}
	m.aliases[name] = canonical
	return nil
}

// Aliases 返回全部绑定（alias → 实现逻辑名；排序快照，供诊断）。
func (m *Manager) Aliases() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.aliases))
	for k, v := range m.aliases {
		out[k] = v
	}
	return out
}

// Plugin 返回已注册的插件（只读指针）。
func (m *Manager) Plugin(id string) (*Plugin, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.plugins[id]
	return p, ok
}

// Lookup 按逻辑名（"id/name" 或退化 "name"）查找工具。
// 退化名仅在候选唯一时成功；多个插件声明同名工具 → 歧义，返回 false。
func (m *Manager) Lookup(name string) (*ToolEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lookupLocked(name)
}

// lookupLocked 是 Lookup 的无锁核（调用方持锁）：
// 真实逻辑名 → 绑定别名 → 唯一工具名（退化后缀）。
func (m *Manager) lookupLocked(name string) (*ToolEntry, bool) {
	if e, ok := m.tools[name]; ok {
		return e, true
	}
	if target, ok := m.aliases[name]; ok {
		if e, ok := m.tools[target]; ok {
			return e, true
		}
	}
	var hit *ToolEntry
	for _, e := range m.tools {
		if e.Tool.Name == name {
			if hit != nil {
				return nil, false // ambiguous
			}
			hit = e
		}
	}
	return hit, hit != nil
}

// Names 返回全部可调用逻辑名（已注册工具 + 绑定别名；排序，确定性）。
// 别名必须出现在这里：tool 节点按 Names 做精确匹配，绑定层不应要求
// 节点知道“这个名字是别名还是真实注册”。
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.tools)+len(m.aliases))
	for name := range m.tools {
		out = append(out, name)
	}
	for name := range m.aliases {
		if _, real := m.tools[name]; !real {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// PluginIDs 返回已注册插件 ID（排序，确定性）。
func (m *Manager) PluginIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.plugins))
	for id := range m.plugins {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Has 报告逻辑名是否已注册。
func (m *Manager) Has(name string) bool {
	_, ok := m.Lookup(name)
	return ok
}

func logicalName(id, tool string) string {
	if id == "" {
		return tool
	}
	return id + "/" + tool
}

// MatchName 判断逻辑名是否匹配（完整名或按后缀匹配 tool 名）。
// 与 nodes/tool.go 的 registered 判定一致。
func MatchName(entry *ToolEntry, name string) bool {
	ln := logicalName(entry.Plugin.ID, entry.Tool.Name)
	return ln == name || strings.HasSuffix(ln, "/"+name)
}
