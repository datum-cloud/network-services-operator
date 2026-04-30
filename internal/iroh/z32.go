package iroh

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
)

const endpointIDByteLen = 32

// z32Encoding is iroh's z-base-32: RFC 4648 base32 bit-packing with the
// Zooko alphabet and no padding. iroh uses this exclusively for the
// "_iroh.<z32>.<origin>" DNS discovery name.
var z32Encoding = base32.NewEncoding("ybndrfg8ejkmcpqxot1uwisza345h769").WithPadding(base32.NoPadding)

// EndpointHexToZ32 converts the hex form of an iroh EndpointId — as
// written to Connector.Status.ConnectionDetails.PublicKey.Id — into
// the z-base-32 form iroh's DNS resolver uses to build its
// "_iroh.<z32>.<origin>" lookup names.
//
// The Connector schema carries the hex form because that's iroh's
// Display output and what iroh-base's FromStr round-trips. iroh's DNS
// layer, in contrast, always encodes the same 32 raw bytes as
// z-base-32. This function is that boundary.
func EndpointHexToZ32(hexID string) (string, error) {
	b, err := hex.DecodeString(hexID)
	if err != nil {
		return "", fmt.Errorf("decode endpoint id hex: %w", err)
	}
	if len(b) != endpointIDByteLen {
		return "", fmt.Errorf("endpoint id must be %d bytes, got %d", endpointIDByteLen, len(b))
	}
	return z32Encoding.EncodeToString(b), nil
}
