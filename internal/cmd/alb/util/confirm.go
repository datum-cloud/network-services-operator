// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func NonInteractive(in io.Reader) bool {
	if _, ci := os.LookupEnv("CI"); ci {
		return true
	}
	f, isFile := in.(*os.File)
	if !isFile {
		return false
	}
	return !term.IsTerminal(int(f.Fd()))
}

func AssumeYes(cmd *cobra.Command) bool {
	yes, _ := cmd.Root().PersistentFlags().GetBool("yes")
	return yes
}

func ConfirmYesNo(in io.Reader, out io.Writer, prompt string, defaultYes bool) (bool, error) {
	if NonInteractive(in) {
		return true, nil
	}

	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	if _, err := fmt.Fprintf(out, "%s %s: ", prompt, suffix); err != nil {
		return false, fmt.Errorf("writing prompt: %w", err)
	}

	line, err := readLine(in)
	if err != nil {
		return false, err
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return defaultYes, nil
	}
	return isAffirmative(answer), nil
}

func readLine(in io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			if err == io.EOF {
				return b.String(), nil
			}
			return b.String(), fmt.Errorf("reading confirmation: %w", err)
		}
	}
}

func isAffirmative(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func ConfirmTyped(in io.Reader, out io.Writer, prompt, want string) (bool, error) {
	if NonInteractive(in) {
		return false, NewCLIError(ExitAborted,
			"refusing to perform a destructive action non-interactively without confirmation").
			WithFix(fmt.Sprintf("re-run with --yes to confirm %q.", want))
	}

	if prompt != "" {
		if _, err := fmt.Fprintln(out, prompt); err != nil {
			return false, fmt.Errorf("writing prompt: %w", err)
		}
	}
	if _, err := fmt.Fprintf(out, "Type %q to confirm: ", want); err != nil {
		return false, fmt.Errorf("writing prompt: %w", err)
	}

	line, err := readLine(in)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(line) != want {
		return false, NewCLIError(ExitAborted, "confirmation did not match; aborted")
	}
	return true, nil
}
