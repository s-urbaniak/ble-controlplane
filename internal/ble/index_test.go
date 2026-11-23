// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"

	api "example.com/ble-controlplane/pkg/apis/generic"
)

func TestWatchReplaysSightingsBetweenListAndWatch(t *testing.T) {
	index := NewIndex()
	index.setReady()
	now := time.Now()

	list, err := index.List(metainternalversion.ListOptions{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("initial list: %+v, %v", list, err)
	}
	index.observe(Observation{Key: "radio-key", LocalName: "Pizza Beacon", RSSI: ptr.To(int32(-63))}, now)
	index.observe(Observation{Key: "radio-key", RSSI: ptr.To(int32(-42))}, now.Add(2*time.Second))
	index.tick(now.Add(2 * time.Second))
	index.tick(now.Add(2*time.Second + deviceTTL))
	index.observe(Observation{Key: "radio-key"}, now.Add(time.Minute))

	stream, err := index.Watch(t.Context(), metainternalversion.ListOptions{ResourceVersion: list.ResourceVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Stop()
	cases := []struct {
		name string
		kind watch.EventType
		rssi *int32
	}{
		{name: "first sighting", kind: watch.Added, rssi: ptr.To(int32(-63))},
		{name: "signal change", kind: watch.Modified, rssi: ptr.To(int32(-42))},
		{name: "expired sighting", kind: watch.Deleted, rssi: ptr.To(int32(-42))},
		{name: "rediscovered", kind: watch.Added},
	}
	var first *api.BLEDevice
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := receive(t, stream)
			if event.Type != tc.kind {
				t.Fatalf("event type = %s, want %s", event.Type, tc.kind)
			}
			object := event.Object.(*api.BLEDevice)
			if !ptr.Equal(object.Status.RSSIDbm, tc.rssi) {
				t.Fatalf("RSSI = %v, want %v", ptr.Deref(object.Status.RSSIDbm, 0), ptr.Deref(tc.rssi, 0))
			}
			if first == nil {
				first = object
			}
			if object.Name != first.Name || (object.UID == first.UID) != (tc.rssi != nil) {
				t.Fatalf("unexpected device identity: %v / %v", object.Name, object.UID)
			}
			if tc.rssi != nil && object.Status.LocalName != "Pizza Beacon" {
				t.Fatalf("partial report cleared the local name: %+v", object.Status)
			}
		})
	}
}

func TestWatchInitialEventsAndScannerLoss(t *testing.T) {
	index := NewIndex()
	index.setReady()
	index.observe(Observation{Key: "beacon"}, time.Now())
	list, err := index.List(metainternalversion.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	index.observe(Observation{Key: "other"}, time.Now())
	stream, err := index.Watch(t.Context(), metainternalversion.ListOptions{
		FieldSelector:        fields.OneTermEqualSelector("metadata.name", list.Items[0].Name),
		SendInitialEvents:    ptr.To(true),
		ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Stop()
	first := receive(t, stream)
	if first.Type != watch.Added || first.Object.(*api.BLEDevice).Name != list.Items[0].Name {
		t.Fatalf("unexpected initial event: %+v", first)
	}
	end := receive(t, stream)
	if end.Type != watch.Bookmark || end.Object.(*api.BLEDevice).Annotations[metav1.InitialEventsAnnotationKey] != "true" {
		t.Fatalf("unexpected bookmark: %+v", end)
	}
	index.setUnavailable("adapter lost")
	if err := apierrors.FromObject(receive(t, stream).Object); !apierrors.IsServiceUnavailable(err) {
		t.Fatalf("unexpected watch error: %v", err)
	}
	if _, err := index.List(metainternalversion.ListOptions{}); !apierrors.IsServiceUnavailable(err) {
		t.Fatalf("unavailable list: %v", err)
	}
	index.setReady()
	if _, err := index.Watch(t.Context(), metainternalversion.ListOptions{ResourceVersion: list.ResourceVersion}); !apierrors.IsResourceExpired(err) {
		t.Fatalf("old watch after recovery: %v", err)
	}
}

func TestDeviceAndWatchHistoryLimits(t *testing.T) {
	index := NewIndex()
	index.setReady()
	first, err := index.List(metainternalversion.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for range historySize {
		index.observe(Observation{Key: now.Format(time.RFC3339Nano)}, now)
		now = now.Add(time.Millisecond)
	}
	list, err := index.List(metainternalversion.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != maxDevices {
		t.Fatalf("bounded list length = %d, want %d", len(list.Items), maxDevices)
	}
	if _, err := index.Watch(t.Context(), metainternalversion.ListOptions{ResourceVersion: first.ResourceVersion}); !apierrors.IsResourceExpired(err) {
		t.Fatalf("watch beyond the replay buffer: %v", err)
	}
}

func receive(t *testing.T, stream watch.Interface) watch.Event {
	t.Helper()
	select {
	case event, ok := <-stream.ResultChan():
		if !ok {
			t.Fatal("watch closed before delivering an event")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("watch timed out")
		return watch.Event{}
	}
}
