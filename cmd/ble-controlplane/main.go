// SPDX-License-Identifier: Apache-2.0

// Command ble-controlplane serves recent BLE observations through a Kubernetes API.
package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/component-base/cli"

	"example.com/ble-controlplane/internal/app"
)

func main() {
	os.Exit(cli.Run(newCommand()))
}

func newCommand() *cobra.Command {
	opts := app.Options{}
	cmd := &cobra.Command{
		Use:          "ble-controlplane",
		Short:        "Serve local Bluetooth LE observations as Kubernetes resources",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			opts.WorkerFile = filepath.Join(filepath.Dir(executable), "ble-controlplane-ui.cjs")
			return app.Run(server.SetupSignalContext(), opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.DataDir, "data-dir", "_data", "Directory for local TLS credentials and kubeconfigs")
	flags.StringVar(&opts.Provider, "provider", "native", "Bluetooth scanner provider: native or replay")
	flags.IntVar(&opts.SecurePort, "secure-port", 7443, "Local Kubernetes HTTPS port")
	flags.IntVar(&opts.UIPort, "ui-port", 7080, "Local dashboard HTTP port")
	return cmd
}
