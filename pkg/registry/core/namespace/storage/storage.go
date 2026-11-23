// SPDX-License-Identifier: Apache-2.0

// Package storage serves the control plane's fixed local namespace.
package storage

import (
	"context"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/meta/table"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/registry/rest"

	api "example.com/ble-controlplane/pkg/apis/generic"
)

type REST struct {
	local *corev1.Namespace
}

var (
	_ rest.Getter             = (*REST)(nil)
	_ rest.Lister             = (*REST)(nil)
	_ rest.ShortNamesProvider = (*REST)(nil)
)

func NewREST() *REST {
	created := metav1.Now()
	return &REST{local: &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:              api.LocalNamespace,
			UID:               uuid.NewUUID(),
			CreationTimestamp: created,
			ResourceVersion:   strconv.FormatInt(created.UnixNano(), 10),
			Labels:            map[string]string{corev1.LabelMetadataName: api.LocalNamespace},
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}}
}

func (*REST) New() runtime.Object     { return &corev1.Namespace{} }
func (*REST) NewList() runtime.Object { return &corev1.NamespaceList{} }
func (*REST) NamespaceScoped() bool   { return false }
func (*REST) GetSingularName() string { return "namespace" }
func (*REST) ShortNames() []string    { return []string{"ns"} }
func (*REST) Destroy()                {}

func (r *REST) checkVersion(version string) error {
	if version != "" && version != "0" && version != r.local.ResourceVersion {
		return apierrors.NewResourceExpired("namespace resource version expired; list again")
	}
	return nil
}

func (r *REST) Get(_ context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	if options != nil {
		if err := r.checkVersion(options.ResourceVersion); err != nil {
			return nil, err
		}
	}
	if name != r.local.Name {
		return nil, apierrors.NewNotFound(corev1.Resource("namespaces"), name)
	}
	return r.local.DeepCopy(), nil
}

func (r *REST) List(_ context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	list := &corev1.NamespaceList{
		ListMeta: metav1.ListMeta{ResourceVersion: r.local.ResourceVersion},
		Items:    []corev1.Namespace{},
	}
	if options != nil {
		if err := r.checkVersion(options.ResourceVersion); err != nil {
			return nil, err
		}
		if options.Continue != "" {
			return nil, apierrors.NewBadRequest("namespace lists do not use continuation tokens")
		}
		if options.LabelSelector != nil && !options.LabelSelector.Matches(labels.Set(r.local.Labels)) {
			return list, nil
		}
		selectable := fields.Set{"metadata.name": r.local.Name, "status.phase": string(r.local.Status.Phase)}
		if options.FieldSelector != nil && !options.FieldSelector.Matches(selectable) {
			return list, nil
		}
	}
	list.Items = append(list.Items, *r.local.DeepCopy())
	return list, nil
}

func (*REST) ConvertToTable(_ context.Context, object runtime.Object, _ runtime.Object) (*metav1.Table, error) {
	metadata, err := meta.CommonAccessor(object)
	if err != nil {
		return nil, err
	}
	rows, err := table.MetaToTableRow(object, func(object runtime.Object, _ metav1.Object, name, age string) ([]any, error) {
		return []any{name, string(object.(*corev1.Namespace).Status.Phase), age}, nil
	})
	if err != nil {
		return nil, err
	}
	return &metav1.Table{
		ListMeta: metav1.ListMeta{ResourceVersion: metadata.GetResourceVersion()},
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Format: "name"},
			{Name: "Status", Type: "string"},
			{Name: "Age", Type: "string"},
		},
		Rows: rows,
	}, nil
}
