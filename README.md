# BLE Control Plane

A small Kubernetes-style control plane for the Bluetooth Low Energy advertisements
seen by this machine. It serves the native `BLEDevice` resource in the fixed
`local` namespace and embeds a read-only dashboard.

## Run the replay demo

Install GNU Make, Go 1.26.5 or newer, and Node.js 24 or newer with npm. On macOS,
the build uses the Xcode Command Line Tools to compile the scanner helper.

```sh
make run PROVIDER=replay
```

Open [the local dashboard](http://127.0.0.1:7080). The replay beacon appears with
a changing signal, expires, and comes back on the next cycle. From a second
terminal in the project directory:

```sh
export KUBECONFIG="$PWD/_data/kubeconfig"
kubectl get ns
kubectl api-resources --api-group=generic.example.com
kubectl get bledevices
kubectl explain bledevice.status
kubectl get bledevices -n local --watch --output-watch-events
```

The HTTPS API binds to `127.0.0.1:7443`. The generated kubeconfig trusts the
local CA and selects `local` by default. The core Namespace API exposes the
single read-only `local` namespace. The server accepts `--secure-port`,
`--ui-port`, and `--data-dir` when launching the built daemon directly.

## Scan with the host radio

```sh
make run
```

On macOS, allow Bluetooth access when starting the daemon from Terminal. The
Core Bluetooth helper lives alongside the daemon binary. On Linux, start BlueZ
with a powered adapter and make sure the daemon user can start discovery on the
system D-Bus. Results are nearby BLE advertisements, including unpaired devices.
Adapter and permission errors appear as scanner unavailability in the dashboard
and as HTTP 503 from the BLE API. An active scan can return an empty list.
Sightings expire roughly three seconds after the last report from the platform.

## How it follows Kubernetes

- The native-resource layer of Kubernetes' [generic control plane sample] guides
  the server assembly. `k8s.io/apiserver` handles HTTPS, request routing,
  discovery, OpenAPI, serialization, and lifecycle. The BLE registry implements
  the standard read-only REST interfaces for get, list, and watch.
- Observations live in a bounded in-memory index with a replayable watch history.
  The [watch cache] guides list-to-watch handoff, bookmarks, and expired history.
  The scanner starts a new collection when it recovers from a failure. Pass the
  returned resource versions back unchanged and relist after HTTP 410.
- The dashboard page, assets, and event stream are served inside the Go daemon.
  A managed Node worker uses the [official Kubernetes TypeScript client] with
  its own kubeconfig, scoped to list and watch `bledevices` in `local`.
  The Go server relays the worker's snapshots to the browser.
- The scanner and BLE types were extracted from [PizzaPlane]. The local project
  retains their Apache 2.0 notices.

## Validate

```sh
make help
make check
```

`make check` verifies generated clients and OpenAPI, Go formatting, Go vet,
and TypeScript. It also starts the control plane with replay and exercises the
HTTPS API and dashboard stream with the bundled Node worker.

[generic control plane sample]: https://github.com/kubernetes/kubernetes/tree/v1.37.1/pkg/controlplane/apiserver/samples/generic
[watch cache]: https://github.com/kubernetes/kubernetes/blob/v1.37.1/staging/src/k8s.io/apiserver/pkg/storage/cacher/watch_cache.go
[official Kubernetes TypeScript client]: https://github.com/kubernetes-client/javascript
[PizzaPlane]: https://github.com/defenseunicorns-labs/pizzaplane
