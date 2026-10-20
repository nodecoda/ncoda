package obligation

// Diagnostic code registry — the single source of truth for
// docs/GRAPH-OBLIGATION-KERNEL.md §6.2. Documents and tests must use these
// constants rather than string literals.
const (
	CodeArmPortMissing            = "GOB_ARM_PORT_MISSING"
	CodeArmPortDangling           = "GOB_ARM_PORT_DANGLING"
	CodeArmPortDuplicate          = "GOB_ARM_PORT_DUPLICATE"
	CodeMergeAliasUnresolved      = "GOB_MERGE_ALIAS_UNRESOLVED"
	CodeMergeAliasCycle           = "GOB_MERGE_ALIAS_CYCLE"
	CodeUnreachableNode           = "GOB_UNREACHABLE_NODE"
	CodeDeadPathLeak              = "GOB_DEAD_PATH_LEAK"
	CodeLoopBackMissing           = "GOB_LOOP_BACK_MISSING"
	CodeLoopEntryAmbiguous        = "GOB_LOOP_ENTRY_AMBIGUOUS"
	CodeTerminatorLeak            = "GOB_TERMINATOR_LEAK"
	CodePendingIfLeak             = "GOB_PENDING_IF_LEAK"
	CodePhiOrderMismatch          = "GOB_PHI_ORDER_MISMATCH"
	CodePhiPathMismatch           = "GOB_PHI_PATH_MISMATCH"
	CodeRefUnresolved             = "GOB_REF_UNRESOLVED"
	CodeIDCollision               = "GOB_ID_COLLISION"
	CodeIDMapInvalid              = "GOB_ID_MAP_INVALID"
	CodeLoweringUndeclared        = "GOB_LOWERING_UNDECLARED"
	CodeLoweringRegistryInvalid   = "GOB_LOWERING_REGISTRY_INVALID"
	CodePlanDiverged              = "GOB_PLAN_DIVERGED"
	CodeCanonicalLandingAmbiguous = "GOB_CANONICAL_LANDING_AMBIGUOUS"
	CodeQuirkWhitelistExpired     = "GOB_QUIRK_WHITELIST_EXPIRED"
)
