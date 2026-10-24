// database.go — `database` 库的**真实现**（SQLite 后端），不是 stub。
//
// 为什么用 SQLite 而不是自己造一个 map：WHERE / ORDER BY / LIMIT / 类型亲和
// 这些语义若由本包发明，就只能被本包的测试"证明"，那是自证。用成熟引擎 = 语义
// 有外部出处，我们只负责**声明 ↔ 存储的映射**这一层。
//
// 映射（全部来自头文件 database.ncoda 的参数面，不猜平台私有键名）：
//
//	tables     → 表：`t_<tableID>`（平台一个数据库节点只操作一张表）
//	fieldID    → 列：`f_<fieldID>`
//	values     → INSERT/UPDATE 的 (fieldID, 值) 对
//	condition  → WHERE：conditionList 的三元组 [left, operation, right]
//	order_by   → ORDER BY：{fieldID, isAsc}
//	rows       → LIMIT
//	outputs    → 只用于**声明的结果形状**；列名映射不在语料里，故结果行以
//	             fieldID 为键（见下方"诚实边界"）
//
// 诚实边界：
//   - 平台返回的列名是**表 schema 里的字段名**（changjing / fengge…），语料里
//     只有 select 的 `outputs.outputList.items.properties` 名字集合与 columns 的
//     fieldID 列表，**没有任何一条把两者对上的映射**（一手：get_video 的
//     columns[0]=1806321126402 对应 insert 里的 img，而 properties[1]=changjing）。
//     所以本地实现以 fieldID 作列名：宁可调用方读到 null，也不造一个可能错的映射。
//   - 平台会补 id / create_time 等系统列；本实现只存**调用方写进来的**字段。
package localtools

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// ── 值解包：平台把参数包成 {type,value:{content}} / {value:X} / 裸值 ──────

// unwrapValue 把平台的参数壳剥掉，返回调用方真正写的值。
func unwrapValue(v any) any {
	for i := 0; i < 8; i++ { // 防御性上限：壳最多几层
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		switch {
		case hasKey(m, "input") && (len(m) == 1 || hasKey(m, "name")):
			// {input:…} 或 {name:…, input:…}（条件三元组/字段对的两种壳）
			v = m["input"]
		case hasKey(m, "value") && subset(keys, "type", "value", "rawMeta"):
			v = m["value"]
		case hasKey(m, "content") && subset(keys, "content", "type", "rawMeta"):
			return m["content"]
		default:
			return v
		}
	}
	return v
}

