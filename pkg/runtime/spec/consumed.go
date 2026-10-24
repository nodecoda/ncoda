package spec

// consumed.go — D1 "运行时已消费字段"注册表。
//
// Every JSON-visible field of every config DTO is declared here. The parity
// test below marshals each DTO and asserts the field set matches this
// registry exactly, so adding/renaming a consumable field requires an
// explicit registry update (the runtime never reads un-declared fields by
// construction — the DTO is the only decode target).

// ConsumedFields maps kind → the JSON field names the runtime may read.
var ConsumedFields = map[string][]string{
	string(KindStart):        {"params"},
	string(KindEnd):          {"inputs", "outputs", "named_return", "answer"},
	string(KindLLM):          {"model", "config", "inputs", "output_format", "response_format", "exception", "result"},
	string(KindHTTP):         {"method", "url", "inputs", "body", "headers", "params", "setting_timeout_sec", "exception"},
	string(KindTool):         {"tool_name", "provider_id", "operation", "params", "exception"},
	string(KindKnowledge):    {"dataset_ids", "dataset_names", "inputs", "query_name"},
	string(KindAgent):        {"model", "instruction", "query", "max_iteration", "strategy", "memory", "memory_window", "exception", "tools"},
	string(KindText):         {"method", "template", "concat_char", "separators", "inputs", "answer"},
	string(KindCode):         {"code", "language", "inputs", "out_order", "condition", "synthetic", "keep_whole"},
	string(KindListOp):       {"operation", "inputs", "filter_key", "filter_op", "filter_val", "order_key", "order_val", "limit"},
	string(KindSelector):     {"conditions", "logic"},
	string(KindAggregate):    {"refs", "groups"},
	string(KindAssign):       {"var_name", "value", "operation", "loop_state", "loop_id"},
	string(KindLoopAssign):   {"var_name", "value", "operation", "loop_state", "loop_id"},
	string(KindQA):           {"choices", "inputs", "template", "fields", "timeout"},
	string(KindOutput):       {"template"},
	string(KindConversation): {},
	string(KindLoop):         {"var_name", "loop_type", "loop_count", "input_arrays", "inputs", "types", "state_types", "state_values", "out_types", "output_sources", "flatten_output", "is_parallel", "parallel_nums", "error_handle_mode", "children", "inner_edges"},
	string(KindBreak):        {},
	string(KindContinue):     {},
}
