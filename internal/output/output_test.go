package output

import (
	"encoding/json"
	"testing"
)

func TestApplyFilter(t *testing.T) {
	data := json.RawMessage(`[{"id":1,"n":"a"},{"id":2,"n":"b"}]`)
	got, err := ApplyFilter(data, "#.id")
	if err != nil || string(got) != "[1,2]" {
		t.Fatalf("%s %v", got, err)
	}
	if got, _ := ApplyFilter(data, "nope"); string(got) != "null" {
		t.Errorf("missing path: %s", got)
	}
	if got, _ := ApplyFilter(data, ""); string(got) != string(data) {
		t.Error("empty filter must pass through")
	}
}

func TestFormatValueAndTruncate(t *testing.T) {
	if FormatValue(float64(3)) != "3" || FormatValue(nil) != "" || FormatValue(true) != "true" {
		t.Error("FormatValue")
	}
	if FormatValue([]any{"a", "b"}) != "a, b" {
		t.Error("slice")
	}
	if got := Truncate(5)("abcdefgh"); got != "ab..." {
		t.Errorf("truncate %q", got)
	}
}

func TestRegisterColumns(t *testing.T) {
	RegisterColumns("t.x", []ColumnDef{{Header: "A", Key: "a.b"}})
	if err := printTable("t.x", json.RawMessage(`[{"a":{"b":1}}]`)); err != nil {
		t.Fatal(err)
	}
	if printTable("unknown", json.RawMessage(`[]`)) == nil {
		t.Error("unknown command must fall back to JSON")
	}
}
