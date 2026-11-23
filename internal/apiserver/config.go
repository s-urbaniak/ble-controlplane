// SPDX-License-Identifier: Apache-2.0

// Package apiserver configures Kubernetes API serving and installs the BLE API.
package apiserver

import (
	"fmt"
	"maps"
	"net"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	authx509 "k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	serverstorage "k8s.io/apiserver/pkg/server/storage"
	"k8s.io/component-base/compatibility"
	openapicommon "k8s.io/kube-openapi/pkg/common"

	"example.com/ble-controlplane/internal/ble"
	"example.com/ble-controlplane/internal/credentials"
	api "example.com/ble-controlplane/pkg/apis/generic"
	"example.com/ble-controlplane/pkg/apis/generic/install"
	"example.com/ble-controlplane/pkg/apis/generic/v1alpha1"
	generated "example.com/ble-controlplane/pkg/generated/openapi"
	namespaceopenapi "example.com/ble-controlplane/pkg/generated/openapi/namespace"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	install.Install(scheme)
	// Generic REST request options and Status use the unqualified v1 scheme.
	metav1.AddToGroupVersion(scheme, corev1.SchemeGroupVersion)
	// The fixed namespace uses the core types for storage and on the wire.
	namespaces := []runtime.Object{&corev1.Namespace{}, &corev1.NamespaceList{}}
	scheme.AddKnownTypes(corev1.SchemeGroupVersion, namespaces...)
	scheme.AddKnownTypes(schema.GroupVersion{Version: runtime.APIVersionInternal}, namespaces...)
	utilruntime.Must(scheme.SetVersionPriority(corev1.SchemeGroupVersion))
	utilruntime.Must(scheme.AddFieldLabelConversionFunc(corev1.SchemeGroupVersion.WithKind("Namespace"), func(label, value string) (string, string, error) {
		switch label {
		case "metadata.name", "status.phase":
			return label, value, nil
		default:
			return "", "", fmt.Errorf("field label not supported: %s", label)
		}
	}))
}

// Config contains the generic server config and the source of BLE observations.
type Config struct {
	generic *server.Config
	index   *ble.Index
}

func NewConfig(listener net.Listener, pki credentials.Files, index *ble.Index) (*Config, error) {
	c := server.NewConfig(codecs)
	c.EffectiveVersion = compatibility.NewEffectiveVersionFromString("1.37.1", "", "")
	c.EnableProfiling = false
	c.ShutdownWatchTerminationGracePeriod = 2 * time.Second
	c.MergedResourceConfig = serverstorage.NewResourceConfig()
	c.MergedResourceConfig.EnableVersions(bleResource.GroupVersion(), namespaceResource.GroupVersion())
	namer := openapi.NewDefinitionNamer(scheme)
	c.OpenAPIConfig = server.DefaultOpenAPIConfig(openAPIDefinitions, namer)
	c.OpenAPIV3Config = server.DefaultOpenAPIV3Config(openAPIDefinitions, namer)

	secure := options.NewSecureServingOptions().WithLoopback()
	secure.Listener = listener
	secure.ServerCert.CertKey = options.CertKey{CertFile: pki.ServerCert, KeyFile: pki.ServerKey}
	if err := secure.ApplyToConfig(c); err != nil {
		return nil, err
	}
	c.ExternalAddress = listener.Addr().String()
	clientCert := options.ClientCertAuthenticationOptions{ClientCA: pki.CA}
	ca, err := clientCert.GetClientCAContentProvider()
	if err != nil {
		return nil, err
	}
	if err := c.Authentication.ApplyClientCert(ca, c.SecureServing); err != nil {
		return nil, err
	}
	c.Authentication.Authenticator = authx509.NewDynamic(ca.VerifyOptions, authx509.CommonNameUserConversion)
	c.Authorization.Authorizer = localAuthorizer()
	return &Config{generic: c, index: index}, nil
}

type completedConfig struct {
	generic server.CompletedConfig
	index   *ble.Index
}

type CompletedConfig struct{ *completedConfig }

func (c *Config) Complete() CompletedConfig {
	return CompletedConfig{&completedConfig{generic: c.generic.Complete(nil), index: c.index}}
}

var bleResource = schema.GroupVersion(v1alpha1.GroupVersion).WithResource(api.ResourceName)

var namespaceResource = corev1.SchemeGroupVersion.WithResource("namespaces")

func openAPIDefinitions(ref openapicommon.ReferenceCallback) map[string]openapicommon.OpenAPIDefinition {
	definitions := generated.GetOpenAPIDefinitions(ref)
	maps.Copy(definitions, namespaceopenapi.GetOpenAPIDefinitions(ref))
	return definitions
}
