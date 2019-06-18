package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeOverlayIpSpec defines the desired state of NodeOverlayIp
// +k8s:openapi-gen=true
type NodeOverlayIpSpec struct {
}

// NodeOverlayIpStatus defines the observed state of NodeOverlayIp
// +k8s:openapi-gen=true
type NodeOverlayIpStatus struct {
	// IpAddr reserved in IPAM to configure on the node
	IpAddr string `json:"ipAddr,omitempty"`

	// Gateway the gateway IP address of the network (optional)
	Gateway string `json:"gateway,omitempty"`

	// Interface the interface to put the IP address on
	Interface string `json:"interface,omitempty"`

	// InterfaceLabel the name of the overlay interface
	InterfaceLabel string `json:"interfaceLabel,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeOverlayIp is the Schema for the nodeoverlayips API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type NodeOverlayIp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeOverlayIpSpec   `json:"spec,omitempty"`
	Status NodeOverlayIpStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NodeOverlayIpList contains a list of NodeOverlayIp
type NodeOverlayIpList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeOverlayIp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeOverlayIp{}, &NodeOverlayIpList{})
}
