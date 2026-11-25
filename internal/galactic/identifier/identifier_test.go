/*
Copyright © 2026 Datum Technology, Inc. All rights reserved.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package identifier

import "testing"

func TestHexRejectsReservedValues(t *testing.T) {
	for _, value := range []uint64{0, MaxVPC, MaxVPC + 1} {
		if _, err := Hex(value, MaxVPC); err == nil {
			t.Errorf("Hex(%d): got no error, want one", value)
		}
	}
}

func TestDerivedAttachmentIdentifiersFitTheirInterfaceNameSegment(t *testing.T) {
	for attempt := range 200 {
		attachment, err := VPCAttachmentBase62For("tenant/nic", attempt)
		if err != nil {
			t.Fatalf("VPCAttachmentBase62For: %v", err)
		}
		if len(attachment) > 3 {
			t.Errorf("attachment identifier %q exceeds three base62 characters", attachment)
		}
	}
}

func TestDerivedAttachmentIdentifiersAreStablePerSeed(t *testing.T) {
	first, err := VPCAttachmentBase62For("tenant/nic", 0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := VPCAttachmentBase62For("tenant/nic", 0)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Errorf("same seed and attempt: got %q then %q", first, again)
	}

	distinct := map[string]struct{}{}
	for attempt := range 8 {
		candidate, err := VPCAttachmentBase62For("tenant/nic", attempt)
		if err != nil {
			t.Fatal(err)
		}
		distinct[candidate] = struct{}{}
	}
	if len(distinct) < 7 {
		t.Errorf("attempts collapse onto %d identifiers, want independent candidates", len(distinct))
	}

	other, err := VPCAttachmentBase62For("tenant/other", 0)
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Errorf("different seeds share identifier %q", first)
	}
}

func TestBase62RoundTrip(t *testing.T) {
	hex, err := Random(MaxVPC)
	if err != nil {
		t.Fatalf("Random(MaxVPC): %v", err)
	}
	base62, err := HexToBase62(hex)
	if err != nil {
		t.Fatalf("HexToBase62(%q): %v", hex, err)
	}
	back, err := Base62ToHex(base62)
	if err != nil {
		t.Fatalf("Base62ToHex(%q): %v", base62, err)
	}
	if want := trimLeadingZeros(hex); back != want {
		t.Errorf("round trip: got %q, want %q", back, want)
	}
}

func trimLeadingZeros(value string) string {
	for i := range value {
		if value[i] != '0' {
			return value[i:]
		}
	}
	return "0"
}

func TestVPCBase62RejectsReservedValuesAndFitsItsSegment(t *testing.T) {
	for _, value := range []uint64{0, MaxVPC, MaxVPC + 1} {
		if _, err := VPCBase62(value); err == nil {
			t.Errorf("VPCBase62(%d): got no error, want one", value)
		}
	}

	// The widest fabric identity is 32 bits, well inside the 48 the VPC
	// identifier holds, so nothing an identity carries can overflow the segment.
	for _, value := range []uint64{1, 16, 1 << 31, 1<<32 - 1} {
		encoded, err := VPCBase62(value)
		if err != nil {
			t.Fatalf("VPCBase62(%d): %v", value, err)
		}
		if len(encoded) > 9 {
			t.Errorf("VPCBase62(%d) = %q exceeds nine base62 characters", value, encoded)
		}
	}
}

func TestParseTenantIdentifier(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		wantVPC        string
		wantAttachment string
		wantOK         bool
	}{
		{name: "nine character vpc", id: "2wJqT7d9x-9xK", wantVPC: "2wJqT7d9x", wantAttachment: "9xK", wantOK: true},
		{name: "short halves", id: "a-1", wantVPC: "a", wantAttachment: "1", wantOK: true},
		{name: "vpc longer than nine characters", id: "2wJqT7d9xK-9xK"},
		{name: "no separator", id: "2wJqT7d9x"},
		{name: "empty vpc", id: "-9xK"},
		{name: "empty attachment", id: "2wJqT7d-"},
		{name: "second separator", id: "vpc-a-attachment"},
		{name: "underscore", id: "vpc_a-9xK"},
		{name: "dot in attachment", id: "vpc-9x.K"},
		{name: "space", id: "vpc -9xK"},
		{name: "trailing newline", id: "vpc-9xK\n"},
		{name: "non-ascii letter", id: "vpcé-9xK"},
		{name: "empty", id: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vpc, attachment, ok := ParseTenantIdentifier(tt.id)
			if ok != tt.wantOK || vpc != tt.wantVPC || attachment != tt.wantAttachment {
				t.Errorf("ParseTenantIdentifier(%q) = (%q, %q, %t), want (%q, %q, %t)",
					tt.id, vpc, attachment, ok, tt.wantVPC, tt.wantAttachment, tt.wantOK)
			}
		})
	}
}

func TestParseTenantIdentifierAcceptsGeneratedIdentifiers(t *testing.T) {
	for _, value := range []uint64{1, 61, 62, MaxVPC - 1} {
		vpc, err := VPCBase62(value)
		if err != nil {
			t.Fatalf("VPCBase62(%d): %v", value, err)
		}
		for attempt := range 50 {
			attachment, err := VPCAttachmentBase62For(vpc, attempt)
			if err != nil {
				t.Fatalf("VPCAttachmentBase62For: %v", err)
			}
			id := vpc + "-" + attachment
			gotVPC, gotAttachment, ok := ParseTenantIdentifier(id)
			if !ok || gotVPC != vpc || gotAttachment != attachment {
				t.Errorf("ParseTenantIdentifier(%q) = (%q, %q, %t), want (%q, %q, true)",
					id, gotVPC, gotAttachment, ok, vpc, attachment)
			}
		}
	}
}
