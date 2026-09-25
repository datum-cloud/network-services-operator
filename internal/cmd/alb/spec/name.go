// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"crypto/rand"
	"strings"
	"unicode"

	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

const (
	generatedNameMaxLength    = 30
	generatedNameRandomLength = 6
)

var randomNameSuffix = randomAlphanum

func NameFromDisplayName(displayName string) (string, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return "", util.UsageErrorf("display name is required when no name is given").
			WithFix("pass a name, or --display-name TEXT")
	}
	if len(displayName) > MaxDisplayNameLength {
		return "", util.UsageErrorf("display name must be %d characters or fewer", MaxDisplayNameLength)
	}

	base := toKebabCase(displayName)
	if base == "" {
		return "", util.UsageErrorf("display name %q has no letters or digits to build a name from", displayName).
			WithFix("pass an explicit name, or use a display name with letters or digits")
	}

	suffix, err := randomNameSuffix(generatedNameRandomLength)
	if err != nil {
		return "", util.NewCLIError(util.ExitError, "could not generate a name").WithCause(err)
	}

	maxBase := generatedNameMaxLength - generatedNameRandomLength - 1
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}
	if base == "" {
		return "", util.UsageErrorf("display name %q is too short after sanitizing", displayName).
			WithFix("pass an explicit name")
	}

	name := base + "-" + suffix
	if !isDNSLabel(name) {
		return "", util.UsageErrorf("could not build a valid name from display name %q", displayName).
			WithFix("pass an explicit name")
	}
	return name, nil
}

func toKebabCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := true
	prevLower := false
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			prevHyphen = false
			prevLower = false
		case unicode.IsLower(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevHyphen = false
			prevLower = unicode.IsLower(r)
		case r == ' ' || r == '_' || r == '.' || r == '-':
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
			prevLower = false
		default:
			prevLower = false
		}
	}
	return strings.Trim(b.String(), "-")
}

func isDNSLabel(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func randomAlphanum(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}
