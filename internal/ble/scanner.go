// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"context"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	api "example.com/ble-controlplane/pkg/apis/generic"
)

// Observation is the portable subset of a scan report. Key stays inside the scanner.
type Observation struct {
	Key          string
	LocalName    string
	RSSI         *int32
	TxPower      *int32
	Connectable  *bool
	ServiceUUIDs []string
	CompanyIDs   []int32
}

func (o Observation) mergeInto(status *api.BLEDeviceStatus) {
	if o.LocalName != "" {
		status.LocalName = o.LocalName
	}
	if o.RSSI != nil {
		status.RSSIDbm = ptr.To(*o.RSSI)
	}
	if o.TxPower != nil {
		status.TxPowerDbm = ptr.To(*o.TxPower)
	}
	if o.Connectable != nil {
		status.Connectable = ptr.To(*o.Connectable)
	}
	if o.ServiceUUIDs != nil {
		services := make([]string, 0, len(o.ServiceUUIDs))
		for _, id := range o.ServiceUUIDs {
			id = strings.ToLower(id)
			switch len(id) {
			case 4:
				id = "0000" + id + "-0000-1000-8000-00805f9b34fb"
			case 8:
				id += "-0000-1000-8000-00805f9b34fb"
			}
			services = append(services, id)
		}
		slices.Sort(services)
		status.AdvertisedServiceUUIDs = slices.Compact(services)
	}
	if o.CompanyIDs != nil {
		ids := slices.Clone(o.CompanyIDs)
		slices.Sort(ids)
		status.ManufacturerCompanyIDs = slices.Compact(ids)
	}
}

// Provider delivers observations synchronously while the scanner is active.
// It calls ready when discovery starts, and releases its radio session on return.
type Provider interface {
	Scan(ctx context.Context, ready func(), report func(Observation)) error
}

func SelectProvider(name string) Provider {
	if name == "replay" {
		return replayProvider{}
	}
	return nativeProvider{}
}

// Run drives one provider for the lifetime of an enabled API group.
func Run(ctx context.Context, index *Index, provider Provider) {
	var workers wait.Group
	workers.Start(func() {
		wait.UntilWithContext(ctx, func(context.Context) { index.tick(time.Now()) }, publishEvery)
	})
	defer workers.Wait()

	report := func(observation Observation) { index.observe(observation, time.Now()) }
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		err := provider.Scan(ctx, index.setReady, report)
		if ctx.Err() != nil {
			return
		}
		reason := "BLE scanner stopped"
		if err != nil {
			reason = err.Error()
		}
		if index.setUnavailable(reason) {
			klog.InfoS("BLE discovery unavailable", "reason", reason)
		}
	}, 5*time.Second)
}
