// Copyright © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package handlers

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"net/url"

	"github.com/OpenCHAMI/cloud-init/pkg/resources/group"
	"github.com/OpenCHAMI/cloud-init/pkg/smdclient"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// Store defines the interface for data storage operations
type Store interface {
	GetClusterDefaults() (*ClusterDefaults, error)
	GetInstanceInfo(id string) (*InstanceInfo, error)
	// UPDATED: Now accepts profile
	GetGroupData(name, profile string) (*group.Group, error)
}

// ... [Keep Struct definitions: ClusterDefaults, InstanceInfo, MetaData, InstanceData, VendorData same as before] ...
// (I am omitting the unchanged structs to save space, keep them as they were)

// ClusterDefaults holds cluster-wide default configuration
type ClusterDefaults struct {
	BaseURL          string   `json:"base_url"`
	CloudProvider    string   `json:"cloud_provider"`
	Region           string   `json:"region"`
	AvailabilityZone string   `json:"availability_zone"`
	ClusterName      string   `json:"cluster_name"`
	ShortName        string   `json:"short_name"`
	NidLength        int      `json:"nid_length"`
	PublicKeys       []string `json:"public_keys"`
}

type InstanceInfo struct {
	InstanceID       string   `json:"instance_id"`
	LocalHostname    string   `json:"local_hostname"`
	Hostname         string   `json:"hostname"`
	CloudInitBaseURL string   `json:"cloud_init_base_url"`
	PublicKeys       []string `json:"public_keys"`
}

type MetaData struct {
	InstanceID    string       `json:"instance-id" yaml:"instance-id"`
	LocalHostname string       `json:"local-hostname" yaml:"local-hostname"`
	Hostname      string       `json:"hostname" yaml:"hostname"`
	ClusterName   string       `json:"cluster-name" yaml:"cluster-name"`
	InstanceData  InstanceData `json:"instance-data" yaml:"instance_data"`
}

type InstanceData struct {
	V1 struct {
		CloudName        string     `json:"cloud-name,omitempty" yaml:"cloud_name,omitempty"`
		AvailabilityZone string     `json:"availability-zone,omitempty" yaml:"availability_zone,omitempty"`
		InstanceID       string     `json:"instance-id,omitempty" yaml:"instance_id,omitempty"`
		InstanceType     string     `json:"instance-type,omitempty" yaml:"instance_type,omitempty"`
		LocalHostname    string     `json:"local-hostname,omitempty" yaml:"local_hostname,omitempty"`
		Region           string     `json:"region,omitempty" yaml:"region,omitempty"`
		Hostname         string     `json:"hostname,omitempty" yaml:"hostname,omitempty"`
		LocalIPv4        string     `json:"local-ipv4,omitempty" yaml:"local_ipv4,omitempty"`
		CloudProvider    string     `json:"cloud-provider,omitempty" yaml:"cloud_provider,omitempty"`
		PublicKeys       []string   `json:"public-keys,omitempty" yaml:"public_keys,omitempty"`
		VendorData       VendorData `json:"vendor-data,omitempty" yaml:"vendor_data,omitempty"`
	} `json:"v1" yaml:"v1"`
}

type VendorData struct {
	Version          string                    `json:"version" yaml:"version"`
	CloudInitBaseURL string                    `json:"cloud_init_base_url,omitempty" yaml:"cloud_init_base_url,omitempty"`
	ClusterName      string                    `json:"cluster_name,omitempty" yaml:"cluster_name,omitempty"`
	Nid              int64                     `json:"nid,omitempty" yaml:"nid,omitempty"`
	Role             string                    `json:"role,omitempty" yaml:"role,omitempty"`
	MAC              string                    `json:"mac,omitempty" yaml:"mac,omitempty"`
	Interfaces       []map[string]any          `json:"interfaces,omitempty" yaml:"interfaces,omitempty"`
	Groups           map[string]map[string]any `json:"groups,omitempty" yaml:"groups,omitempty"`
}

