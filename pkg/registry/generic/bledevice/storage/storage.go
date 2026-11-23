// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/meta/table"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	"example.com/ble-controlplane/internal/ble"
	api "example.com/ble-controlplane/pkg/apis/generic"
)

type REST struct {
	index *ble.Index
}

var (
	_ rest.Getter               = (*REST)(nil)
	_ rest.Lister               = (*REST)(nil)
	_ rest.Watcher              = (*REST)(nil)
	_ rest.Scoper               = (*REST)(nil)
	_ rest.SingularNameProvider = (*REST)(nil)
)

func NewREST(index *ble.Index) *REST { return &REST{index: index} }

func (r *REST) New() runtime.Object     { return &api.BLEDevice{} }
func (r *REST) NewList() runtime.Object { return &api.BLEDeviceList{} }
func (r *REST) NamespaceScoped() bool   { return true }
func (r *REST) GetSingularName() string { return "bledevice" }
func (r *REST) Destroy()                {}

func (r *REST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	if err := checkNamespace(ctx); err != nil {
		return nil, err
	}
	if options == nil {
		options = &metav1.GetOptions{}
	}
	return r.index.Get(name, options.ResourceVersion)
}

func (r *REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	if err := checkNamespace(ctx); err != nil {
		return nil, err
	}
	if options == nil {
		options = &metainternalversion.ListOptions{}
	}
	return r.index.List(*options)
}

func (r *REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	if err := checkNamespace(ctx); err != nil {
		return nil, err
	}
	if options == nil {
		options = &metainternalversion.ListOptions{}
	}
	return r.index.Watch(ctx, *options)
}

func checkNamespace(ctx context.Context) error {
	namespace := request.NamespaceValue(ctx)
	if namespace == api.LocalNamespace || namespace == metav1.NamespaceAll {
		return nil
	}
	return apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, namespace)
}

func (r *REST) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	m, err := meta.CommonAccessor(object)
	if err != nil {
		return nil, err
	}
	rows, err := table.MetaToTableRow(object, func(object runtime.Object, _ metav1.Object, name, age string) ([]any, error) {
		device := object.(*api.BLEDevice)
		var rssi any
		if device.Status.RSSIDbm != nil {
			rssi = int64(*device.Status.RSSIDbm)
		}
		return []any{name, device.Status.LocalName, rssi, device.Status.LastSeen.UTC().Format(time.RFC3339), age}, nil
	})
	if err != nil {
		return nil, err
	}
	return &metav1.Table{
		ListMeta: metav1.ListMeta{ResourceVersion: m.GetResourceVersion()},
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Format: "name", Description: "Name of the observed device."},
			{Name: "Local Name", Type: "string", Description: "Bluetooth name reported by the scanner."},
			{Name: "RSSI (dBm)", Type: "integer", Description: "Received signal strength in dBm."},
			{Name: "Last Seen", Type: "date", Description: "Time of the last published sighting."},
			{Name: "Age", Type: "string", Description: "Time since the first sighting."},
		},
		Rows: rows,
	}, nil
}
