// SPDX-License-Identifier: Apache-2.0

// Package frontend serves the embedded page and forwards snapshots from the
// TypeScript client over a same-origin Server-Sent Events stream.
package frontend

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

//go:embed assets
var files embed.FS

type Dashboard struct {
	worker   string
	node     string
	config   string
	address  string
	provider string
	mu       sync.Mutex
	latest   json.RawMessage
	changed  chan struct{}
}

func New(worker, kubeconfig, address, provider string) (*Dashboard, error) {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("locating Node.js for the dashboard: %w", err)
	}
	if info, err := os.Stat(worker); err != nil {
		return nil, fmt.Errorf("locating the dashboard worker: %w", err)
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("dashboard worker is not a regular file: %s", worker)
	}
	return &Dashboard{
		worker: worker, node: node, config: kubeconfig, address: address, provider: provider,
		latest: json.RawMessage(`{"state":"connecting","devices":[]}`), changed: make(chan struct{}),
	}, nil
}

func (d *Dashboard) Serve(ctx context.Context, listener net.Listener) error {
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", d.stream)
	mux.Handle("GET /{$}", http.FileServerFS(assets))
	mux.Handle("GET /app.js", http.FileServerFS(assets))
	mux.Handle("GET /style.css", http.FileServerFS(assets))
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != d.address {
				http.Error(w, "Use the dashboard address printed by ble-controlplane", http.StatusMisdirectedRequest)
				return
			}
			w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			mux.ServeHTTP(w, r)
		}),
	}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (d *Dashboard) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	controller := http.NewResponseController(w)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	send := func(line string) error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
		return controller.Flush()
	}
	for {
		d.mu.Lock()
		snapshot, changed := d.latest, d.changed
		d.mu.Unlock()
		if err := send("data: " + string(snapshot) + "\n\n"); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			// Repeating the current snapshot also checks quiet browser connections.
		case <-changed:
		}
	}
}

func (d *Dashboard) publish(snapshot json.RawMessage) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.latest = snapshot
	close(d.changed)
	d.changed = make(chan struct{})
}

func (d *Dashboard) RunWorker(ctx context.Context) {
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		err := d.readWorker(ctx)
		if ctx.Err() == nil {
			klog.ErrorS(err, "Dashboard client stopped, restarting")
			d.publish(json.RawMessage(`{"state":"reconnecting","devices":[]}`))
		}
	}, 2*time.Second)
}

func (d *Dashboard) readWorker(ctx context.Context) error {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(workerCtx, d.node, d.worker)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+d.config, "BLE_PROVIDER="+d.provider)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for lines.Scan() {
		if !json.Valid(lines.Bytes()) {
			cancel()
			return errors.Join(fmt.Errorf("dashboard client emitted invalid JSON"), cmd.Wait())
		}
		d.publish(json.RawMessage(lines.Text()))
	}
	if err := lines.Err(); err != nil {
		cancel()
		return errors.Join(err, cmd.Wait())
	}
	return cmd.Wait()
}
