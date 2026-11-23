# Development workflow

- Consult the Kubernetes core source and relevant upstream controller implementations before adding features. Use the generic control plane sample in `pkg/controlplane/apiserver/samples/generic`.
- Use Kubernetes API server options and lifecycle hooks for HTTPS serving and client-go conventions for informers. Keep the dashboard listener a thin transport adapter.
- Discover targets with `make help`. Keep aggregate targets composed of smaller targets.
- Keep Go builds at the Makefile's two-worker default and run compilation checks sequentially.
- Use named table-driven cases and generated fake clients when testing controllers.
- Build the daemon and embedded dashboard together.
