// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	api "example.com/ble-controlplane/pkg/apis/generic"
	"example.com/ble-controlplane/pkg/apis/generic/v1alpha1"
	clientset "example.com/ble-controlplane/pkg/generated/clientset/versioned"
)

func TestReplayControlPlane(t *testing.T) {
	worker := os.Getenv("BLE_WORKER_PATH")
	if worker == "" {
		t.Skip("run the test task to build and exercise the official TypeScript client")
	}
	deadline, cancelDeadline := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancelDeadline()
	ctx, cancel := context.WithCancel(deadline)
	dir := t.TempDir()

	probe, err := listenLocal(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	uiPort := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	uiURL := fmt.Sprintf("http://127.0.0.1:%d", uiPort)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{DataDir: dir, Provider: "replay", WorkerFile: worker, UIPort: uiPort})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("control plane stopped with an error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("control plane did not shut down")
		}
	})

	var config *rest.Config
	var client *clientset.Clientset
	var observed *v1alpha1.BLEDeviceList
	err = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		if config == nil {
			var err error
			config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(dir, "kubeconfig"))
			if err != nil {
				return false, nil
			}
			client, err = clientset.NewForConfig(config)
			if err != nil {
				return false, err
			}
		}
		var err error
		observed, err = client.GenericV1alpha1().BLEDevices(api.LocalNamespace).List(ctx, metav1.ListOptions{})
		return err == nil && len(observed.Items) > 0, nil
	})
	if err != nil {
		t.Fatalf("waiting for replayed BLE device: %v", err)
	}
	gv := v1alpha1.GroupVersion.String()
	resourcePath := "/apis/" + gv + "/namespaces/local/bledevices"

	t.Run("discovers BLEDevices and the core namespace", func(t *testing.T) {
		_, resources, err := client.Discovery().ServerGroupsAndResources()
		if err != nil {
			t.Fatal(err)
		}
		if len(resources) != 2 {
			t.Fatalf("unexpected discovery response: %+v", resources)
		}
		for _, group := range resources {
			if len(group.APIResources) != 1 {
				t.Fatalf("unexpected resource count in %s: %+v", group.GroupVersion, group.APIResources)
			}
			resource := group.APIResources[0]
			switch group.GroupVersion {
			case gv:
				if resource.Name != api.ResourceName || !resource.Namespaced ||
					!slices.Equal(resource.Verbs, metav1.Verbs{"get", "list", "watch"}) {
					t.Fatalf("unexpected BLE resource metadata: %+v", resource)
				}
			case "v1":
				if resource.Name != "namespaces" || resource.Namespaced || resource.Kind != "Namespace" ||
					!slices.Equal(resource.ShortNames, []string{"ns"}) ||
					!slices.Equal(resource.Verbs, metav1.Verbs{"get", "list"}) {
					t.Fatalf("unexpected namespace resource metadata: %+v", resource)
				}
			default:
				t.Fatalf("unexpected API group: %s", group.GroupVersion)
			}
		}

		schema, err := client.Discovery().OpenAPIV3().Paths()
		if err != nil {
			t.Fatal(err)
		}
		group := schema["apis/"+gv]
		if group == nil {
			t.Fatalf("OpenAPI is missing the BLE group: %+v", schema)
		}
		document, err := group.Schema("application/json")
		if err != nil || !strings.Contains(string(document), "rssiDbm") {
			t.Fatalf("OpenAPI is missing BLE device status: %v", err)
		}
	})

	t.Run("serves the active local namespace", func(t *testing.T) {
		core, err := coreclient.NewForConfig(config)
		if err != nil {
			t.Fatal(err)
		}
		namespaces := core.Namespaces()
		list, err := namespaces.List(ctx, metav1.ListOptions{
			FieldSelector: "status.phase=Active", LabelSelector: corev1.LabelMetadataName + "=local",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 || list.Items[0].Name != api.LocalNamespace ||
			list.Items[0].Status.Phase != corev1.NamespaceActive || list.ResourceVersion == "" {
			t.Fatalf("unexpected namespace list: %+v", list)
		}
		namespace, err := namespaces.Get(ctx, api.LocalNamespace, metav1.GetOptions{ResourceVersion: list.ResourceVersion})
		if err != nil || namespace.UID != list.Items[0].UID || namespace.Namespace != "" {
			t.Fatalf("get local namespace: %+v, %v", namespace, err)
		}
		_, err = namespaces.Get(ctx, "other", metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("get other namespace: %v", err)
		}
		list, err = namespaces.List(ctx, metav1.ListOptions{FieldSelector: "metadata.name=other"})
		if err != nil || len(list.Items) != 0 {
			t.Fatalf("unexpected filtered namespace list: %+v, %v", list, err)
		}
	})

	devices := client.GenericV1alpha1().BLEDevices(api.LocalNamespace)
	device := observed.Items[0]
	t.Run("reads observations in local", func(t *testing.T) {
		if observed.ResourceVersion == "" || device.Namespace != api.LocalNamespace ||
			device.Status.LocalName != "Demo Beacon (replay)" || device.Status.RSSIDbm == nil {
			t.Fatalf("unexpected device: %+v", device)
		}
		got, err := devices.Get(ctx, device.Name, metav1.GetOptions{})
		if err != nil || got.UID != device.UID {
			t.Fatalf("get device: %+v, %v", got, err)
		}

		all, err := client.GenericV1alpha1().BLEDevices(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil || len(all.Items) != 1 || all.Items[0].Namespace != api.LocalNamespace {
			t.Fatalf("all-namespace list: %+v, %v", all, err)
		}
		_, err = client.GenericV1alpha1().BLEDevices("other").List(ctx, metav1.ListOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("other namespace: %v", err)
		}
	})

	t.Run("requires read-only API credentials", func(t *testing.T) {
		for _, tc := range []struct {
			name, method, path string
			config             *rest.Config
			code               int
		}{
			{"kubectl forbidden to create", http.MethodPost, resourcePath, config, http.StatusForbidden},
			{"kubectl forbidden to create namespaces", http.MethodPost, "/api/v1/namespaces", config, http.StatusForbidden},
			{"API requires credentials", http.MethodGet, resourcePath, rest.AnonymousClientConfig(config), http.StatusUnauthorized},
		} {
			t.Run(tc.name, func(t *testing.T) {
				httpClient, err := rest.HTTPClientFor(tc.config)
				if err != nil {
					t.Fatal(err)
				}
				request, err := http.NewRequestWithContext(ctx, tc.method, config.Host+tc.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := httpClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != tc.code {
					t.Errorf("HTTP status = %d, want %d", response.StatusCode, tc.code)
				}
			})
		}
	})

	t.Run("scopes the dashboard loopback proxy", func(t *testing.T) {
		for _, tc := range []struct {
			name, method, path string
			code               int
		}{
			{"lists local devices", http.MethodGet, resourcePath, http.StatusOK},
			{"rejects writes", http.MethodPost, resourcePath, http.StatusMethodNotAllowed},
			{"rejects head requests", http.MethodHead, resourcePath, http.StatusMethodNotAllowed},
			{"does not expose namespaces", http.MethodGet, "/api/v1/namespaces", http.StatusNotFound},
			{"does not expose individual devices", http.MethodGet, resourcePath + "/" + device.Name, http.StatusNotFound},
			{"does not expose all namespaces", http.MethodGet, "/apis/" + gv + "/bledevices", http.StatusNotFound},
		} {
			t.Run(tc.name, func(t *testing.T) {
				request, err := http.NewRequestWithContext(ctx, tc.method, uiURL+tc.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer invalid")
				request.Header.Set("Impersonate-User", "dashboard-visitor")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != tc.code {
					t.Errorf("HTTP status = %d, want %d", response.StatusCode, tc.code)
				}
			})
		}
	})

	t.Run("watches replay updates and expiry in the API and UI", func(t *testing.T) {
		_, err := devices.Watch(ctx, metav1.ListOptions{ResourceVersion: "expired-scan.1"})
		if !apierrors.IsResourceExpired(err) {
			t.Fatalf("stale resource version: %v", err)
		}
		stream, err := devices.Watch(ctx, metav1.ListOptions{
			ResourceVersion: observed.ResourceVersion, ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			AllowWatchBookmarks: true, SendInitialEvents: ptr.To(true), FieldSelector: "metadata.name=" + device.Name,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Stop()
		for _, want := range []watch.EventType{watch.Added, watch.Bookmark, watch.Modified} {
			if event := nextEvent(t, ctx, stream); event.Type != want {
				t.Fatalf("received %s, want %s: %+v", event.Type, want, event.Object)
			}
		}

		page := get(t, ctx, uiURL+"/")
		html, err := io.ReadAll(page.Body)
		page.Body.Close()
		if err != nil || !strings.Contains(string(html), "Nearby Bluetooth devices") {
			t.Fatalf("embedded page unavailable: %v", err)
		}
		events := get(t, ctx, uiURL+"/events")
		defer events.Body.Close()
		reader := bufio.NewScanner(events.Body)
		first := dashboardSnapshot(t, reader, func(items []v1alpha1.BLEDevice) bool {
			return len(items) == 1 && items[0].UID == device.UID
		})
		dashboardSnapshot(t, reader, func(items []v1alpha1.BLEDevice) bool {
			return len(items) == 1 && first[0].ResourceVersion != items[0].ResourceVersion
		})
		dashboardSnapshot(t, reader, func(items []v1alpha1.BLEDevice) bool { return len(items) == 0 })

		for {
			if event := nextEvent(t, ctx, stream); event.Type == watch.Deleted {
				break
			}
		}
	})
}

func nextEvent(t *testing.T, ctx context.Context, stream watch.Interface) watch.Event {
	t.Helper()
	select {
	case event, ok := <-stream.ResultChan():
		if !ok {
			t.Fatal("watch ended")
		}
		return event
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return watch.Event{}
	}
}

func get(t *testing.T, ctx context.Context, url string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("GET %s: HTTP %d", url, response.StatusCode)
	}
	return response
}

func dashboardSnapshot(t *testing.T, reader *bufio.Scanner, matches func([]v1alpha1.BLEDevice) bool) []v1alpha1.BLEDevice {
	t.Helper()
	for reader.Scan() {
		data, ok := strings.CutPrefix(reader.Text(), "data: ")
		if !ok {
			continue
		}
		var snapshot struct {
			State   string
			Devices []v1alpha1.BLEDevice
		}
		if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.State == "live" && matches(snapshot.Devices) {
			return snapshot.Devices
		}
	}
	t.Fatalf("dashboard snapshot did not arrive: %v", reader.Err())
	return nil
}
