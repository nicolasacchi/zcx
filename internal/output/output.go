// Package output is the zcx JSON/table dispatcher (mirrors pmx/ddx). Humans on
// a TTY get a go-pretty table when a command has a registered column set;
// everything else - pipes, --json, --jq, and LLM callers (CLAUDECODE) - gets
// clean JSON.
package output

import (
	"encoding/json"
	"fmt"
	"os"

	cliout "github.com/nicolasacchi/clicore/output"
)

// IsJSON reports whether output must be JSON (--json, --jq, or non-TTY).
func IsJSON(jsonFlag bool, jqFilter string) bool { return cliout.IsJSON(jsonFlag, jqFilter) }

// CapArray applies the agent row cap (explicit --limit wins; CLAUDECODE gets
// 100) to a top-level JSON array, noting the truncation on stderr.
func CapArray(data []byte, userLimit int) []byte { return cliout.CapAgentArray(data, userLimit) }

// PrintData outputs data as a table (TTY + registered command) or JSON.
func PrintData(command string, data json.RawMessage, jsonMode bool, jqFilter string) error {
	if !jsonMode {
		if err := printTable(command, data); err == nil {
			return nil
		}
	}
	if jqFilter != "" {
		filtered, err := ApplyFilter(data, jqFilter)
		if err != nil {
			return err
		}
		data = filtered
	}
	return printJSON(data)
}

// PrintError writes the {ok:false,error,status_code} envelope to stdout so
// agents reading stdout still get machine-parseable errors.
func PrintError(errMsg string, statusCode int) {
	obj := map[string]any{"ok": false, "error": errMsg}
	if statusCode > 0 {
		obj["status_code"] = statusCode
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(obj)
}

func printJSON(data json.RawMessage) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	return nil
}

// PrintJSONValue prints any Go value as indented JSON on stdout.
func PrintJSONValue(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	return nil
}
