// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

// Package install registers the Generic API's internal and external representations.
package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"example.com/ble-controlplane/pkg/apis/generic"
	"example.com/ble-controlplane/pkg/apis/generic/v1alpha1"
)

// Install registers the Generic API group and its preferred external version.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(generic.Install(scheme))
	utilruntime.Must(v1alpha1.Install(scheme))
	utilruntime.Must(scheme.SetVersionPriority(schema.GroupVersion(v1alpha1.GroupVersion)))
}
