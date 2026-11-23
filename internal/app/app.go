// SPDX-License-Identifier: Apache-2.0

// Package app composes the BLE scanner, generic API server, and embedded dashboard.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/klog/v2"

	"example.com/ble-controlplane/internal/apiserver"
	"example.com/ble-controlplane/internal/ble"
	"example.com/ble-controlplane/internal/credentials"
	"example.com/ble-controlplane/internal/frontend"
	api "example.com/ble-controlplane/pkg/apis/generic"
)

type Options struct {
	DataDir    string
	Provider   string
	WorkerFile string
	SecurePort int
	UIPort     int
}

func Run(ctx context.Context, opts Options) error {
	if opts.Provider != "native" && opts.Provider != "replay" {
		return fmt.Errorf("unknown scanner provider %q (choose native or replay)", opts.Provider)
	}
	dir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return err
	}
	lock, err := lockDirectory(dir)
	if err != nil {
		return err
	}
	defer lock.Close()

	pki, err := credentials.Ensure(filepath.Join(dir, "pki"))
	if err != nil {
		return err
	}
	apiListener, err := listenLocal(ctx, opts.SecurePort)
	if err != nil {
		return fmt.Errorf("binding the Kubernetes API: %w", err)
	}
	defer apiListener.Close()
	httpListener, err := listenLocal(ctx, opts.UIPort)
	if err != nil {
		return fmt.Errorf("binding the dashboard: %w", err)
	}
	defer httpListener.Close()

	endpoint := "https://" + apiListener.Addr().String()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := pki.WriteKubeconfig(kubeconfig, endpoint, api.LocalNamespace); err != nil {
		return err
	}

	index := ble.NewIndex()
	config, err := apiserver.NewConfig(apiListener, pki, index)
	if err != nil {
		return err
	}
	s, err := config.Complete().New()
	if err != nil {
		return err
	}
	running := false
	defer func() {
		if !running {
			s.Destroy()
		}
	}()
	ui, err := frontend.New(s.LoopbackClientConfig, opts.WorkerFile, httpListener.Addr().String(), opts.Provider)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	var workers wait.Group
	defer func() {
		cancel(nil)
		workers.Wait()
	}()
	start := func(name string, run func(context.Context)) error {
		ready := make(chan struct{})
		if err := s.AddPostStartHook(name, func(server.PostStartHookContext) error {
			close(ready)
			return nil
		}); err != nil {
			return err
		}
		workers.Start(func() {
			select {
			case <-ready:
				if ctx.Err() == nil {
					run(ctx)
				}
			case <-ctx.Done():
			}
		})
		return nil
	}
	provider := ble.SelectProvider(opts.Provider)
	if err := start("start-ble-scanner", func(ctx context.Context) { ble.Run(ctx, index, provider) }); err != nil {
		return err
	}
	if err := start("start-dashboard", func(ctx context.Context) {
		klog.InfoS("BLE control plane listening", "api", endpoint, "dashboard", "http://"+httpListener.Addr().String(), "kubeconfig", kubeconfig)
		if err := ui.Serve(ctx, httpListener); err != nil {
			cancel(fmt.Errorf("serving the dashboard: %w", err))
		}
	}); err != nil {
		return err
	}
	if err := start("start-dashboard-client", ui.RunWorker); err != nil {
		return err
	}
	prepared := s.PrepareRun()
	running = true
	runErr := prepared.RunWithContext(ctx)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return errors.Join(runErr, cause)
	}
	return runErr
}

func listenLocal(ctx context.Context, port int) (net.Listener, error) {
	var config net.ListenConfig
	return config.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func lockDirectory(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot lock data directory %s: %w", dir, err)
	}
	return f, nil
}