// getActualRequestIP extracts the real client IP from the request
func getActualRequestIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// Helper to get profile from query param, default to "default"
func getProfile(r *http.Request) string {
	p := r.URL.Query().Get("profile")
	if p == "" {
		return "default"
	}
	return p
}

// MetaDataHandler returns metadata for the requesting node
func MetaDataHandler(smd smdclient.SMDClient, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getActualRequestIP(r)
		profile := getProfile(r) // ✅ Get Profile
		
		log.Debug().Msgf("Metadata request from IP: %s (Profile: %s)", ip, profile)

		id, err := smd.IDfromIP(ip)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to get component ID from IP %s", ip)
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}

		component, err := smd.ComponentInformation(id)
		if err != nil {
			http.Error(w, "component information not available", http.StatusInternalServerError)
			return
		}

		groups, err := smd.GroupMembership(id)
		if err != nil {
			groups = []string{}
		}

		bootIP, _ := smd.IPfromID(id)
		bootMAC, _ := smd.MACfromID(id)

		// ✅ Pass Profile to generator
		metadata := generateMetaData(smd, component, groups, bootIP, bootMAC, store, profile)

		w.Header().Set("Content-Type", "application/x-yaml")
		w.WriteHeader(http.StatusOK)

		yamlData, err := yaml.Marshal(metadata)
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal metadata to YAML")
			http.Error(w, "failed to encode metadata", http.StatusInternalServerError)
			return
		}
		w.Write(yamlData)
	}
}

// generateMetaData creates the metadata structure
// ✅ UPDATED: Accepts profile string
func generateMetaData(smd smdclient.SMDClient, component *smdclient.Component, groups []string, bootIP, bootMAC string, store Store, profile string) MetaData {
	metadata := MetaData{}

	clusterDefaults, err := store.GetClusterDefaults()
	if err != nil || clusterDefaults == nil {
		clusterDefaults = &ClusterDefaults{}
	}

	instanceInfo, err := store.GetInstanceInfo(component.ID)
	if err != nil || instanceInfo == nil {
		instanceInfo = &InstanceInfo{}
	}

	// [Hostname Logic - Unchanged]
	if instanceInfo.InstanceID != "" {
		metadata.InstanceID = instanceInfo.InstanceID
	} else {
		metadata.InstanceID = component.ID
	}
	if instanceInfo.LocalHostname != "" {
		metadata.LocalHostname = instanceInfo.LocalHostname
	} else {
		metadata.LocalHostname = generateHostname(clusterDefaults.ClusterName, clusterDefaults.ShortName, clusterDefaults.NidLength, component)
	}
	if instanceInfo.Hostname != "" {
		metadata.Hostname = instanceInfo.Hostname
	} else {
		metadata.Hostname = generateHostname(clusterDefaults.ClusterName, clusterDefaults.ShortName, clusterDefaults.NidLength, component)
	}
	metadata.ClusterName = clusterDefaults.ClusterName

	// Build instance data
	instanceData := InstanceData{}
	instanceData.V1.CloudName = "OpenCHAMI"
	instanceData.V1.CloudProvider = clusterDefaults.CloudProvider
	instanceData.V1.Region = clusterDefaults.Region
	instanceData.V1.AvailabilityZone = clusterDefaults.AvailabilityZone
	instanceData.V1.InstanceID = metadata.InstanceID
	instanceData.V1.LocalHostname = metadata.LocalHostname
	instanceData.V1.Hostname = metadata.Hostname
	instanceData.V1.LocalIPv4 = bootIP
	instanceData.V1.PublicKeys = append(clusterDefaults.PublicKeys, instanceInfo.PublicKeys...)

	// Build vendor data
	instanceData.V1.VendorData.Version = "1.0"
	instanceData.V1.VendorData.ClusterName = clusterDefaults.ClusterName
	instanceData.V1.VendorData.Nid = component.NID
	instanceData.V1.VendorData.Role = component.Role
	instanceData.V1.VendorData.MAC = bootMAC

	if instanceInfo.CloudInitBaseURL != "" {
		instanceData.V1.VendorData.CloudInitBaseURL = instanceInfo.CloudInitBaseURL
	} else {
		instanceData.V1.VendorData.CloudInitBaseURL = clusterDefaults.BaseURL
	}

	// Add group data (Profile Aware)
	if len(groups) > 0 {
		instanceData.V1.VendorData.Groups = make(map[string]map[string]any)
		for _, groupName := range groups {
			// ✅ Pass profile to GetGroupData
			groupData, err := store.GetGroupData(groupName, profile)
			if err != nil {
				continue
			}

			if groupData.Spec.Template == "" {
				continue
			}

			groupMeta := make(map[string]any)
			groupMeta["description"] = groupData.Spec.Description
			for k, v := range groupData.Spec.MetaData {
				groupMeta[k] = v
			}
			instanceData.V1.VendorData.Groups[groupName] = groupMeta
		}
	}

	nics, _ := smd.EthernetNICInfo(component.ID)
	ifaces, _ := smd.EthernetInterfaces(component.ID)
	if len(nics) > 0 && len(ifaces) > 0 {
		instanceData.V1.VendorData.Interfaces = buildInterfacesArray(nics, ifaces)
	}

	metadata.InstanceData = instanceData
	return metadata
}

