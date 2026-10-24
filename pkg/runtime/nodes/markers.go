package nodes

// markers.go — 运行时控制标记（D5）。
//
// 这些是节点与驱动器之间的协议常量:selector 输出 "$branch" 键,由
// compile 层的分支门读取;break/continue sink 输出 "$break"/"$continue",
// 由 loop 驱动器读取。放在 nodes 包是因为它们是节点实现契约的一部分
// （compile 层引用,不重复定义）。
const (
	MarkerBranch   = "$branch"
	MarkerBreak    = "$break"
	MarkerContinue = "$continue"
)
