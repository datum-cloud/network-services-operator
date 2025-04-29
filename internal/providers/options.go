package providers

type Provider string

const (
	// ProviderSingle behaves as a normal controller-runtime manager
	ProviderSingle Provider = "single"

	// ProviderDatum discovers clusters by watching Project resources
	ProviderDatum Provider = "datum"

	// ProviderKind discovers clusters registered via kind
	ProviderKind Provider = "kind"
)

// AllowedProviders are the supported multicluster-runtime Provider implementations.
var AllowedProviders = []Provider{
	ProviderSingle,
	ProviderDatum,
}
