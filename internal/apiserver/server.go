// SPDX-License-Identifier: Apache-2.0

package apiserver

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server"
	serverstorage "k8s.io/apiserver/pkg/server/storage"

	"example.com/ble-controlplane/internal/ble"
	selfreviewstorage "example.com/ble-controlplane/pkg/registry/authorization/selfsubjectaccessreview/storage"
	namespacestorage "example.com/ble-controlplane/pkg/registry/core/namespace/storage"
	blestorage "example.com/ble-controlplane/pkg/registry/generic/bledevice/storage"
)

// New follows the native-resource layer of Kubernetes' generic control plane sample.
func (c CompletedConfig) New() (*server.GenericAPIServer, error) {
	s, err := c.generic.New("ble-controlplane", server.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}
	if c.generic.MergedResourceConfig.ResourceEnabled(namespaceResource) {
		core := server.NewDefaultAPIGroupInfo(namespaceResource.Group, scheme, metav1.ParameterCodec, codecs)
		core.VersionedResourcesStorageMap[namespaceResource.Version] = map[string]rest.Storage{
			namespaceResource.Resource: namespacestorage.NewREST(),
		}
		if err := s.InstallLegacyAPIGroup(server.DefaultLegacyAPIPrefix, &core); err != nil {
			s.Destroy()
			return nil, fmt.Errorf("installing the core namespace API: %w", err)
		}
	}
	if c.generic.MergedResourceConfig.ResourceEnabled(selfReviewResource) {
		group := server.NewDefaultAPIGroupInfo(selfReviewResource.Group, scheme, metav1.ParameterCodec, codecs)
		group.VersionedResourcesStorageMap[selfReviewResource.Version] = map[string]rest.Storage{
			selfReviewResource.Resource: selfreviewstorage.NewREST(c.generic.Authorization.Authorizer),
		}
		if err := s.InstallAPIGroups(&group); err != nil {
			s.Destroy()
			return nil, fmt.Errorf("installing the authorization API group: %w", err)
		}
	}
	provider := storageProvider{index: c.index}
	group := provider.NewRESTStorage(c.generic.MergedResourceConfig)
	if len(group.VersionedResourcesStorageMap) > 0 {
		if err := s.InstallAPIGroups(&group); err != nil {
			s.Destroy()
			return nil, fmt.Errorf("installing the BLE API group: %w", err)
		}
	}
	return s, nil
}

type storageProvider struct{ index *ble.Index }

func (p storageProvider) NewRESTStorage(resources serverstorage.APIResourceConfigSource) server.APIGroupInfo {
	group := server.NewDefaultAPIGroupInfo(bleResource.Group, scheme, metav1.ParameterCodec, codecs)
	if resources.ResourceEnabled(bleResource) {
		group.VersionedResourcesStorageMap[bleResource.Version] = map[string]rest.Storage{
			bleResource.Resource: blestorage.NewREST(p.index),
		}
	}
	return group
}
