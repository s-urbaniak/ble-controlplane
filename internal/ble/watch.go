// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	api "example.com/ble-controlplane/pkg/apis/generic"
)

type deviceWatch struct {
	cancel context.CancelFunc
	events chan watch.Event
}

func (w *deviceWatch) Stop()                          { w.cancel() }
func (w *deviceWatch) ResultChan() <-chan watch.Event { return w.events }

func (i *Index) Watch(ctx context.Context, options metainternalversion.ListOptions) (watch.Interface, error) {
	i.mu.Lock()
	revision, err := i.resolve(options.ResourceVersion)
	if err != nil {
		i.mu.Unlock()
		return nil, err
	}
	epoch := i.epoch
	initial := options.SendInitialEvents != nil && *options.SendInitialEvents
	var snapshot *api.BLEDeviceList
	if initial || (options.SendInitialEvents == nil && options.ResourceVersion == "0") {
		snapshot = i.snapshot(options)
		revision = i.revision
	}
	if i.revision-revision > historySize {
		i.mu.Unlock()
		return nil, expired()
	}
	i.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	stream := &deviceWatch{events: make(chan watch.Event), cancel: cancel}
	go i.stream(ctx, stream, options, epoch, revision, snapshot, initial)
	return stream, nil
}

func (i *Index) stream(ctx context.Context, stream *deviceWatch, options metainternalversion.ListOptions, epoch string, revision uint64, snapshot *api.BLEDeviceList, initial bool) {
	defer stream.Stop()
	defer close(stream.events)
	send := func(event watch.Event) bool {
		select {
		case stream.events <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if snapshot != nil {
		for index := range snapshot.Items {
			if !send(watch.Event{Type: watch.Added, Object: &snapshot.Items[index]}) {
				return
			}
		}
		if initial && !send(bookmark(snapshot.ResourceVersion, true)) {
			return
		}
	}
	var bookmarks <-chan time.Time
	if options.AllowWatchBookmarks {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		bookmarks = ticker.C
	}

	for ctx.Err() == nil {
		event, version, changed, err := i.next(epoch, revision)
		if err != nil {
			send(watch.Event{Type: watch.Error, Object: &err.ErrStatus})
			return
		}
		if event.Object != nil {
			revision++
			device := event.Object.(*api.BLEDevice)
			if matches(options, device) {
				event.Object = device.DeepCopy()
				if !send(event) {
					return
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-bookmarks:
			if !send(bookmark(version, false)) {
				return
			}
		}
	}
}

func (i *Index) next(epoch string, revision uint64) (watch.Event, string, <-chan struct{}, *apierrors.StatusError) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.unavailable != "" {
		return watch.Event{}, "", nil, apierrors.NewServiceUnavailable(i.unavailable)
	}
	if epoch != i.epoch || i.revision-revision > historySize {
		return watch.Event{}, "", nil, apierrors.NewResourceExpired("BLE watch history expired; list again")
	}
	if revision < i.revision {
		return i.history[revision%historySize], "", nil, nil
	}
	return watch.Event{}, i.version(), i.changed, nil
}

func bookmark(version string, initial bool) watch.Event {
	object := &api.BLEDevice{ObjectMeta: metav1.ObjectMeta{ResourceVersion: version}}
	if initial {
		object.Annotations = map[string]string{metav1.InitialEventsAnnotationKey: "true"}
	}
	return watch.Event{Type: watch.Bookmark, Object: object}
}