func hasKey(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func subset(keys []string, allowed ...string) bool {
	for _, k := range keys {
		ok := false
		for _, a := range allowed {
			if k == a {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// fieldIDOf 取 {name:"fieldID", input:…} 里的字段 id。
func fieldIDOf(pair map[string]any) (string, bool) {
	name, _ := unwrapValue(pair["name"]).(string)
	if name != "fieldID" {
		return "", false
	}
	id := fmt.Sprint(unwrapValue(pair["input"]))
	if id == "" || id == "<nil>" {
		return "", false
	}
	return id, true
}

// fieldValueOf 取 {name:"fieldValue", input:…} 里的值。
func fieldValueOf(pair map[string]any) (any, bool) {
	name, _ := unwrapValue(pair["name"]).(string)
	if name != "fieldValue" {
		return nil, false
	}
	return unwrapValue(pair["input"]), true
}

// decodeRow 把一条记录里的 {name:fieldID}/{name:fieldValue} 序列配成 map。
//
// 语料的真实形态是**顺序成对**：fieldID 那条给出列号，紧随其后的 fieldValue
// 那条给出值。先按名字各收各的会丢掉配对关系（第一版就是这么错的）。
func decodeRow(pairs []map[string]any) map[string]any {
	out := map[string]any{}
	pending := ""
	for _, p := range pairs {
		if id, ok := fieldIDOf(p); ok {
			pending = id
			continue
		}
		if v, ok := fieldValueOf(p); ok && pending != "" {
			out[colName(pending)] = v
			pending = ""
		}
	}
	return out
}

// splitRows 把 values 归一成「一行 = 若干 (fieldID,value) 对」。
//
// 语料里同一参数有两种分组形态，两者都必须吃：
//   - 每个元素是**一条对**（create_video / produce）：[{fieldID,fieldValue},{...}]
//   - 每个元素是**一条对数组**（database_curd）：[[{...},{...}]]
func splitRows(v any) [][]map[string]any {
	list, _ := v.([]any)
	var rows [][]map[string]any
	var single []map[string]any
	for _, item := range list {
		if group, ok := item.([]any); ok {
			var row []map[string]any
			for _, g := range group {
				if m, ok := g.(map[string]any); ok {
					row = append(row, m)
				}
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
			continue
		}
		if m, ok := item.(map[string]any); ok {
			single = append(single, m)
		}
	}
	if len(rows) == 0 && len(single) > 0 {
		rows = append(rows, single)
	}
	return rows
}

// ── 存储 ──────────────────────────────────────────────────────────────────

type dbStore struct {
	mu sync.Mutex
	db *sql.DB
}

func openStore(path string) (*dbStore, error) {
	if strings.TrimSpace(path) == "" {
		path = ":memory:"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("local database: open %q: %w", path, err)
	}
	// 单连接：:memory: 的库是 per-connection 的；文件库也避免锁竞争。
	db.SetMaxOpenConns(1)
	return &dbStore{db: db}, nil
}

func tableName(id string) string { return "t_" + sanitizeID(id) }

func colName(fieldID string) string { return "f_" + sanitizeID(fieldID) }

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "x"
	}
	return b.String()
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// ensureTable 建表并按需加列（平台表 schema 随工作流变化，列是动态的）。
func (s *dbStore) ensureTable(table string, cols []string) error {
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s ("_rowid_" INTEGER PRIMARY KEY AUTOINCREMENT)`, quoteIdent(table))); err != nil {
		return err
	}
	have, err := s.columns(table)
	if err != nil {
		return err
	}
	for _, c := range cols {
		if have[c] {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s`, quoteIdent(table), quoteIdent(c))); err != nil {
			return err
		}
		have[c] = true
	}
	return nil
}

func (s *dbStore) columns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *dbStore) orderedColumns(table string) ([]string, error) {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		if name == "_rowid_" {
			continue
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ── 条件 ──────────────────────────────────────────────────────────────────

type where struct {
	clause string
	args   []any
}

func buildWhere(cond any) (where, error) {
	m, _ := cond.(map[string]any)
	if m == nil {
		return where{}, nil
	}
	list, _ := unwrapValue(m["conditionList"]).([]any)
	logic := strings.ToUpper(fmt.Sprint(unwrapValue(m["logic"])))
	if logic == "" || logic == "<nil>" {
		logic = "AND"
	}
	if logic != "AND" && logic != "OR" {
		return where{}, fmt.Errorf("local database: unsupported condition logic %q", logic)
	}
	var parts []string
	var args []any
	for _, item := range list {
		triple, ok := item.([]any)
		if !ok || len(triple) < 3 {
			continue
		}
		left := fmt.Sprint(unwrapValue(triple[0]))
		op := strings.ToUpper(fmt.Sprint(unwrapValue(triple[1])))
		right := unwrapValue(triple[2])
		if left == "" || left == "<nil>" || op == "" || op == "<nil>" {
			continue // 语料里有 [null,null,null] 的空条件，等价于无条件
		}
		col := quoteIdent(colName(left))
		switch op {
		case "EQUAL":
			parts = append(parts, col+" = ?")
			args = append(args, right)
		case "NOT_EQUAL":
			parts = append(parts, col+" <> ?")
			args = append(args, right)
		case "IS_NULL":
			parts = append(parts, col+" IS NULL")
		case "IS_NOT_NULL":
			parts = append(parts, col+" IS NOT NULL")
		case "GREATER_THAN":
			parts = append(parts, col+" > ?")
			args = append(args, right)
		case "LESS_THAN":
			parts = append(parts, col+" < ?")
			args = append(args, right)
		case "GREATER_THAN_OR_EQUAL":
			parts = append(parts, col+" >= ?")
			args = append(args, right)
		case "LESS_THAN_OR_EQUAL":
			parts = append(parts, col+" <= ?")
			args = append(args, right)
		case "LIKE", "CONTAINS":
			parts = append(parts, col+" LIKE ?")
			args = append(args, fmt.Sprintf("%%%v%%", right))
		default:
			// 不静默忽略：没实现的算子必须让调用方看见（否则结果会"看起来对"）。
			return where{}, fmt.Errorf("local database: unsupported condition operation %q (left=%s)", op, left)
		}
	}
	if len(parts) == 0 {
		return where{}, nil
	}
	return where{clause: strings.Join(parts, " "+logic+" "), args: args}, nil
}

// ── 四个算子 ──────────────────────────────────────────────────────────────

func (s *dbStore) insert(args map[string]any) (any, error) {
	table, err := singleTable(args)
	if err != nil {
		return nil, err
	}
	rows := splitRows(args["values"])
	if len(rows) == 0 {
		return nil, fmt.Errorf("local database: insert needs values")
	}
	inserted := 0
	for _, row := range rows {
		vals := decodeRow(row)
		if len(vals) == 0 {
			continue
		}
		cols := make([]string, 0, len(vals))
		for c := range vals {
			cols = append(cols, c)
		}
		sort.Strings(cols) // 确定性：同样的输入产生同样的语句
		if err := s.ensureTable(table, cols); err != nil {
			return nil, err
		}
		marks := make([]string, len(cols))
		argv := make([]any, len(cols))
		for i, c := range cols {
			marks[i] = "?"
			argv[i] = vals[c]
		}
		if _, err := s.db.Exec(fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`,
			quoteIdent(table), quoteIdents(cols), strings.Join(marks, ", ")), argv...); err != nil {
			return nil, err
		}
		inserted++
	}
	return opResult(nil, inserted), nil
}

func (s *dbStore) selectRows(args map[string]any) (any, error) {
	table, err := singleTable(args)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTable(table, nil); err != nil {
		return nil, err
	}
	cols, err := s.selectColumns(table, args["columns"])
	if err != nil {
		return nil, err
	}
	wh, err := buildWhere(args["condition"])
	if err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		// 空表也要有声明过的列（平台表 schema 来自表定义，不来自已写入的行）。
		if err := s.ensureTable(table, cols); err != nil {
			return nil, err
		}
	}
	proj := "*"
	if len(cols) > 0 {
		proj = quoteIdents(cols)
	}
	q := fmt.Sprintf(`SELECT %s FROM %s`, proj, quoteIdent(table))
	if wh.clause != "" {
		q += " WHERE " + wh.clause
	}
	if ob, err := buildOrderBy(args["order_by"]); err != nil {
		return nil, err
	} else if ob != "" {
		q += " ORDER BY " + ob
	}
	if limit, ok := limitOf(args["rows"]); ok {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.Query(q, wh.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scanned, cerr := rows.Columns()
	if cerr != nil {
		return nil, cerr
	}
	if len(cols) == 0 {
		cols = scanned
	}
	out := []any{}
	for rows.Next() {
		vals := make([]any, len(scanned))
		ptrs := make([]any, len(scanned))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rec := map[string]any{}
		for i, c := range scanned {
			if c == "_rowid_" {
				continue
			}
			rec[strings.TrimPrefix(c, "f_")] = normalizeSQLValue(vals[i])
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return opResult(out, len(out)), nil
}

func (s *dbStore) update(args map[string]any) (any, error) {
	table, err := singleTable(args)
	if err != nil {
		return nil, err
	}
	rows := splitRows(args["values"])
	if len(rows) == 0 {
		return nil, fmt.Errorf("local database: update needs values")
	}
	wh, err := buildWhere(args["condition"])
	if err != nil {
		return nil, err
	}
	affected := 0
	for _, row := range rows {
		sets := decodeRow(row)
		if len(sets) == 0 {
			continue
		}
		cols := make([]string, 0, len(sets))
		for c := range sets {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		if err := s.ensureTable(table, cols); err != nil {
			return nil, err
		}
		assign := make([]string, len(cols))
		argv := make([]any, 0, len(cols)+len(wh.args))
		for i, c := range cols {
			assign[i] = quoteIdent(c) + " = ?"
			argv = append(argv, sets[c])
		}
		argv = append(argv, wh.args...)
		q := fmt.Sprintf(`UPDATE %s SET %s`, quoteIdent(table), strings.Join(assign, ", "))
		if wh.clause != "" {
			q += " WHERE " + wh.clause
		}
		res, err := s.db.Exec(q, argv...)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		affected += int(n)
	}
	return opResult(nil, affected), nil
}

func (s *dbStore) deleteRows(args map[string]any) (any, error) {
	table, err := singleTable(args)
	if err != nil {
		return nil, err
	}
	wh, err := buildWhere(args["condition"])
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s ("_rowid_" INTEGER PRIMARY KEY AUTOINCREMENT)`, quoteIdent(table))); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`DELETE FROM %s`, quoteIdent(table))
	if wh.clause != "" {
		q += " WHERE " + wh.clause
	}
	res, err := s.db.Exec(q, wh.args...)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	return opResult(nil, int(n)), nil
}

// ── 结果 / 参数 ────────────────────────────────────────────────────────────

// opResult 就是头文件声明的 DatabaseOpResult { array<any> outputList; int rowNum }。
func opResult(list []any, n int) map[string]any {
	if list == nil {
		list = []any{}
	}
	return map[string]any{"outputList": list, "rowNum": int64(n)}
}

func singleTable(args map[string]any) (string, error) {
	tables, _ := unwrapValue(args["tables"]).([]any)
	if len(tables) == 0 {
		return "", fmt.Errorf("local database: tables is empty")
	}
	if len(tables) > 1 {
		return "", fmt.Errorf("local database: one node operates one table, got %d", len(tables))
	}
	id := fmt.Sprint(unwrapValue(tables[0]))
	if id == "" || id == "<nil>" {
		return "", fmt.Errorf("local database: empty table id")
	}
	return tableName(id), nil
}

func (s *dbStore) selectColumns(table string, v any) ([]string, error) {
	list, _ := unwrapValue(v).([]any)
	var cols []string
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := fmt.Sprint(unwrapValue(m["fieldID"]))
		if id == "" || id == "<nil>" {
			continue
		}
		cols = append(cols, colName(id))
	}
	if len(cols) > 0 {
		return cols, nil
	}
	// 未声明列 = 全列（平台语义：select *）。交给 SELECT *，按实际列名回填。
	return nil, nil
}

func buildOrderBy(v any) (string, error) {
	list, _ := unwrapValue(v).([]any)
	var parts []string
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := fmt.Sprint(unwrapValue(m["fieldID"]))
		if id == "" || id == "<nil>" {
			continue
		}
		dir := "ASC"
		if asc, ok := unwrapValue(m["isAsc"]).(bool); ok && !asc {
			dir = "DESC"
		}
		parts = append(parts, colName(id)+" "+dir)
	}
	return strings.Join(parts, ", "), nil
}

