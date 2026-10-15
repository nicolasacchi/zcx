package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	cliconfig "github.com/nicolasacchi/clicore/config"
	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/nicolasacchi/zcx/internal/promptops"
	"github.com/nicolasacchi/zcx/internal/store"
	"github.com/nicolasacchi/zcx/internal/textdiff"
	"github.com/nicolasacchi/zcx/internal/timeparse"
	"github.com/spf13/cobra"
)

func init() {
	output.RegisterColumns("prompt.history", []output.ColumnDef{
		{Header: "BACKUP", Key: "name"},
		{Header: "TIME (Rome)", Key: "time_rome"},
		{Header: "SIZE", Key: "size"},
		{Header: "SHA256", Key: "sha256"},
	})
	Register(func(root *cobra.Command) { root.AddCommand(promptCmd()) })
}

func promptCmd() *cobra.Command {
	c := &cobra.Command{Use: "prompt", Short: "Read and (safely) write the chat master prompt"}

	get := &cobra.Command{
		Use:   "get",
		Short: "Print the live master prompt",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, _ := cmd.Flags().GetBool("raw")
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			p, err := e.Client.GetPrompt(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			if raw {
				_, err = io.WriteString(os.Stdout, p.MasterPrompt)
				return err
			}
			if isJSONMode() {
				return printValue("", map[string]any{
					"chat_id": e.ChatID, "master_prompt": p.MasterPrompt,
					"master_prompt_max_length": p.MasterPromptMaxLength,
					"length":                   utf8.RuneCountInString(p.MasterPrompt),
					"sha256":                   store.SHA256(p.MasterPrompt),
				})
			}
			if _, err := fmt.Fprintln(os.Stdout, p.MasterPrompt); err != nil {
				return err
			}
			progressf("length %d/%d characters, sha256 %s", utf8.RuneCountInString(p.MasterPrompt), p.MasterPromptMaxLength, store.SHA256(p.MasterPrompt))
			return nil
		},
	}
	get.Flags().Bool("raw", false, "print the prompt exactly as stored, nothing else")

	pull := &cobra.Command{
		Use:   "pull",
		Short: "Save the live prompt to a file, back it up locally and print its sha256",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			outFile, _ := cmd.Flags().GetString("output")
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			p, err := e.Client.GetPrompt(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			st, err := e.Store()
			if err != nil {
				return err
			}
			backup, err := st.BackupPrompt(p.MasterPrompt)
			if err != nil {
				return fmt.Errorf("store backup: %w", err)
			}
			sha := store.SHA256(p.MasterPrompt)
			if outFile != "" {
				if err := cliconfig.WriteFileAtomic(outFile, []byte(p.MasterPrompt), 0o600); err != nil {
					return fmt.Errorf("write %s: %w", outFile, err)
				}
			}
			if outFile == "" {
				// the prompt itself goes to stdout; metadata to stderr.
				if _, err := io.WriteString(os.Stdout, p.MasterPrompt); err != nil {
					return err
				}
				progressf("sha256 %s\nbackup %s", sha, backup)
				return nil
			}
			if isJSONMode() {
				return printValue("", map[string]any{"file": outFile, "sha256": sha, "backup": backup,
					"length": utf8.RuneCountInString(p.MasterPrompt)})
			}
			_, err = fmt.Fprintf(os.Stdout, "sha256 %s\nfile   %s\nbackup %s\n", sha, outFile, backup)
			return err
		},
	}
	pull.Flags().StringP("output", "o", "", "write the prompt to this file (default: stdout)")

	diff := &cobra.Command{
		Use:   "diff FILE",
		Short: "Unified diff of the live prompt against FILE",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			p, err := e.Client.GetPrompt(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			d := textdiff.Unified(p.MasterPrompt, promptops.Normalize(string(b)), "live", args[0])
			if isJSONMode() {
				return printValue("", map[string]any{"identical": d == "", "diff": d, "live_sha256": store.SHA256(p.MasterPrompt)})
			}
			if d == "" {
				progressf("no differences")
				return nil
			}
			_, err = fmt.Fprint(os.Stdout, d)
			return err
		},
	}

	push := &cobra.Command{
		Use:   "push FILE --base-sha SHA",
		Short: "Replace the live prompt with FILE (guarded: base sha, backup, verify)",
		Long: "Replaces the WHOLE live master prompt. Safety order: trim and reject blank, " +
			"fetch live, refuse if sha256(live) != --base-sha, refuse if too long, show diff, " +
			"no-op if identical, require --yes (or preview with --dry-run), back up the live " +
			"prompt locally, send a single PATCH (never retried), re-read and verify.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, _ := cmd.Flags().GetString("base-sha")
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			return promptWrite(string(b), args[0], base, "push "+args[0])
		},
	}
	push.Flags().String("base-sha", "", "sha256 of the live prompt you based FILE on (printed by 'prompt pull')")

	history := &cobra.Command{
		Use:   "history",
		Short: "List local prompt backups, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := getEnv(true, true)
			if err != nil {
				if e, err = getEnv(true, false); err != nil {
					return err
				}
			}
			st, err := e.Store()
			if err != nil {
				return err
			}
			bs, err := st.ListPromptBackups()
			if err != nil {
				if os.IsNotExist(err) {
					bs = nil
				} else {
					return err
				}
			}
			rows := make([]map[string]any, 0, len(bs))
			for _, x := range bs {
				rows = append(rows, map[string]any{"name": x.Name, "path": x.Path, "time": x.Time,
					"time_rome": timeparse.FormatRomeSec(x.Time), "size": x.Size, "sha256": x.SHA256})
			}
			return printValue("prompt.history", rows)
		},
	}

	restore := &cobra.Command{
		Use:   "restore BACKUP --base-sha SHA",
		Short: "Write a local backup back as the live prompt (same guards as push)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, _ := cmd.Flags().GetString("base-sha")
			e, err := getEnv(true, true)
			if err != nil {
				if e, err = getEnv(true, false); err != nil {
					return err
				}
			}
			st, err := e.Store()
			if err != nil {
				return err
			}
			text, err := st.ReadPromptBackup(args[0])
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return &client.APIError{Status: 404, Kind: "not_found", Detail: err.Error(), Hint: "see 'zcx prompt history'"}
				}
				return err
			}
			return promptWrite(text, args[0], base, "restore "+args[0])
		},
	}
	restore.Flags().String("base-sha", "", "sha256 of the CURRENT live prompt (from 'prompt pull' or 'prompt get --json')")

	c.AddCommand(get, pull, diff, push, history, restore)
	return c
}

