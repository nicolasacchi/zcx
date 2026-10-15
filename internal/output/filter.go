package output

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
)

// ApplyFilter applies a gjson path expression. This is gjson syntax, NOT jq:
// arrays use `#.field`, projections use `#.{a:a,b:b}`.
func ApplyFilter(data json.RawMessage, path string) (json.RawMessage, error) {
	if path == "" {
		return data, nil
	}
	result := gjson.GetBytes(data, path)
	if !result.Exists() {
		return json.RawMessage("null"), nil
	}
	if result.Raw == "" {
		return nil, fmt.Errorf("gjson filter %q returned non-JSON result", path)
	}
	return json.RawMessage(result.Raw), nil
}
