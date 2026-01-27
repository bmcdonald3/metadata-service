package main

import (
	"context"
	"fmt"

	"github.com/OpenCHAMI/cloud-init/internal/storage"
	"github.com/OpenCHAMI/cloud-init/pkg/handlers"
	"github.com/OpenCHAMI/cloud-init/pkg/resources/group"
	"github.com/OpenCHAMI/cloud-init/pkg/smdclient"
	"github.com/go-chi/chi/v5"
)

// StorageAdapter adapts the storage backend to the handlers.Store interface
type StorageAdapter struct{}

// NewStorageAdapter creates a new storage adapter
func NewStorageAdapter() *StorageAdapter {
	return &StorageAdapter{}
}

// GetClusterDefaults retrieves cluster defaults from storage
func (s *StorageAdapter) GetClusterDefaults() (*handlers.ClusterDefaults, error) {
	ctx := context.Background()

	// Get the first (and presumably only) ClusterDefaults resource
	resources, err := storage.LoadAllClusterDefaultss(ctx)
	if err != nil {
		return nil, err
	}

	if len(resources) == 0 {
		return nil, nil
	}

	// Get the first ClusterDefaults
	cd := resources[0]

	return &handlers.ClusterDefaults{
		BaseURL:          cd.Spec.BaseURL,
		CloudProvider:    cd.Spec.CloudProvider,
		Region:           cd.Spec.Region,
		AvailabilityZone: cd.Spec.AvailabilityZone,
		ClusterName:      cd.Spec.ClusterName,
		ShortName:        cd.Spec.ShortName,
		NidLength:        cd.Spec.NidLength,
		PublicKeys:       cd.Spec.PublicKeys,
	}, nil
}

// GetInstanceInfo retrieves instance-specific information from storage
func (s *StorageAdapter) GetInstanceInfo(id string) (*handlers.InstanceInfo, error) {
	ctx := context.Background()

	ii, err := storage.LoadInstanceInfo(ctx, id)
	if err != nil {
		return nil, err
	}

	return &handlers.InstanceInfo{
		InstanceID:       ii.Spec.InstanceID,
		LocalHostname:    ii.Spec.LocalHostname,
		Hostname:         ii.Spec.Hostname,
		CloudInitBaseURL: ii.Spec.CloudInitBaseURL,
		PublicKeys:       ii.Spec.PublicKeys,
	}, nil
}

// GetGroupData retrieves group data from storage
// ✅ UPDATED: Accepts 'profile' argument
func (s *StorageAdapter) GetGroupData(name, profile string) (*group.Group, error) {
	ctx := context.Background()

	// TODO: Phase 3.5 - Implement Profile-Scoped Lookup
	// The RFD states resources should be looked up as (profile, group).
	// Since the underlying storage package code wasn't provided,
	// we will default to ignoring the profile for now to keep the code compiling.
	
	// When you update internal/storage, change this line to:
	// return storage.LoadGroupWithProfile(ctx, name, profile)
	
	if profile != "default" {
		fmt.Printf("[DEBUG] Loading group '%s' with profile override '%s'\n", name, profile)
	}
	
	// Fallback to existing behavior for now
	g, err := storage.LoadGroup(ctx, name)
	if err != nil {
		return nil, err
	}

	return g, nil
}

// RegisterCloudInitRoutes registers the cloud-init metadata server endpoints
func RegisterCloudInitRoutes(r chi.Router, smd smdclient.SMDClient, store handlers.Store) {
	// Cloud-init metadata endpoints
	r.Get("/meta-data", handlers.MetaDataHandler(smd, store))
	r.Get("/user-data", handlers.UserDataHandler)
	r.Get("/vendor-data", handlers.VendorDataHandler(smd, store))
	r.Get("/network-config", handlers.NetworkConfigHandler(smd, store))
	r.Get("/{group}.yaml", handlers.GroupUserDataHandler(smd, store))
}