// [generateHostname and buildInterfacesArray - Unchanged]
func generateHostname(clusterName, shortName string, nidLength int, component *smdclient.Component) string {
	var sname string
	var nlen int
	if shortName == "" {
		if len(clusterName) >= 2 {
			sname = clusterName[:2]
		} else {
			sname = clusterName
		}
	} else {
		sname = shortName
	}
	if nidLength == 0 {
		nlen = 4
	} else {
		nlen = nidLength
	}
	return fmt.Sprintf("%s%0*d", sname, nlen, component.NID)
}

func buildInterfacesArray(nics []smdclient.EthernetNIC, ifaces []smdclient.EthernetInterface) []map[string]any {
	ifaceMap := make(map[string]*smdclient.EthernetInterface)
	for i := range ifaces {
		ifaceMap[ifaces[i].MACAddress] = &ifaces[i]
	}
	var result []map[string]any
	for idx, nic := range nics {
		ifaceData := map[string]any{
			"name":        fmt.Sprintf("eth%d", idx),
			"mac":         nic.MACAddress,
			"description": nic.Description,
			"enabled":     nic.InterfaceEnabled,
			"redfishid":   nic.RedfishID,
		}
		if iface, ok := ifaceMap[nic.MACAddress]; ok {
			if len(iface.IPAddresses) > 0 {
				ifaceData["ip"] = iface.IPAddresses[0].IPAddress
				ifaceData["network"] = iface.IPAddresses[0].Network
				if len(iface.IPAddresses) > 1 {
					var ipAddrs []map[string]string
					for _, ipMap := range iface.IPAddresses {
						ipAddrs = append(ipAddrs, map[string]string{
							"ip":      ipMap.IPAddress,
							"network": ipMap.Network,
						})
					}
					ifaceData["ip_addresses"] = ipAddrs
				}
			}
		}
		result = append(result, ifaceData)
	}
	return result
}

// NetworkConfigHandler - [Unchanged, does not use profile yet]
func NetworkConfigHandler(smd smdclient.SMDClient, store Store) http.HandlerFunc { 
	return func(w http.ResponseWriter, r *http.Request) {
		// ... (Same as original) ...
		// If you want to force profile logging:
		// log.Debug().Msgf("Network config req (Profile: %s)", getProfile(r))
		
		// For brevity, I'm skipping the body here as it was unchanged in logic
		// just paste the original NetworkConfigHandler body here
        w.WriteHeader(http.StatusOK)
	}
}

// UserDataHandler - [Unchanged]
func UserDataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/cloud-config")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("#cloud-config\n"))
}

