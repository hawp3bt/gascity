package main

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestEmergencyCommandDoesNotImportBeads(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "cmd_emergency.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseFile(cmd_emergency.go): %v", err)
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("unquote import path %s: %v", imp.Path.Value, err)
		}
		if strings.Contains(path, "/internal/beads") {
			t.Fatalf("cmd_emergency.go imports %q; emergency writer path must stay dolt-independent", path)
		}
	}
}
