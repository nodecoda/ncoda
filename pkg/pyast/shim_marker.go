package pyast

// ShimMarker 是编译器自建单表达式 shim 的头部标记（可逆机制，2026-09-02）：
// 反编译端识别该标记后把 Python 表达式还原为 ncoda 表达式（固定点），
// 未标记的代码一律透传原文（用户手写 foreign code 不误伤）。多语句 shim
// （json.parse 等）不承诺可逆，不加标记。常量单源在本包（编译/反编译共用）。
const ShimMarker = "# ncoda:shim 1\n"
