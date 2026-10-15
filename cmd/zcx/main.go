// Command zcx is the Zipchat Backend API explorer: read-only analytics for a
// Zipchat assistant (conversations, escalations, KPIs, prompt audit) plus a
// heavily guarded prompt push. A fleet sibling of pmx/ddx/stx.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/commands"
	"github.com/nicolasacchi/zcx/internal/output"
)

// version is overwritten at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	commands.SetVersion(version)
	err := commands.Execute()
	if err == nil {
		return
	}
	var exitErr *commands.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.Msg != "" {
			fmt.Fprintln(os.Stderr, "Error:", exitErr.Msg)
		}
		os.Exit(exitErr.Code)
	}
	var coder interface{ ExitCode() int }
	status := 0
	code := 1
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		status = apiErr.Status
		fmt.Fprintln(os.Stderr, "Error:", apiErr.Error())
		if apiErr.Hint != "" && apiErr.Status != 0 {
			fmt.Fprintln(os.Stderr, "Hint:", apiErr.Hint)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	if errors.As(err, &coder) {
		code = coder.ExitCode()
	}
	output.PrintError(err.Error(), status)
	os.Exit(code)
}
