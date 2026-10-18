package capability

// axis_liveness_test.go — 能力表的**每一条轴都必须有生产读者**（死轴 ⇒ 红）。
//
// 为什么这条判据存在（2026-09-27，用户口径）：
//
//	「json 是 ncoda 内置的功能，而 coze-biz **恰巧**有这个节点」——
//	平台有没有承载面是**偶然事实**，必须由能力表回答，不能由构造期写死。
//
// 一手背景（同一病灶的两次现场）：
//
//	① `JSONSerial/JSONDeserial` 曾不在决策点上被读 ⇒ 产物是原生 `to_json`/`from_json`，
//	   日志却恒写 `SHIM / no-native-node`（R-118，已修：单一决策点 `recordJSONBuiltinPlacement`）。
//	② 普查（2026-09-27）实测 `Condition.RefLiteral` 与 `ObjectLit` **0 个生产读者**
//	   ⇒ 两条落点其实是写死的，能力表对它们而言是装饰。
//
// 机制（**不维护第二份清单**）：字段名由 `reflect` 扫 `Table` 得出；新增一条轴
// ⇒ 自动进入本判据 ⇒ 没有生产读者就红。这正是「一个规则不两处定义」的机器形态。
//
// 处置：`Condition.RefLiteral` = 接线（`conditionNativeDecision` 读它）；
// `ObjectLit` = 删（三个目标都没有原生对象构造节点 ⇒ 轴恒为 false = 无信息量，
// 且本仓 2026-09-23 已认「不声明是最优策略」；真需要时按接线重新加）。

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// axisLeafNames 反射取 Table 的全部叶子字段名（嵌套结构体递归）。
func axisLeafNames(t *testing.T) []string {
	t.Helper()
	var out []string
	var walk func(rt reflect.Type, prefix string)
	walk = func(rt reflect.Type, prefix string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, prefix+f.Name+".")
				continue
			}
			out = append(out, f.Name)
		}
	}
	walk(reflect.TypeOf(Table{}), "")
	if len(out) == 0 {
		t.Fatal("反射没取到任何轴 —— 判据空了（Table 被改名 / 全字段未导出？）")
	}
	sort.Strings(out)
	return out
}

// axisReaderCensus 扫生产源码（`pkg/` + `internal/` + `platforms/` 的非 `_test.go`，
// 跳过本包目录 = 声明的家），数每条轴 `.<轴名>` 的**文件**命中数。
func axisReaderCensus(t *testing.T, axes []string) (map[string][]string, int) {
	t.Helper()
	root := filepath.Join("..", "..")
	self := filepath.Join("pkg", "capability")
	hits := map[string][]string{}
	files := 0
	for _, top := range []string{"pkg", "internal", "platforms"} {
		dir := filepath.Join(root, top)
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			continue // 该根不在本模块（O2 开放/闭源拆分：开放仓无 internal/）
		}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			if strings.HasPrefix(rel, self+string(filepath.Separator)) {
				return nil // 声明的家：定义处不算读者
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			src := string(raw)
			files++
			for _, a := range axes {
				if axisReferenced(src, a) {
					hits[a] = append(hits[a], rel)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s：%v", top, err)
		}
	}
	return hits, files
}

// axisReferenced 是判据用的**唯一**匹配式（提取出来是为了能被正控制自测 ——
// 扫描逻辑本身也得有判据，否则「扫不到 = 全死」和「匹配器坏了」分不开）。
func axisReferenced(src, axis string) bool {
	return strings.Contains(src, "."+axis)
}

func TestCapabilityAxesAreLive(t *testing.T) {
	axes := axisLeafNames(t)
	// 正控制：匹配器必须认得出「反射取到的轴名」——零手维护名单，
	// 但它锁住「匹配器 / 遍历坏掉 ⇒ 判据静默变绿」这条退化路径。
	for _, a := range axes {
		if !axisReferenced("g.caps.Condition."+a+" == true", a) {
			t.Fatalf("匹配器认不出轴 %q —— 扫描面失效，判据不可信", a)
		}
	}
	hits, files := axisReaderCensus(t, axes)
	if files < 100 {
		t.Fatalf("只扫到 %d 个生产文件（今天实测 365）—— 遍历面失效，判据不可信", files)
	}
	var dead []string
	for _, a := range axes {
		if len(hits[a]) == 0 {
			dead = append(dead, a)
		}
	}
	if len(dead) > 0 {
		t.Errorf("能力表有**死轴**（声明了没有任何落点读它 ⇒ 该落点是写死的，平台有/没有承载面都不影响产物）= %v\n"+
			"要么接线（把落点决策改成读这条轴），要么删（不声明是最优策略）——见 SHIM-ESCAPE-CONTRACT.md §5-2。", dead)
	}
}