// VendorDataHandler returns vendor-data as an include-file list
// ✅ UPDATED: Propagates ?profile=... to the include URLs
func VendorDataHandler(smd smdclient.SMDClient, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getActualRequestIP(r)
		profile := getProfile(r) // ✅ Get Profile
		
		log.Debug().Msgf("Vendor-data request from IP: %s (Profile: %s)", ip, profile)

		id, err := smd.IDfromIP(ip)
		if err != nil {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}

		groups, err := smd.GroupMembership(id)
		if err != nil {
			groups = []string{}
		}

		clusterDefaults, err := store.GetClusterDefaults()
		baseURL := ""
		if err == nil {
			baseURL = clusterDefaults.BaseURL
		}

		instanceInfo, err := store.GetInstanceInfo(id)
		if err == nil && instanceInfo.CloudInitBaseURL != "" {
			baseURL = instanceInfo.CloudInitBaseURL
		}

		payload := "#include\n"
		for _, groupName := range groups {
			// ✅ Pass profile to GetGroupData
			groupData, err := store.GetGroupData(groupName, profile)
			if err != nil || groupData.Spec.Template == "" {
				continue
			}
			
			// ✅ Append profile to the include URL so the next request preserves context
			includeURL := fmt.Sprintf("%s/%s.yaml", baseURL, groupName)
			if profile != "default" {
				// Append query param properly
				u, err := url.Parse(includeURL)
				if err == nil {
					q := u.Query()
					q.Set("profile", profile)
					u.RawQuery = q.Encode()
					includeURL = u.String()
				}
			}
			
			payload += fmt.Sprintf("%s\n", includeURL)
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(payload))
	}
}

// GroupUserDataHandler returns group-specific cloud-config
// ✅ UPDATED: Reads ?profile=... and passes to storage
func GroupUserDataHandler(smd smdclient.SMDClient, store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupName := chi.URLParam(r, "group")
		profile := getProfile(r) // ✅ Get Profile

		ip := getActualRequestIP(r)
		log.Debug().Msgf("Group user-data request from IP: %s for group: %s (Profile: %s)", ip, groupName, profile)

		id, err := smd.IDfromIP(ip)
		if err != nil {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}

		groups, err := smd.GroupMembership(id)
		if err != nil {
			http.Error(w, "failed to verify group membership", http.StatusInternalServerError)
			return
		}

		isMember := false
		for _, g := range groups {
			if g == groupName {
				isMember = true
				break
			}
		}

		if !isMember {
			http.Error(w, fmt.Sprintf("node %s is not a member of group %s", id, groupName), http.StatusNotFound)
			return
		}

		// ✅ Pass profile to GetGroupData
		groupData, err := store.GetGroupData(groupName, profile)
		if err != nil {
			w.Header().Set("Content-Type", "text/cloud-config")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("#cloud-config\n"))
			return
		}

		component, err := smd.ComponentInformation(id)
		if err != nil {
			http.Error(w, "component information not available", http.StatusInternalServerError)
			return
		}

		clusterDefaults, err := store.GetClusterDefaults()
		if err != nil {
			clusterDefaults = &ClusterDefaults{}
		}

		bootIP, _ := smd.IPfromID(id)
		bootMAC, _ := smd.MACfromID(id)

		defaultMeta := map[string]string{
			"hostname":    generateHostname(clusterDefaults.ClusterName, clusterDefaults.ShortName, clusterDefaults.NidLength, component),
			"instance_id": component.ID,
			"nid":         fmt.Sprintf("%d", component.NID),
			"role":        component.Role,
			"mac":         bootMAC,
			"ip":          bootIP,
			"profile":     profile, // Expose profile to the template itself!
		}

		merged := group.MergeMetadata(defaultMeta, groupData.Spec.MetaData)

		nics, _ := smd.EthernetNICInfo(id)
		ifaces, _ := smd.EthernetInterfaces(id)
		if len(nics) > 0 && len(ifaces) > 0 {
			merged["interfaces"] = buildInterfacesArray(nics, ifaces)
		}

		rendered, err := group.RenderTemplate(groupData.Spec.Template, merged)
		if err != nil {
			http.Error(w, "template rendering failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/cloud-config")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(rendered))
	}
}
