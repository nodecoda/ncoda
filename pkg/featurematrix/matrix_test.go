package featurematrix

// matrix_test.go —— 语言特性矩阵锚点校验器（docs/LANGUAGE-FEATURE-MATRIX.md）。
//
// 机制（2026-08-29）：
//   - 矩阵是"特性 × 平台"影响面追踪：改语言特性时防跨平台遗漏；
//   - 每个 ✓ 单元格 = "完整支持且测试锁定"，必须有对应平台测试目录里的
//     `//@feature <name>` 锚点注释；
//   - 本测试解析矩阵所有 ✓ 单元格 → 检查锚点存在 → 缺则红。
//   - 标签语义/流程门禁见矩阵文档 §1/§4（~ 与 ✗ 不需要锚点，由覆盖清单兜底）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// featureRow 矩阵一行：特性标识 + 各列标签（列序对应文档表头）。
type featureRow struct {
	name  string
	cells []string
}

// columnDirs 矩阵列号（0=特性,1=语义文档,2..=平台）→ 测试目录（相对本包
// 目录 ../..，即仓库根）。与 docs/LANGUAGE-FEATURE-MATRIX.md §3 映射一致。
// 说明：parseMatrix 的 cells 从 0 起（cells[0]=语义文档列），故这里用
// cells 下标。与 docs/LANGUAGE-FEATURE-MATRIX.md §3 列映射一致。
// O2 拆分后语言内核在 pkg/（开放），闭源遗留（internal/compiler、各平台）仍
// 在 internal/；列 1 的覆盖率与拆分前一致（其余列仍指向单一目录）。
var columnDirs = map[int][]string{
	// 列 1 = 语言内核（parser / passes / ir / binding … 递归）。平台适配器列
	// 随适配器移出开放仓（ASSET-BOUNDARY），由适配器仓的同名矩阵校验。
	1: {filepath.Join("..", "..", "pkg")},
}

func TestMatrixAnchors(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "LANGUAGE-FEATURE-MATRIX.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read matrix: %v", err)
	}
	rows := parseMatrix(string(b))
	if len(rows) == 0 {
		t.Fatalf("matrix parse: no feature rows found in %s", path)
	}
	var failures []string
	for _, row := range rows {
		for col, tag := range row.cells {
			if strings.TrimSpace(tag) != "✓" {
				continue
			}
			dirs, ok := columnDirs[col]
			if !ok || len(dirs) == 0 {
				failures = append(failures, row.name+": 列"+string(rune('0'+col))+" 无测试目录映射")
				continue
			}
			// 只扫本模块实际存在的目录（O2 开放/闭源拆分：开放仓无 internal/ 与
			// coze 平台目录；godcc 里两者都在场，扫描面不变）。
			var present []string
			for _, dir := range dirs {
				if _, statErr := os.Stat(dir); statErr == nil {
					present = append(present, dir)
				}
			}
			if len(present) == 0 {
				continue
			}
			hit := false
			var walkErr error
			for _, dir := range present {
				var h bool
				h, walkErr = dirHasAnchor(dir, row.name)
				if walkErr != nil {
					break
				}
				if h {
					hit = true
					break
				}
			}
			if walkErr != nil {
				failures = append(failures, row.name+": "+walkErr.Error())
				continue
			}
			if !hit {
				failures = append(failures, row.name+": 列"+string(rune('0'+col))+" 标 ✓ 但 "+strings.Join(present, " / ")+" 无 //@feature "+row.name+" 锚点")
			}
		}
	}
	if len(failures) > 0 {
		t.Errorf("语言特性矩阵 ✓ 缺锚点 %d 处:\n%s", len(failures), strings.Join(failures, "\n"))
	}
}

// parseMatrix 解析矩阵 markdown 表格（行格式 `| \`name\` | ... |`）。
func parseMatrix(text string) []featureRow {
	var rows []featureRow
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		name = strings.TrimPrefix(name, "`")
		name = strings.TrimSuffix(name, "`")
		if name == "" || name == "特性" {
			continue
		}
		var cells []string
		for i := 2; i < len(parts)-1; i++ { // 跳过首空段与行尾空段
			cells = append(cells, strings.TrimSpace(parts[i]))
		}
		rows = append(rows, featureRow{name: name, cells: cells})
	}
	return rows
}

// dirHasAnchor 在 dir 下递归扫描 *_test.go，检查是否存在
// `//@feature <name>` 锚点注释（独立行或带空格前缀的行，行内可有后续描述）。
func dirHasAnchor(dir, name string) (bool, error) {
	found := false
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "//@feature "+name || strings.HasPrefix(line, "//@feature "+name+" ") {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found, err
}
