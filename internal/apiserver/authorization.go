// SPDX-License-Identifier: Apache-2.0

package apiserver

import (
	"context"
	"strings"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"

	"example.com/ble-controlplane/internal/credentials"
	api "example.com/ble-controlplane/pkg/apis/generic"
)

func localAuthorizer() authorizer.Authorizer {
	return authorizer.AuthorizerFunc(func(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
		identity := a.GetUser()
		if identity == nil {
			return authorizer.DecisionNoOpinion, "an authenticated identity is required", nil
		}
		name := identity.GetName()
		if a.IsResourceRequest() && a.GetAPIGroup() == bleResource.Group && a.GetResource() == bleResource.Resource && a.GetSubresource() == "" {
			switch name {
			case credentials.ClientUser:
				if a.GetVerb() == "get" || a.GetVerb() == "list" || a.GetVerb() == "watch" {
					return authorizer.DecisionAllow, "", nil
				}
			case user.APIServerUser:
				if a.GetNamespace() == api.LocalNamespace && (a.GetVerb() == "list" || a.GetVerb() == "watch") {
					return authorizer.DecisionAllow, "", nil
				}
			}
		}
		if name == credentials.ClientUser && a.IsResourceRequest() &&
			a.GetAPIGroup() == namespaceResource.Group && a.GetResource() == namespaceResource.Resource && a.GetSubresource() == "" &&
			(a.GetVerb() == "get" || a.GetVerb() == "list") {
			return authorizer.DecisionAllow, "", nil
		}
		if name == credentials.ClientUser && !a.IsResourceRequest() && a.GetVerb() == "get" {
			switch a.GetPath() {
			case "/api", "/api/v1", "/api/v1/", "/apis", "/version", "/healthz", "/livez", "/readyz", "/openapi/v2", "/openapi/v3":
				return authorizer.DecisionAllow, "", nil
			}
			for _, prefix := range []string{"/apis/", "/openapi/v3/", "/healthz/", "/livez/", "/readyz/"} {
				if strings.HasPrefix(a.GetPath(), prefix) {
					return authorizer.DecisionAllow, "", nil
				}
			}
		}
		return authorizer.DecisionNoOpinion, "request is outside the local BLE reader permissions", nil
	})
}
