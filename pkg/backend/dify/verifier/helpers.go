package verifier

// Small formatting/collection helpers shared by the rules. Message
// formatting mirrors Python f-string interpolation exactly: a sorted
// set/list interpolates as its repr, e.g. ['a', 'b'] with single quotes
// (Python list repr). Node ids / port names never carry quotes or
// backslashes, so the plain join below is byte-identical to repr() for
// every value the rules interpolate.

import (
	"reflect"
	"sort"
	"strings"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/types"
)

// pyList renders a sorted string slice the way Python's repr() renders a
// list of strings: ['a', 'b'] (single quotes, comma+space separators).
func pyList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	return "['" + strings.Join(items, "', '") + "']"
}

// sortedCopy returns a new sorted copy of items (never mutates the input).
func sortedCopy(items []string) []string {
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}

// formatSorted renders the sorted copy of items as a Python list repr.
func formatSorted(items []string) string { return pyList(sortedCopy(items)) }

// uniqueStrings reports whether every entry in items is distinct.
func uniqueStrings(items []string) bool {
	seen := map[string]bool{}
	for _, item := range items {
		if seen[item] {
			return false
		}
		seen[item] = true
	}
	return true
}

// formatSortedMapKeys renders the sorted key set of a map as a Python list
// repr, mirroring Python's sorted(dict).
func formatSortedMapKeys[V any](m map[string]V) string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return formatSorted(keys)
}

// deliveryMethodsEqual mirrors Python's tuple(data.delivery_methods) !=
// fingerprint.delivery_methods: value equality between tuple[dict, ...]
// elements. Python dict == is by value (order-insensitive within each
// dict), exactly reflect.DeepEqual's map semantics, so element-wise
// DeepEqual is the faithful port.
func deliveryMethodsEqual(actual []any, expected []any) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range expected {
		if !reflect.DeepEqual(actual[i], expected[i]) {
			return false
		}
	}
	return true
}

// pyBool renders a bool the way Python's str(True)/str(False) does
// (f-string interpolation of a bool): "True" / "False".
func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// dataTypeName mirrors type(node.data).__name__ for the canonical-assignment
// message: the concrete Go struct name equals the Python dataclass name.
func dataTypeName(data dify.NodeData) string {
	return reflect.TypeOf(data).Elem().Name()
}

// dataContainerFlags reads the Python DataNode base container-ownership
// fields (is_in_iteration / iteration_id / is_in_loop / loop_id) off any
// concrete data node via the embedded *dify.DataNode.
func dataContainerFlags(data dify.NodeData) (isInIteration bool, iterationID string, isInLoop bool, loopID string) {
	type flagsProvider interface {
		ContainerFlags() (bool, string, bool, string)
	}
	if provider, ok := data.(flagsProvider); ok {
		return provider.ContainerFlags()
	}
	return false, "", false, ""
}

func nodeDataIsInIteration(node *dify.NodeDecl) bool {
	flag, _, _, _ := dataContainerFlags(node.Data)
	return flag
}

func nodeDataIterationID(node *dify.NodeDecl) string {
	_, id, _, _ := dataContainerFlags(node.Data)
	return id
}

func nodeDataIsInLoop(node *dify.NodeDecl) bool {
	_, _, flag, _ := dataContainerFlags(node.Data)
	return flag
}

func nodeDataLoopID(node *dify.NodeDecl) string {
	_, _, _, id := dataContainerFlags(node.Data)
	return id
}

// fmtList renders a string slice as a Python list repr (f-string of a list).
func fmtList(items []string) string { return pyList(items) }

// containsString reports whether items contains target.
func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// sortedKeys returns the sorted key list of a set (map[string]bool).
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sortedKeysMap returns the sorted key list of a map.
func sortedKeysMap[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// differenceSortedKeys returns the sorted elements of a not present in b.
func differenceSortedKeys(a, b map[string]bool) []string {
	var out []string
	for key := range a {
		if !b[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// descriptorsEqual compares two type descriptors structurally. Python
// compares TypeDescriptor dataclasses with == (field-by-field value
// equality); Go's Descriptor is a comparable-by-reflect struct, so
// reflect.DeepEqual is the faithful analogue (ordered Fields list matches
// Python's ordered RecordType.fields).
func descriptorsEqual(a, b *types.Descriptor) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a, b)
}
