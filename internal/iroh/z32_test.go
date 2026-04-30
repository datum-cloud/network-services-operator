package iroh

import (
	"strings"
	"testing"
)

func TestEndpointHexToZ32(t *testing.T) {
	tests := []struct {
		name        string
		hexID       string
		want        string
		wantErrSubs string
	}{
		{
			// Cross-checked against the public_key test in iroh's z32
			// crate (z32-1.3.0/src/lib.rs): the 32-byte public key
			// [241, 32, 213, 46, ...] encodes to
			// "6ropkm1nz98qqwnotqz1tryk3mrfiw9u16iwzp1usci6kbqdfwho".
			name:  "iroh z32 crate public_key vector",
			hexID: "f120d52e42bfcee750508baf28900acac85ad3f397ab4bb653b32be505c32d39",
			want:  "6ropkm1nz98qqwnotqz1tryk3mrfiw9u16iwzp1usci6kbqdfwho",
		},
		{
			name:        "invalid hex characters",
			hexID:       "z" + strings.Repeat("0", 63),
			wantErrSubs: "decode endpoint id hex",
		},
		{
			name:        "odd-length hex",
			hexID:       strings.Repeat("a", 63),
			wantErrSubs: "decode endpoint id hex",
		},
		{
			name:        "too few bytes",
			hexID:       strings.Repeat("ab", 16),
			wantErrSubs: "must be 32 bytes",
		},
		{
			name:        "too many bytes",
			hexID:       strings.Repeat("ab", 64),
			wantErrSubs: "must be 32 bytes",
		},
		{
			name:        "empty",
			hexID:       "",
			wantErrSubs: "must be 32 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EndpointHexToZ32(tt.hexID)
			if tt.wantErrSubs != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result %q)", tt.wantErrSubs, got)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubs) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErrSubs, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("EndpointHexToZ32(%q) = %q, want %q", tt.hexID, got, tt.want)
			}
		})
	}
}
