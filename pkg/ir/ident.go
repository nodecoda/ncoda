package ir

import "regexp"

// platformIdentRe matches the variable-name contract the coze-biz platform
// enforces on code/plugin node input & output names ("只能包含字母、数字或
// 下划线，并且以字母或下划线开头"): ^[A-Za-z_][A-Za-z0-9_]*$. Non-ASCII
// labels (Chinese dict keys etc.) are DATA, never platform variable names.
var platformIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// IsPlatformIdent reports whether s is a legal platform variable name.
func IsPlatformIdent(s string) bool { return platformIdentRe.MatchString(s) }
