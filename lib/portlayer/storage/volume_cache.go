// Copyright 2016-2017 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package storage

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"

	log "github.com/Sirupsen/logrus"

	"github.com/vmware/vic/lib/archive"
	"github.com/vmware/vic/lib/portlayer/exec"
	"github.com/vmware/vic/lib/portlayer/util"
	"github.com/vmware/vic/pkg/trace"
)

// VolumeLookupCache caches Volume references to volumes in the system.
type VolumeLookupCache struct {

	// Maps IDs to Volumes.
	//
	// id -> Volume
	vlc     map[string]Volume
	vlcLock sync.RWMutex

	// Maps the service url of the volume store to the underlying data storage implementation
	volumeStores map[string]VolumeStorer
}

func NewVolumeLookupCache(op trace.Operation) *VolumeLookupCache {
	v := &VolumeLookupCache{
		vlc:          make(map[string]Volume),
		volumeStores: make(map[string]VolumeStorer),
	}

	return v
}

func (v *VolumeLookupCache) GetVolumeStore(op trace.Operation, storeName string) (*url.URL, error) {
	u, err := util.VolumeStoreNameToURL(storeName)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// AddStore adds a volumestore by name.  The url returned is the service url to the volume store.
func (v *VolumeLookupCache) AddStore(op trace.Operation, storeName string, vs VolumeStorer) (*url.URL, error) {
	v.vlcLock.Lock()
	defer v.vlcLock.Unlock()

	// get the service url
	u, err := util.VolumeStoreNameToURL(storeName)
	if err != nil {
		return nil, err
	}

	if _, ok := v.volumeStores[u.String()]; ok {
		return nil, fmt.Errorf("volumestore (%s) already added", u.String())
	}

	v.volumeStores[u.String()] = vs
	return u, v.rebuildCache(op)
}

func (v *VolumeLookupCache) volumeStore(store *url.URL) (VolumeStorer, error) {

	// find the datastore
	vs, ok := v.volumeStores[store.String()]
	if !ok {
		err := VolumeStoreNotFoundError{
			Msg: fmt.Sprintf("volume store (%s) not found", store.String()),
		}
		return nil, err
	}

	return vs, nil
}

// VolumeStoresList returns a list of volume store names
func (v *VolumeLookupCache) VolumeStoresList(op trace.Operation) ([]string, error) {

	v.vlcLock.RLock()
	defer v.vlcLock.RUnlock()

	stores := make([]string, 0, len(v.volumeStores))
	for u := range v.volumeStores {

		// from the storage url, get the store name
		storeURL, err := url.Parse(u)
		if err != nil {
			return nil, err
		}

		storeName, err := util.VolumeStoreName(storeURL)
		if err != nil {
			return nil, err
		}

		stores = append(stores, storeName)
	}

	return stores, nil
}

func (v *VolumeLookupCache) VolumeCreate(op trace.Operation, ID string, store *url.URL, capacityKB uint64, info map[string][]byte) (*Volume, error) {
	v.vlcLock.Lock()
	defer v.vlcLock.Unlock()

	// check if it exists
	_, ok := v.vlc[ID]
	if ok {
		return nil, os.ErrExist
	}

	vs, err := v.volumeStore(store)
	if err != nil {
		return nil, err
	}

	vol, err := vs.VolumeCreate(op, ID, store, capacityKB, info)
	if err != nil {
		return nil, err
	}
	// Add it to the cache.
	v.vlc[vol.ID] = *vol

	return vol, nil
}

func (v *VolumeLookupCache) VolumeDestroy(op trace.Operation, ID string) error {
	v.vlcLock.Lock()
	defer v.vlcLock.Unlock()

	// Check if it exists
	vol, ok := v.vlc[ID]
	if !ok {
		return os.ErrNotExist
	}

	if err := volumeInUse(vol.ID); err != nil {
		op.Errorf("VolumeStore: delete error: %s", err.Error())
		return err
	}

	vs, err := v.volumeStore(vol.Store)
	if err != nil {
		return err
	}

	// remove it from the volumestore
	if err := vs.VolumeDestroy(op, &vol); err != nil {
		return err
	}
	delete(v.vlc, vol.ID)

	return nil
}

func (v *VolumeLookupCache) VolumeGet(op trace.Operation, ID string) (*Volume, error) {
	v.vlcLock.RLock()
	defer v.vlcLock.RUnlock()

	// look in the cache
	vol, ok := v.vlc[ID]
	if !ok {
		return nil, os.ErrNotExist
	}

	return &vol, nil
}

func (v *VolumeLookupCache) VolumesList(op trace.Operation) ([]*Volume, error) {
	v.vlcLock.RLock()
	defer v.vlcLock.RUnlock()

	// look in the cache, return the list
	l := make([]*Volume, 0, len(v.vlc))
	for _, vol := range v.vlc {
		// this is idiotic
		var e Volume
		e = vol
		l = append(l, &e)
	}

	return l, nil
}

func (v *VolumeLookupCache) Export(op trace.Operation, store *url.URL, id, ancestor string, spec *archive.FilterSpec, data bool) (io.ReadCloser, error) {
	storeName, err := util.VolumeStoreName(store)
	if err != nil {
		return nil, err
	}

	vs, ok := v.volumeStores[storeName]
	if !ok {
		err := fmt.Errorf("Volume store not found: %s", storeName)
		return nil, err
	}

	return vs.Export(op, store, id, ancestor, spec, data)
}

func (v *VolumeLookupCache) Import(op trace.Operation, store *url.URL, id string, spec *archive.FilterSpec, tarStream io.ReadCloser) error {
	storeName, err := util.VolumeStoreName(store)
	if err != nil {
		return err
	}

	vs, ok := v.volumeStores[storeName]
	if !ok {
		err := fmt.Errorf("Volume store not found: %s", storeName)
		return err
	}

	return vs.Import(op, store, id, spec, tarStream)
}

// goto the volume store and repopulate the cache.
func (v *VolumeLookupCache) rebuildCache(op trace.Operation) error {
	op.Infof("Refreshing volume cache.")

	for _, vs := range v.volumeStores {
		vols, err := vs.VolumesList(op)
		if err != nil {
			return err
		}

		for _, vol := range vols {
			log.Infof("Volumestore: Found vol %s on store %s.", vol.ID, vol.Store)
			// Add it to the cache.
			v.vlc[vol.ID] = *vol
		}
	}

	return nil
}

func volumeInUse(ID string) error {
	conts := exec.Containers.Containers(nil)
	if len(conts) == 0 {
		return nil
	}

	for _, cont := range conts {

		if cont.ExecConfig.Mounts == nil {
			continue
		}

		if _, mounted := cont.ExecConfig.Mounts[ID]; mounted {
			return &ErrVolumeInUse{
				Msg: fmt.Sprintf("volume %s in use by %s", ID, cont.ExecConfig.ID),
			}
		}
	}

	return nil
}
