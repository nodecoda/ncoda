package localtools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// pair 构造平台参数形态的一条 {name, input}。语料的两种壳都要能吃：
// 简化壳 {value:X} 与带类型壳 {type,value:{content,type}}。
func pair(name string, v any) map[string]any {
	return map[string]any{"name": name, "input": map[string]any{"value": v}}
}

func typedPair(name string, v any) map[string]any {
	return map[string]any{"name": name, "input": map[string]any{
		"type": "string", "value": map[string]any{"content": v, "type": "literal"},
	}}
}

func dbRecords(t *testing.T, out any) ([]any, int64) {
	t.Helper()
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want map", out)
	}
	list, _ := m["outputList"].([]any)
	n, _ := m["rowNum"].(int64)
	return list, n
}

func TestDatabaseInsertSelectRoundTrip(t *testing.T) {
	s, err := openStore("")
	if err != nil {
		t.Fatal(err)
	}
	// 形态一：一条对 = 一条列（create_video / produce 的写法）
	if _, err := s.insert(map[string]any{
		"tables": []any{"7577398685760421939"},
		"values": []any{
			pair("fieldID", "1806321126401"), pair("fieldValue", "艺术展览"),
			pair("fieldID", "1806321126402"), pair("fieldValue", "img-1"),
		},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 形态二：一条对数组 = 一行（database_curd 的写法）
	if _, err := s.insert(map[string]any{
		"tables": []any{"7577398685760421939"},
		"values": []any{[]any{
			typedPair("fieldID", "1806321126401"), typedPair("fieldValue", "产品发布"),
			typedPair("fieldID", "1806321126402"), typedPair("fieldValue", "img-2"),
		}},
	}); err != nil {
		t.Fatalf("insert (typed shape): %v", err)
	}

	out, err := s.selectRows(map[string]any{
		"tables":  []any{"7577398685760421939"},
		"columns": []any{map[string]any{"fieldID": "1806321126401", "isDistinct": false}},
		"rows":    int64(100),
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	rows, n := dbRecords(t, out)
	if n != 2 {
		t.Fatalf("rowNum = %d, want 2 (rows=%#v)", n, rows)
	}
	if got := rows[0].(map[string]any)["1806321126401"]; got != "艺术展览" {
		t.Fatalf("row0 = %#v", rows[0])
	}
}

func TestDatabaseConditionOrderLimit(t *testing.T) {
	s, _ := openStore("")
	for _, v := range []int64{3, 1, 2} {
		if _, err := s.insert(map[string]any{
			"tables": []any{"t1"},
			"values": []any{[]any{
				pair("fieldID", "code"), pair("fieldValue", v),
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	cond := map[string]any{
		"conditionList": []any{[]any{
			map[string]any{"value": "code"}, map[string]any{"value": "GREATER_THAN"}, map[string]any{"value": float64(1)},
		}},
		"logic": "AND",
	}
	out, err := s.selectRows(map[string]any{
		"tables":    []any{"t1"},
		"columns":   []any{map[string]any{"fieldID": "code"}},
		"condition": cond,
		"order_by":  []any{map[string]any{"fieldID": "code", "isAsc": false}},
		"rows":      int64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, n := dbRecords(t, out)
	if n != 1 {
		t.Fatalf("rowNum = %d, want 1", n)
	}
	if got := rows[0].(map[string]any)["code"]; got != int64(3) {
		t.Fatalf("code = %#v, want 3 (DESC + LIMIT 1)", got)
	}
}

func TestDatabaseUpdateAndDelete(t *testing.T) {
	s, _ := openStore("")
	ins := map[string]any{"tables": []any{"t2"}, "values": []any{[]any{
		pair("fieldID", "name"), pair("fieldValue", "a"),
	}}}
	if _, err := s.insert(ins); err != nil {
		t.Fatal(err)
	}
	byName := map[string]any{
		"conditionList": []any{[]any{
			map[string]any{"value": "name"}, map[string]any{"value": "EQUAL"}, map[string]any{"value": "a"},
		}},
		"logic": "AND",
	}
	upd, err := s.update(map[string]any{
		"tables":    []any{"t2"},
		"values":    []any{[]any{pair("fieldID", "name"), pair("fieldValue", "b")}},
		"condition": byName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, n := dbRecords(t, upd); n != 1 {
		t.Fatalf("update rowNum = %d, want 1", n)
	}
	byNameB := map[string]any{
		"conditionList": []any{[]any{
			map[string]any{"value": "name"}, map[string]any{"value": "EQUAL"}, map[string]any{"value": "b"},
		}},
		"logic": "AND",
	}
	del, err := s.deleteRows(map[string]any{"tables": []any{"t2"}, "condition": byNameB})
	if err != nil {
		t.Fatal(err)
	}
	if _, n := dbRecords(t, del); n != 1 {
		t.Fatalf("delete rowNum = %d, want 1", n)
	}
}

// 没实现的算子必须报错，不许静默忽略（否则结果会"看起来对"）。
func TestDatabaseUnsupportedConditionIsLoud(t *testing.T) {
	s, _ := openStore("")
	cond := map[string]any{"conditionList": []any{[]any{
		map[string]any{"value": "x"}, map[string]any{"value": "REGEX_MATCH"}, map[string]any{"value": "y"},
	}}}
	if _, err := s.selectRows(map[string]any{"tables": []any{"t3"}, "condition": cond}); err == nil {
		t.Fatal("unsupported operator was silently ignored")
	}
}

// 给路径 = 真持久化：跨连接能查到上一次写的行。
func TestDatabasePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.sqlite")
	s1, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.insert(map[string]any{
		"tables": []any{"t4"},
		"values": []any{[]any{pair("fieldID", "k"), pair("fieldValue", "v")}},
	}); err != nil {
		t.Fatal(err)
	}
	s1.db.Close()

	s2, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.db.Close()
	out, err := s2.selectRows(map[string]any{"tables": []any{"t4"}, "columns": []any{map[string]any{"fieldID": "k"}}})
	if err != nil {
		t.Fatal(err)
	}
	rows, n := dbRecords(t, out)
	if n != 1 || rows[0].(map[string]any)["k"] != "v" {
		t.Fatalf("after reopen: rowNum=%d rows=%#v", n, rows)
	}
}

// 绑定面：ncoda 里写 database.select(...) 必须解析到本地实现。
func TestDatabaseBindsThroughRegistry(t *testing.T) {
	m := plugin.New()
	if _, err := Register(m, Options{ArtifactDir: t.TempDir()}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, op := range []string{"insert", "select", "update", "delete"} {
		if _, ok := m.Lookup("database/" + op); !ok {
			t.Fatalf("database/%s not bound; names=%v", op, m.Names())
		}
	}
	if _, err := m.Call(context.Background(), "database/select", map[string]any{"tables": []any{"none"}}); err != nil {
		t.Fatalf("Call(database/select): %v", err)
	}
}
