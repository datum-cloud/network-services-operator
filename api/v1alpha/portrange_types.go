// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

// +kubebuilder:validation:XValidation:rule="self.start <= self.end",message="port range start must not exceed end"
type PortRange struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Start int32 `json:"start"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	End int32 `json:"end"`
}

func PortInterval(port int32, interval *PortRange) PortRange {
	if interval != nil {
		return *interval
	}
	return PortRange{Start: port, End: port}
}

func (p PortRange) Valid() bool { return p.Start >= 1 && p.Start <= p.End && p.End <= 65535 }
func (p PortRange) Contains(other PortRange) bool {
	return p.Start <= other.Start && p.End >= other.End
}
func (p PortRange) Overlaps(other PortRange) bool {
	return p.Start <= other.End && other.Start <= p.End
}
func (p PortRange) Width() int32    { return p.End - p.Start + 1 }
func (p PortRange) Singleton() bool { return p.Start == p.End }