func limitOf(v any) (int, bool) {
	switch n := unwrapValue(v).(type) {
	case int64:
		if n > 0 {
			return int(n), true
		}
	case int:
		if n > 0 {
			return n, true
		}
	case float64:
		if n > 0 {
			return int(n), true
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil && i > 0 {
			return i, true
		}
	}
	return 0, false
}

func quoteIdents(cols []string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = quoteIdent(c)
	}
	return strings.Join(out, ", ")
}

// normalizeSQLValue 把驱动类型收敛到 JSON 可表达的形状。
func normalizeSQLValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case int64, float64, string, bool, nil:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// ── 注册 ──────────────────────────────────────────────────────────────────

// registerDatabase 注册 database 的四个算子（SQLite 后端）。
// 库名/函数名 → 实现的绑定仍然走 bindings.yaml（一个机制，不两条路）。
func registerDatabase(m *plugin.Manager, path string) error {
	store, err := openStore(path)
	if err != nil {
		return err
	}
	call := func(fn func(map[string]any) (any, error)) plugin.CallFunc {
		return func(_ context.Context, args map[string]any) (any, error) {
			store.mu.Lock()
			defer store.mu.Unlock()
			return fn(args)
		}
	}
	p := &plugin.Plugin{
		ID:      "local_database",
		Version: "0.1.0",
		Type:    plugin.PluginCallable,
		Tools: []plugin.Tool{
			{Name: "insert", Desc: "SQLite 本地实现：插入记录", Capabilities: []string{"database.insert"}, Call: call(store.insert)},
			{Name: "select", Desc: "SQLite 本地实现：查询记录", Capabilities: []string{"database.select"}, Call: call(store.selectRows)},
			{Name: "update", Desc: "SQLite 本地实现：更新记录", Capabilities: []string{"database.update"}, Call: call(store.update)},
			{Name: "delete", Desc: "SQLite 本地实现：删除记录", Capabilities: []string{"database.delete"}, Call: call(store.deleteRows)},
		},
	}
	return m.Register(p)
}
