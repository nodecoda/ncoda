package tool

import (
	"os"
	"path/filepath"
)

// PackageBindingFiles resolves import declarations against the search paths
// and returns the sibling binding file "<provider>.<target>.yaml" for each
// resolved package (P3.5 §5 绑定随包). A package without a binding file is
// skipped — package bindings are optional and the central aggregate table
// remains the primary source. Resolution order is identical to
// LoadCapabilityPackages: the first search path containing the header wins,
// and the binding file is looked up in that same directory.
func PackageBindingFiles(searchPaths []string, imports []string, target string) ([]string, error) {
	if len(searchPaths) == 0 || len(imports) == 0 {
		return nil, nil
	}
	var files []string
	seen := make(map[string]bool, len(imports))
	for _, provider := range imports {
		if seen[provider] {
			continue
		}
		seen[provider] = true
		platform, bare, err := SplitImport(provider)
		if err != nil {
			return nil, err
		}
		// Platform-qualified imports resolve against their own platform
		// binding even when no --target was given; bare imports need one.
		bindingPlatform := platform
		if bindingPlatform == "" {
			if target == "" {
				continue
			}
			bindingPlatform = target
		}
		suffix := "." + bindingPlatform + ".yaml"
		for _, dir := range searchPaths {
			// 规范根优先（与 loadCapabilityPackage 一致，M6）：header 在
			// <dir>/capabilities-ncoda/ 下时同一根内的兄弟绑定必须被解析，
			// 而不是误判"header 不存在"跳到下一个 searchPath。
			header := filepath.Join(dir, CapabilityHeadersDir, bare+CapabilityHeaderExt)
			if _, err := os.Stat(header); err != nil {
				header = filepath.Join(dir, bare+CapabilityHeaderExt)
				if _, err := os.Stat(header); err != nil {
					continue
				}
			}
			binding := filepath.Join(dir, bare+suffix)
			if _, err := os.Stat(binding); err == nil {
				files = append(files, binding)
			}
			break // first search path containing the header wins
		}
	}
	return files, nil
}
