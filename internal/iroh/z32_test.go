package iroh

import (
	"strings"
	"testing"
)

// TestZ32EncodingMatchesIrohUpstream cross-checks that our stdlib-based
// z-base-32 encoder agrees byte-for-byte with iroh's Rust z32 crate
// (z32-1.3.0/src/lib.rs) — same alphabet, bit-packing, and trailing
// padding behavior. Every vector here is lifted from that crate's own
// TEST_DATA and public_key tests; if they all pass in Go, the two
// implementations are interchangeable.
func TestZ32EncodingMatchesIrohUpstream(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
		want  string
	}{
		{"empty", []byte{}, ""},
		{"single zero", []byte{0}, "yy"},
		{"single 248", []byte{248}, "9y"},
		{"two bytes", []byte{100, 22}, "comy"},
		{"single 7", []byte{7}, "yh"},
		{"three bytes a", []byte{240, 191, 199}, "6n9hq"},
		{"three bytes b", []byte{212, 122, 4}, "4t7ye"},
		{
			"ten bytes",
			[]byte{4, 17, 130, 50, 156, 17, 148, 233, 91, 94},
			"yoearcwhngkq1s46",
		},
		{
			"alphabet round-trip",
			[]byte{
				0, 68, 50, 20, 199, 66, 84, 182, 53, 207,
				132, 101, 58, 86, 215, 198, 117, 190, 119, 223,
			},
			"ybndrfg8ejkmcpqxot1uwisza345h769",
		},
		{
			// 32-byte public-key test vector from z32-1.3.0/src/lib.rs.
			"32-byte public key",
			[]byte{
				241, 32, 213, 46, 66, 191, 206, 231, 80, 80, 139, 175, 40, 144,
				10, 202, 200, 90, 211, 243, 151, 171, 75, 182, 83, 179, 43, 229,
				5, 195, 45, 57,
			},
			"6ropkm1nz98qqwnotqz1tryk3mrfiw9u16iwzp1usci6kbqdfwho",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := z32Encoding.EncodeToString(tt.bytes); got != tt.want {
				t.Fatalf("z32Encoding diverged from iroh upstream: got %q, want %q", got, tt.want)
			}
		})
	}
}

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
