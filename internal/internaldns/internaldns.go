// SPDX-License-Identifier: AGPL-3.0-only

package internaldns

const (
	ContextNameAnnotation        = "networking.datumapis.com/internal-dns-context-name"
	AccessNameAnnotation         = "networking.datumapis.com/internal-dns-access-name"
	ServiceDestinationAnnotation = "networking.datumapis.com/internal-dns-service-destination"
	RegionAnnotation             = "networking.datumapis.com/internal-dns-region"

	ConsumerVPCLabel = "networking.datumapis.com/internal-dns-vpc-uid"
)

func IntentComplete(annotations map[string]string) bool {
	return annotations[ContextNameAnnotation] != "" &&
		annotations[AccessNameAnnotation] != "" &&
		annotations[ServiceDestinationAnnotation] != "" &&
		annotations[RegionAnnotation] != ""
}
