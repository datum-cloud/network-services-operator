package iroh

import (
	"encoding/hex"
	"fmt"

	"github.com/tv42/zbase32"
)

const endpointIDByteLen = 32

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
	return zbase32.EncodeToString(b), nil
}
