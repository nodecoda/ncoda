// Tests for the `library <name> @ <semver>;` identity declaration.
//
// The declaration names the unit; it carries no IR and no ABI input, so the
// negative-control design is symmetric: a file with the line and the same
// file without it must parse to the same program modulo the declaration
// itself.
package parser

//@feature library

import (
	"strings"
	"testing"
)

func TestLibraryDeclParsesNameAndVersion(t *testing.T) {
	cases := []struct {
		name, version string
	}{
		{"adjustment", "0.1.0"},
		{"image_tools", "1.12.30"},
		{"a", "10.0.200"},
	}
	for _, tc := range cases {
		src := "library " + tc.name + " @ " + tc.version + ";\n" +
			"function main() {}\n"
		prog := parseOK(t, src)
		lib := prog.Library()
		if lib == nil {
			t.Fatalf("%s: Library() = nil, want a declaration", tc.name)
		}
		if got := lib.Name(); got != tc.name {
			t.Errorf("Name() = %q, want %q", got, tc.name)
		}
		if got := lib.Version(); got != tc.version {
			t.Errorf("Version() = %q, want %q", got, tc.version)
		}
	}
}

func TestLibraryDeclIsOptional(t *testing.T) {
	// Negative control for "declaration required": the 174 committed
	// headers have no declaration line and must keep parsing.
	if got := parseOK(t, "function main() {}").Library(); got != nil {
		t.Fatalf("Library() = %v, want nil when no declaration is present", got)
	}
}

func TestLibraryDeclDoesNotChangeRestOfProgram(t *testing.T) {
	// Same source, with and without the line: every other Program field
	// must be identical. This is the parser-level half of the ABI/IR
	// invariant.
	body := "type Point = {\n  string x;\n};\nfunction main() -> int { return 1; }\n"
	withLib := parseOK(t, "library geometry @ 0.2.1;\n"+body)
	without := parseOK(t, body)
	if withLib.Entry() == nil || without.Entry() == nil {
		t.Fatal("both programs must have an entry")
	}
	if withLib.Entry().Name() != without.Entry().Name() {
		t.Errorf("entry name changed: %q vs %q", withLib.Entry().Name(), without.Entry().Name())
	}
	if len(withLib.TypeDecls()) != len(without.TypeDecls()) {
		t.Errorf("type decls changed: %d vs %d", len(withLib.TypeDecls()), len(without.TypeDecls()))
	}
	if withLib.Library() == nil || without.Library() != nil {
		t.Error("the declaration itself must be the only difference")
	}
}

func TestLibraryDeclAfterOtherDeclsIsAllowed(t *testing.T) {
	prog := parseOK(t, "function main() {}\nlibrary late @ 0.1.0;\n")
	if prog.Library() == nil || prog.Library().Name() != "late" {
		t.Fatalf("Library() = %v, want name \"late\"", prog.Library())
	}
}

func TestLibraryDeclRejectsMalformed(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"missing-at", "library a;", "Expected '@'"},
		{"missing-version", "library a @;", "must be MAJOR.MINOR.PATCH"},
		{"two-part-version", "library a @ 1.0;", "must be MAJOR.MINOR.PATCH"},
		{"four-part-version", "library a @ 1.2.3.4;", "must be MAJOR.MINOR.PATCH"},
		{"non-numeric", "library a @ v1.2.3;", "must be MAJOR.MINOR.PATCH"},
		{"missing-semicolon", "library a @ 1.2.3", "Expected ';'"},
		{"missing-name", "library @ 1.2.3;", "Expected library name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := New(lex(tc.src)).Parse()
			if len(errs) == 0 {
				t.Fatalf("parse %q: want an error, got none", tc.src)
			}
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message)
			}
			if !strings.Contains(strings.Join(msgs, " | "), tc.want) {
				t.Errorf("errors = %v, want one containing %q", msgs, tc.want)
			}
		})
	}
}

func TestLibraryDeclRejectsDuplicate(t *testing.T) {
	_, errs := New(lex("library a @ 0.1.0;\nlibrary b @ 0.1.0;\nfunction main() {}\n")).Parse()
	if len(errs) == 0 {
		t.Fatal("want a duplicate-declaration error")
	}
	if !strings.Contains(errs[0].Message, "Duplicate library declaration") {
		t.Errorf("Message = %q, want it to report a duplicate", errs[0].Message)
	}
}
