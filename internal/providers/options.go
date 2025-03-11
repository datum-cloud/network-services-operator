package providers

const (
	// ProviderSingle behaves as a normal controller-runtime manager
	ProviderSingle = "single"

	// ProviderDatum discovers clusters by watching Project resources
	ProviderDatum = "datum"

	// ProviderKind discovers clusters registered via kind
	ProviderKind = "kind"
)

// AllowedProviders are the supported multicluster-runtime Provider implementations.
var AllowedProviders = []string{
	ProviderSingle,
	ProviderDatum,
	ProviderKind,
}
