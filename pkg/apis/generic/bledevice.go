// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package generic

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BLEDevice struct {
	metav1.TypeMeta
	metav1.ObjectMeta
	Status BLEDeviceStatus
}

type BLEDeviceStatus struct {
	LocalName              string
	RSSIDbm                *int32
	TxPowerDbm             *int32
	Connectable            *bool
	AdvertisedServiceUUIDs []string
	ManufacturerCompanyIDs []int32
	FirstSeen              metav1.Time
	LastSeen               metav1.Time
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BLEDeviceList struct {
	metav1.TypeMeta
	metav1.ListMeta
	Items []BLEDevice
}
