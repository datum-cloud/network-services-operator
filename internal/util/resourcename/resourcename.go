package resourcename

import (
	"crypto/md5"
	"fmt"

	validation "k8s.io/apimachinery/pkg/util/validation"
)

// GetValidDNS1123Name returns a name compliant with Kubernetes DNS-1123
// subdomain length (253).
// It truncates and appends an MD5 hash if the input name is too long.
func GetValidDNS1123Name(name string) string {
	return optionalTruncateAndHash(name, validation.DNS1123SubdomainMaxLength)
}

// GetValidDNS1035Name returns a name compliant with Kubernetes DNS-1035
// label length (63).
// It truncates and appends an MD5 hash if the input name is too long.
func GetValidDNS1035Name(name string) string {
	return optionalTruncateAndHash(name, validation.DNS1035LabelMaxLength)
}

// optionalTruncateAndHash ensures a name fits maxLength. If longer, it
// truncates the name and appends an MD5 hash of the original name. Panics if
// maxLength is too small for the hash suffix (33 chars).
func optionalTruncateAndHash(name string, maxLength int) string {
	if len(name) <= maxLength {
		return name
	}

	hash := md5.Sum([]byte(name))

	prefixLen := maxLength - 33
	if prefixLen <= 0 {
		panic(fmt.Sprintf("maxLength is too small: %d", maxLength))
	}

	if len(name) > prefixLen {
		name = name[0:prefixLen]
	}

	return fmt.Sprintf("%s-%x", name, hash)
}
