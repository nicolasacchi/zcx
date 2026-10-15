package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	cliconfig "github.com/nicolasacchi/clicore/config"
	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/redact"
	"github.com/nicolasacchi/zcx/internal/timeparse"
	"github.com/spf13/cobra"
)

func init() {
	Register(func(root *cobra.Command) { root.AddCommand(exportCmd()) })
}

func exportCmd() *cobra.Command {
	c := &cobra.Command{Use: "export", Short: "Export data from the local cache"}
	c.AddCommand(exportTranscriptsCmd())
	return c
}

type exportMessage struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	Text      string `json:"text"`
}

type exportConversation struct {
	ID                 int64           `json:"id"`
	CreatedAt          string          `json:"created_at"`
	StartPage          string          `json:"start_page,omitempty"`
	Channel            string          `json:"channel,omitempty"`
	Escalated          bool            `json:"escalated"`
	EscalatedAt        string          `json:"escalated_at,omitempty"`
	EscalationResolved string          `json:"escalation_resolved_at,omitempty"`
	ShareURL           string          `json:"share_url,omitempty"`
	Messages           []exportMessage `json:"messages"`
}

func exportRole(m client.Message) string {
	switch {
	case m.Role == "user":
		return "CUSTOMER"
	case m.ManualReply:
		return "OPERATOR"
	default:
		return "BOT"
	}
}

func exportBuild(c client.Conversation, raw, maskOrders bool) exportConversation {
	out := exportConversation{
		ID:        c.ID,
		CreatedAt: timeparse.FormatRomeSec(c.CreatedAt),
		Channel:   c.Channel,
		ShareURL:  c.ShareURL,
		Messages:  []exportMessage{},
	}
	if c.StartLink != nil {
		out.StartPage = *c.StartLink
		if !raw { // the landing URL is customer-controlled: drop query/fragment, mask PII
			out.StartPage = redact.Text(stripQuery(out.StartPage), redact.Options{})
		}
	}
	if c.LastEscalatedAt != nil {
		out.Escalated = true
		out.EscalatedAt = timeparse.FormatRomeSec(*c.LastEscalatedAt)
	}
	if c.LastEscalationResolvedAt != nil {
		out.EscalationResolved = timeparse.FormatRomeSec(*c.LastEscalationResolvedAt)
	}
	for _, m := range c.Messages {
		role := exportRole(m)
		text := m.Message
		if role != "CUSTOMER" {
			text = redact.StripHTML(text)
		}
		if !raw {
			text = redact.Text(text, redact.Options{MaskOrders: maskOrders})
		}
		out.Messages = append(out.Messages, exportMessage{
			ID: m.ID, Role: role, CreatedAt: timeparse.FormatRomeSec(m.CreatedAt), Text: text,
		})
	}
	return out
}

func exportWriteText(w io.Writer, c exportConversation) {
	fmt.Fprintf(w, "=== Conversation %d ===\n", c.ID)
	fmt.Fprintf(w, "created: %s (Europe/Rome)\n", c.CreatedAt)
	if c.StartPage != "" {
		fmt.Fprintf(w, "start page: %s\n", c.StartPage)
	}
	if c.Escalated {
		line := "escalated: " + c.EscalatedAt
		if c.EscalationResolved != "" {
			line += " (resolved " + c.EscalationResolved + ")"
		}
		fmt.Fprintln(w, line)
	}
	if c.ShareURL != "" {
		fmt.Fprintf(w, "url: %s\n", c.ShareURL)
	}
	fmt.Fprintln(w)
	for _, m := range c.Messages {
		fmt.Fprintf(w, "[%s] %s: %s\n", m.CreatedAt, m.Role, strings.ReplaceAll(m.Text, "\n", "\n    "))
	}
	fmt.Fprintln(w)
}

func exportTranscriptsCmd() *cobra.Command {
	var from, to, format, out string
	var raw, maskOrders bool
	c := &cobra.Command{
		Use:   "transcripts",
		Short: "Export conversation transcripts from the cache (redacted by default)",
		Long: "Writes one block per conversation (created in [--from, --to)) from the local cache.\n" +
			"Personal data (emails, phones, codice fiscale, IBAN, cards, addresses) is masked unless --raw.\n" +
			"Unless --offline, an incremental sync runs first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "text" && format != "jsonl" {
				return fmt.Errorf("--format must be text or jsonl, got %q", format)
			}
			f, t, err := timeparse.ParseRange(from, to, time.Now())
			if err != nil {
				return err
			}
			e, err := getEnv(true, offlineFlag)
			if err != nil {
				return err
			}
			if !offlineFlag {
				if err := refreshOrWarn(e, EnsureFreshEnv); err != nil {
					return err
				}
			}
			st, err := e.Store()
			if err != nil {
				return err
			}
			convs, err := st.LoadAll(f, t)
			if err != nil {
				return err
			}
			if raw {
				fmt.Fprintln(os.Stderr, "WARNING: --raw disables redaction; the output contains customer PII. Handle accordingly.")
			}
			var w io.Writer = cmd.OutOrStdout()
			var fileBuf bytes.Buffer
			if out != "" {
				w = &fileBuf // written atomically with mode 0600 at the end
			}
			bw := bufio.NewWriter(w)
			enc := json.NewEncoder(bw)
			enc.SetEscapeHTML(false)
			for _, cv := range convs {
				ec := exportBuild(cv, raw, maskOrders)
				if format == "jsonl" {
					if err := enc.Encode(ec); err != nil {
						return err
					}
				} else {
					exportWriteText(bw, ec)
				}
			}
			if err := bw.Flush(); err != nil {
				return err
			}
			if out != "" {
				if err := cliconfig.WriteFileAtomic(out, fileBuf.Bytes(), 0o600); err != nil {
					return fmt.Errorf("write %s: %w", out, err)
				}
			}
			progressf("exported %d conversations", len(convs))
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "start (inclusive; Rome date/datetime, RFC3339 or relative like 7d)")
	c.Flags().StringVar(&to, "to", "", "end (bare date includes the whole Rome day)")
	c.Flags().StringVar(&format, "format", "text", "output format: text|jsonl")
	c.Flags().StringVar(&out, "out", "", "write to FILE (mode 0600) instead of stdout")
	addStrictFlag(c)
	c.Flags().BoolVar(&raw, "raw", false, "disable redaction (prints a warning to stderr)")
	c.Flags().BoolVar(&maskOrders, "mask-orders", false, "also mask order numbers (F#########)")
	return c
}

// stripQuery removes the query string and fragment of a URL.
func stripQuery(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i]
	}
	return u
}
