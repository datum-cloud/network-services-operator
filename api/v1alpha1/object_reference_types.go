package v1alpha1

type LocalConnectorReference struct {
	// Name of the referenced Connector.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}
