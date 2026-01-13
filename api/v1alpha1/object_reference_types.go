package v1alpha1

type LocalConnectorReference struct {
	// Name of the referenced Connector.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}
