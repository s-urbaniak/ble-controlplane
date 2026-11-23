// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"context"
	"time"

	"k8s.io/utils/ptr"
)

type replayProvider struct{}

func (replayProvider) Scan(ctx context.Context, ready func(), report func(Observation)) error {
	ready()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for second := 0; ; second = (second + 1) % 45 {
		if second < 10 {
			report(Observation{
				Key:          "replay-beacon",
				LocalName:    "Demo Beacon (replay)",
				RSSI:         ptr.To(int32(-45 - 3*second)),
				Connectable:  ptr.To(true),
				ServiceUUIDs: []string{"180f"},
			})
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
