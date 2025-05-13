package resourcename

import (
	"strings"
	"testing"
)

func TestOptionalTruncateAndHash(t *testing.T) {
	tests := []struct {
		name  string
		fn    func(string) string
		input string
		want  string
	}{
		{
			name:  "DNS1123 no truncation",
			fn:    GetValidDNS1123Name,
			input: "short",
			want:  "short",
		},
		{
			name:  "DNS1123 truncation",
			fn:    GetValidDNS1123Name,
			input: "long" + strings.Repeat("a", 253),
			want:  "long" + strings.Repeat("a", 253-33-4) + "-ac7d16f7520c0ebcf3269a2d2dbda4da",
		},
		{
			name:  "DNS1035 no truncation",
			fn:    GetValidDNS1035Name,
			input: "short",
			want:  "short",
		},
		{
			name:  "DNS1035 truncation",
			fn:    GetValidDNS1035Name,
			input: "long" + strings.Repeat("a", 63),
			want:  "long" + strings.Repeat("a", 63-33-4) + "-95b923195bff3e889498eca200310834",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(tt.input); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
