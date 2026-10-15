package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/tidwall/gjson"
	"golang.org/x/term"
)

// FormatFunc renders one cell from the raw JSON-decoded value.
type FormatFunc func(any) string

// ColumnDef is one table column. Key is a gjson path into each row object
// (plain "id" or nested "lead.email").
type ColumnDef struct {
	Header string
	Key    string
	Format FormatFunc
}

var (
	mu      sync.RWMutex
	columns = map[string][]ColumnDef{}
)

// RegisterColumns declares the human (TTY) table of a command. Feature files
// call it from init(); a command without columns always renders JSON.
func RegisterColumns(command string, cols []ColumnDef) {
	mu.Lock()
	defer mu.Unlock()
	columns[command] = cols
}

func init() {
	RegisterColumns("chats.list", []ColumnDef{
		{Header: "ID", Key: "id"},
		{Header: "NAME", Key: "name"},
		{Header: "DOMAIN", Key: "domain"},
		{Header: "CREATED", Key: "created_at"},
	})
}

func printTable(command string, data json.RawMessage) error {
	mu.RLock()
	cols, ok := columns[command]
	mu.RUnlock()
	if !ok {
		return fmt.Errorf("no table definition for %s", command)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(data, &rows); err != nil {
		if !gjson.ValidBytes(data) || !gjson.ParseBytes(data).IsObject() {
			return fmt.Errorf("cannot render as table")
		}
		rows = []json.RawMessage{data}
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	if term.IsTerminal(int(os.Stdout.Fd())) {
		t.SetStyle(table.StyleLight)
	} else {
		t.SetStyle(table.StyleDefault)
	}
	header := make(table.Row, len(cols))
	for i, c := range cols {
		header[i] = c.Header
	}
	t.AppendHeader(header)
	for _, row := range rows {
		r := make(table.Row, len(cols))
		for i, c := range cols {
			v := gjson.GetBytes(row, c.Key).Value()
			if c.Format != nil {
				r[i] = c.Format(v)
			} else {
				r[i] = FormatValue(v)
			}
		}
		t.AppendRow(r)
	}
	t.Render()
	return nil
}

// FormatValue renders a decoded JSON value as a table cell.
func FormatValue(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%.2f", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(parts, ", ")
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

// Truncate returns a FormatFunc cutting cells to n runes with an ellipsis.
func Truncate(n int) FormatFunc {
	return func(v any) string {
		r := []rune(FormatValue(v))
		if len(r) > n {
			return string(r[:n-3]) + "..."
		}
		return string(r)
	}
}
