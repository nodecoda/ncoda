package runner

import (
	"fmt"
	"strings"
)

// localPluginsFlag 让 `--local-plugins` 同时支持三种写法：
//
//	--local-plugins                 → 全部内建库
//	--local-plugins=database,tts    → 只注册这两个库
//	--local-plugins=false           → 都不注册（与不写等价，便于脚本覆盖）
//
// 为什么需要子集：进程外插件要与内建库共存时，必须能只关掉被接管的那几个库——
// 插件与内建声明同一符号会注册冲突（不静默覆盖），all-or-nothing 会逼用户放弃内建库。
// 未识别的库名在 localtools.Register 里报用法错，不静默变成「什么都没注册」。
type localPluginsFlag struct {
	enabled bool
	libs    []string // nil = 全部
}

func (f *localPluginsFlag) String() string {
	if !f.enabled {
		return "false"
	}
	if len(f.libs) == 0 {
		return "true"
	}
	return strings.Join(f.libs, ",")
}

func (f *localPluginsFlag) Set(v string) error {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "true", "1", "yes", "on", "all":
		f.enabled, f.libs = true, nil
		return nil
	case "false", "0", "no", "off", "none":
		f.enabled, f.libs = false, nil
		return nil
	}
	var libs []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			libs = append(libs, p)
		}
	}
	if len(libs) == 0 {
		return fmt.Errorf("--local-plugins: empty library list (use --local-plugins or --local-plugins=a,b)")
	}
	f.enabled, f.libs = true, libs
	return nil
}

// IsBoolFlag 让裸 `--local-plugins` 走 Set("true")，保留旧用法。
func (f *localPluginsFlag) IsBoolFlag() bool { return true }
