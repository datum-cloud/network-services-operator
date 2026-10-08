// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitForbidden   = 3
	ExitNotFound    = 4
	ExitConflict    = 5
	ExitInvalid     = 6
	ExitUnavailable = 8
	ExitAborted     = 9
)

const nameError = "ALB_ERROR"

var exitCodeNames = map[int]string{
	ExitOK:          "OK",
	ExitError:       nameError,
	ExitUsage:       "ALB_USAGE",
	ExitForbidden:   "ALB_FORBIDDEN",
	ExitNotFound:    "ALB_NOT_FOUND",
	ExitConflict:    "ALB_CONFLICT",
	ExitInvalid:     "ALB_INVALID",
	ExitUnavailable: "ALB_UNAVAILABLE",
	ExitAborted:     "ALB_ABORTED",
}

func ExitCodeName(code int) string {
	if name, known := exitCodeNames[code]; known {
		return name
	}
	return exitCodeNames[ExitError]
}

type CLIError struct {
	code  int
	msg   string
	fix   string
	cause error
}

func NewCLIError(code int, msg string) *CLIError {
	return &CLIError{code: code, msg: msg}
}

func (e *CLIError) WithFix(fix string) *CLIError {
	e.fix = fix
	return e
}

func (e *CLIError) WithCause(err error) *CLIError {
	e.cause = err
	return e
}

func (e *CLIError) Error() string { return e.msg }

func (e *CLIError) Code() int { return e.code }

func (e *CLIError) Fix() string { return e.fix }

func (e *CLIError) Unwrap() error { return e.cause }

func UsageErrorf(format string, a ...any) *CLIError {
	return NewCLIError(ExitUsage, fmt.Sprintf(format, a...))
}

func ClassifyError(err error) *CLIError {
	if err == nil {
		return nil
	}
	var already *CLIError
	if errors.As(err, &already) {
		if outer := err.Error(); outer != already.msg {
			return &CLIError{code: already.code, msg: outer, fix: already.fix, cause: already.cause}
		}
		return already
	}

	switch code := httpStatusCode(err); {
	case code == 401:
		return NewCLIError(ExitForbidden, fmt.Sprintf("not authenticated: %s", apiMessage(err))).
			WithFix("your session has expired — re-run:\n       datumctl login").
			WithCause(err)
	case code == 403:
		return NewCLIError(ExitForbidden, fmt.Sprintf("not authorized: %s", apiMessage(err))).
			WithFix("verify the active org and project, and your access to Application Load Balancers.").
			WithCause(err)
	case code == 404:
		return NewCLIError(ExitNotFound, apiMessage(err)).WithCause(err)
	case code == 409:
		return NewCLIError(ExitConflict, fmt.Sprintf("conflict: %s", apiMessage(err))).WithCause(err)
	case code == 400, code == 422:
		return NewCLIError(ExitInvalid, fmt.Sprintf("invalid request: %s", apiMessage(err))).WithCause(err)
	case code == 429:
		return NewCLIError(ExitUnavailable, fmt.Sprintf("rate limited: %s", apiMessage(err))).
			WithFix("wait a moment and try again.").
			WithCause(err)
	case code >= 500:
		return NewCLIError(ExitUnavailable, fmt.Sprintf("the networking API is unavailable: %s", apiMessage(err))).
			WithFix("this is a server-side failure — retry, and check the Datum status page if it persists.").
			WithCause(err)
	}

	if errors.Is(err, context.Canceled) {
		return NewCLIError(ExitAborted, "cancelled").WithCause(err)
	}
	if isTransportError(err) {
		return NewCLIError(ExitUnavailable, fmt.Sprintf("cannot reach the networking API: %s", err)).
			WithFix("check connectivity and that you are logged in (datumctl login).").
			WithCause(err)
	}
	return NewCLIError(ExitError, err.Error()).WithCause(err)
}

func RenderExit(w io.Writer, err error, verbose bool) int {
	if err == nil {
		return ExitOK
	}

	ce := ClassifyError(err)
	_, _ = fmt.Fprintf(w, "Error: %s\n", ce.msg)
	if ce.fix != "" {
		_, _ = fmt.Fprintf(w, "Fix:   %s\n", ce.fix)
	}
	if verbose && ce.cause != nil && ce.cause.Error() != ce.msg {
		_, _ = fmt.Fprintf(w, "Cause: %v\n", ce.cause)
	}
	_, _ = fmt.Fprintf(w, "exit status %d   # %s\n", ce.code, ExitCodeName(ce.code))
	return ce.code
}

func httpStatusCode(err error) int {
	if status, isStatus := asAPIStatus(err); isStatus {
		return int(status.Status().Code)
	}
	return 0
}

func asAPIStatus(err error) (apierrors.APIStatus, bool) {
	if err == nil {
		return nil, false
	}
	var s apierrors.APIStatus
	if errors.As(err, &s) {
		return s, true
	}
	return nil, false
}

func apiMessage(err error) string {
	if status, isStatus := asAPIStatus(err); isStatus {
		if m := status.Status().Message; m != "" {
			return m
		}
	}
	return err.Error()
}

func isTransportError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return true
	}
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}