// promptRefusal converts a pre-flight refusal into the CLI error (exit 3 for
// invalid content, 6 for write_locked).
func promptRefusal(err error) error {
	var r *promptops.Refusal
	if !errors.As(err, &r) {
		return err
	}
	switch r.Kind {
	case promptops.KindBlank, promptops.KindTooLong:
		return &ExitError{Code: 3, Msg: r.Msg + "\nHint: " + r.Hint}
	default:
		return &client.APIError{Kind: "write_locked", Detail: r.Msg, Hint: r.Hint}
	}
}

// promptWrite runs the guarded write. text is the candidate prompt (file or
// backup content), source its label, action the gate wording.
func promptWrite(text, source, baseSHA, action string) error {
	// 1. trim + reject blank happens first, without any network call.
	if promptops.Normalize(text) == "" {
		return promptRefusal(&promptops.Refusal{Kind: promptops.KindBlank,
			Msg: "the prompt is blank after trimming whitespace; the server would reject it (422)", Hint: "check " + source})
	}
	e, err := getEnv(true, false)
	if err != nil {
		return err
	}
	// 2. GET live.
	live, err := e.Client.GetPrompt(ctx(), e.ChatID)
	if err != nil {
		return err
	}
	// 3. base-sha, length, no-op detection.
	plan, err := promptops.Preflight(text, live.MasterPrompt, live.MasterPromptMaxLength, baseSHA)
	if err != nil {
		return promptRefusal(err)
	}
	if plan.NoOp {
		if isJSONMode() {
			return printValue("", map[string]any{"ok": true, "changed": false, "sha256": plan.LiveSHA})
		}
		_, err := fmt.Fprintln(os.Stdout, "no-op: the live prompt is already identical to "+source+"; nothing sent")
		return err
	}
	// 4. show the diff (stderr in JSON mode so stdout stays parseable).
	var dw io.Writer = os.Stdout
	if isJSONMode() {
		dw = os.Stderr
	}
	// The diff must be shown before anything is sent: a failed write aborts the push.
	newSHA := store.SHA256(plan.New)
	if _, err := fmt.Fprintf(dw, "%s\nlive %s -> new %s (%d/%d characters)\n", plan.Diff, plan.LiveSHA[:12], newSHA[:12],
		utf8.RuneCountInString(plan.New), live.MasterPromptMaxLength); err != nil {
		return fmt.Errorf("cannot show the diff, prompt not changed: %w", err)
	}

	// 5. confirm gate.
	if handled, err := gateWrite(fmt.Sprintf("replace the live master prompt of chat %s (%s)", e.ChatID, action)); handled {
		return err
	}
	// 6. local backup BEFORE the PATCH.
	st, err := e.Store()
	if err != nil {
		return fmt.Errorf("open store for backup (nothing sent): %w", err)
	}
	backup, err := st.BackupPrompt(live.MasterPrompt)
	if err != nil {
		return fmt.Errorf("could not back up the live prompt (nothing sent): %w", err)
	}
	progressf("backup of the live prompt: %s", backup)

	// 7. single PATCH, never retried.
	_, err = e.Client.UpdatePrompt(ctx(), e.ChatID, plan.New)
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status > 0 && ae.Status < 500 {
			// definite rejection (403 read-only role, 422, ...): nothing changed.
			fmt.Fprintf(os.Stderr, "the server rejected the write; the live prompt was not changed (backup kept: %s)\n", backup)
			return err
		}
		return &ExitError{Code: 1, Msg: fmt.Sprintf(
			"the PATCH failed without a definite answer (%v): the write MAY HAVE HAPPENED. It was sent exactly once and NOT retried.\n"+
				"backup of the previous live prompt: %s\n"+
				"verify with: zcx prompt diff %s   (or: zcx prompt get --json | sha256 is %s if applied)\n"+
				"restore the old text with: zcx prompt restore %s --base-sha <current sha>",
			err, backup, source, newSHA, backup)}
	}
	// 8. verify.
	after, err := e.Client.GetPrompt(ctx(), e.ChatID)
	if err != nil {
		return &ExitError{Code: 1, Msg: fmt.Sprintf("the PATCH was accepted but the verification read failed: %v\nbackup: %s\nverify with: zcx prompt diff %s", err, backup, source)}
	}
	if promptops.Normalize(after.MasterPrompt) != plan.New {
		return &ExitError{Code: 1, Msg: fmt.Sprintf(
			"VERIFICATION FAILED: the stored prompt (sha256 %s) differs from the one sent (sha256 %s).\nbackup of the previous prompt: %s\nrestore with: zcx prompt restore %s --base-sha %s",
			store.SHA256(after.MasterPrompt), newSHA, backup, backup, store.SHA256(after.MasterPrompt))}
	}
	if isJSONMode() {
		return printValue("", map[string]any{"ok": true, "changed": true, "old_sha256": plan.LiveSHA,
			"new_sha256": store.SHA256(after.MasterPrompt), "backup": backup, "verified_at": time.Now().UTC()})
	}
	// The write already succeeded and was verified: a closed stdout must not
	// turn it into a reported failure.
	_, _ = fmt.Fprintf(os.Stdout, "ok: prompt updated and verified\nnew sha256 %s\nbackup     %s\n", store.SHA256(after.MasterPrompt), backup)
	return nil
}
