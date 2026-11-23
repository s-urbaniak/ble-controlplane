// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:onlyVerbs=get,list,watch
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BLEDevice describes a Bluetooth Low Energy advertiser recently heard by this server.
type BLEDevice struct {
	// +k8s:opaqueType
	metav1.TypeMeta `json:",inline"`
	// Metadata identifies the observation for the lifetime of this server process.
	// +optional
	// +k8s:opaqueType
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Status contains the latest published observation.
	// +k8s:opaqueType
	Status BLEDeviceStatus `json:"status"`
}

// BLEDeviceStatus contains metadata reported by the host scanner.
type BLEDeviceStatus struct {
	// LocalName is the Bluetooth name reported by the scanner.
	// +optional
	LocalName string `json:"localName,omitempty"`
	// RSSIDbm is the received signal strength in dBm.
	// +optional
	RSSIDbm *int32 `json:"rssiDbm,omitempty"`
	// TxPowerDbm is the transmit power reported by the advertiser in dBm.
	// +optional
	TxPowerDbm *int32 `json:"txPowerDbm,omitempty"`
	// Connectable records whether the scanner reported that the advertiser accepts connections.
	// +optional
	Connectable *bool `json:"connectable,omitempty"`
	// AdvertisedServiceUUIDs contains service UUIDs reported by the scanner.
	// +optional
	// +listType=set
	AdvertisedServiceUUIDs []string `json:"advertisedServiceUUIDs,omitempty"`
	// ManufacturerCompanyIDs contains Bluetooth SIG company identifiers from manufacturer data.
	// +optional
	// +listType=set
	ManufacturerCompanyIDs []int32 `json:"manufacturerCompanyIDs,omitempty"`
	// FirstSeen is when the first sighting of this object was recorded.
	// +k8s:opaqueType
	FirstSeen metav1.Time `json:"firstSeen"`
	// LastSeen is the time of the latest published sighting.
	// +k8s:opaqueType
	LastSeen metav1.Time `json:"lastSeen"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BLEDeviceList contains a snapshot of the server's recent observations.
type BLEDeviceList struct {
	// +k8s:opaqueType
	metav1.TypeMeta `json:",inline"`
	// Metadata includes the resource version of the snapshot.
	// +optional
	// +k8s:opaqueType
	metav1.ListMeta `json:"metadata,omitempty"`
	// Items contains the matching observations.
	Items []BLEDevice `json:"items"`
}
