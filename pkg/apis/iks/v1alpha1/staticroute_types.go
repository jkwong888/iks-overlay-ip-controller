package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)


// StaticRouteSpec defines the desired state of StaticRoute
// +k8s:openapi-gen=true
type StaticRouteSpec struct {
	Subnet string `json:"subnet"`

	// Gateway the gateway the subnet is routed through (optional, discovered if not set)
	Gateway string `json:"gateway,omitempty"`
}

type StaticRouteNodeStatus struct {
	Hostname string `json:"hostname"`
	Gateway string `json:"gateway"`
	Device string `json:"device"`
}

// StaticRouteStatus defines the observed state of StaticRoute
// +k8s:openapi-gen=true
type StaticRouteStatus struct {
	NodeStatus []StaticRouteNodeStatus `json:"nodeStatus"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object 
// StaticRoute is the Schema for the staticroutes API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type StaticRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StaticRouteSpec   `json:"spec,omitempty"`
	Status StaticRouteStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// StaticRouteList contains a list of StaticRoute
type StaticRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StaticRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StaticRoute{}, &StaticRouteList{})
}
