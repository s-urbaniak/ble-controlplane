// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0
package generic

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName identifies the Generic API group.
const GroupName = "generic.example.com"

// LocalNamespace contains observations from the host scanner.
const LocalNamespace = "local"

// ResourceName is the plural name used for discovery and routing.
const ResourceName = "bledevices"

var (
	// GroupVersion identifies the server's internal API representation.
	GroupVersion = schema.GroupVersion{Group: GroupName, Version: runtime.APIVersionInternal}
	// SchemeBuilder collects the registration functions for the internal API.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// Install registers the internal API types in a scheme.
	Install = SchemeBuilder.AddToScheme
)

// Resource qualifies a resource name with the Generic API group.
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &BLEDevice{}, &BLEDeviceList{})
	return nil
}
