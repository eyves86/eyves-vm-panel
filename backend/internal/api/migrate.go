package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"eyvescloud/internal/config"
	"eyvescloud/internal/kvm"
	"eyvescloud/internal/lxc"
)

const migrateFormat = "eyvescloud-migrate"

// migrateContainer is the portable subset of a container configuration used
// to migrate a workload to another EYVESCLOUD node. Runtime-only state (status,
// allocated ports, traffic counters, host boot restore) is excluded.
type migrateContainer struct {
	Name                  string                        `json:"name"`
	Virtualization        string                        `json:"virtualization"`
	Template              string                        `json:"template"`
	VCPU                  float64                       `json:"vcpu"`
	RAMMB                 int                           `json:"ram_mb"`
	DiskGB                float64                       `json:"disk_gb"`
	DataDiskGB            float64                       `json:"data_disk_gb,omitempty"`
	DataDiskMountPath     string                        `json:"data_disk_mount_path,omitempty"`
	NetworkBWMbps         int                           `json:"network_bw_mbps"`
	NetworkDownMbps       int                           `json:"network_down_mbps"`
	NetworkUpMbps         int                           `json:"network_up_mbps"`
	MonthlyTrafficGB      int                           `json:"monthly_traffic_gb"`
	TrafficMode           string                        `json:"traffic_mode"`
	TrafficInGB           int                           `json:"traffic_in_gb"`
	TrafficOutGB          int                           `json:"traffic_out_gb"`
	IOSpeedMBps           int                           `json:"io_speed_mbps"`
	IOReadMBps            int                           `json:"io_read_mbps"`
	IOWriteMBps           int                           `json:"io_write_mbps"`
	LANIPv4Mode           string                        `json:"lan_ipv4_mode,omitempty"`
	LANInterface          string                        `json:"lan_interface,omitempty"`
	LANIPv4Address        string                        `json:"lan_ipv4_address,omitempty"`
	LANIPv4PrefixLen      int                           `json:"lan_ipv4_prefix_len,omitempty"`
	LANIPv4Gateway        string                        `json:"lan_ipv4_gateway,omitempty"`
	PublicIPv4s           []config.PublicIPv4Assignment `json:"public_ipv4s,omitempty"`
	IPv6Addresses         []config.IPv6Assignment       `json:"ipv6_addresses,omitempty"`
	PortMappings          []config.PortMapping          `json:"port_mappings,omitempty"`
	PortMappingLimit      int                           `json:"port_mapping_limit,omitempty"`
	FirewallEnabled       bool                          `json:"firewall_enabled,omitempty"`
	FirewallDefaultAction string                        `json:"firewall_default_action,omitempty"`
	FirewallRules         []config.FirewallRule         `json:"firewall_rules,omitempty"`
	SSHAuthMode           string                        `json:"ssh_auth_mode,omitempty"`
	SSHPassword           string                        `json:"ssh_password,omitempty"`
	SSHPublicKey          string                        `json:"ssh_public_key,omitempty"`
	CloudInitUserData     string                        `json:"cloud_init_user_data,omitempty"`
	SnapshotLimit         int                           `json:"snapshot_limit,omitempty"`
	Tenant                string                        `json:"tenant,omitempty"`
	ExpiresAt             string                        `json:"expires_at,omitempty"`
	CreatedAt             string                        `json:"created_at,omitempty"`
}

type migrateBundle struct {
	Format         string           `json:"format"`
	Version        int              `json:"version"`
	ExportedAt     string           `json:"exported_at"`
	SourceNode     string           `json:"source_node,omitempty"`
	ChecksumSHA256 string           `json:"checksum_sha256,omitempty"` // 完整性校验
	Container      migrateContainer `json:"container"`
}

// migrateBundleVersion is a counter, not a constant, so builds import the same
// version that was exported (checksum covers the container payload).
const migrateBundleVersion = 2

func checksumMigrateContainer(c migrateContainer) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// buildMigrateContainer strips runtime-only fields from a managed container.
func buildMigrateContainer(c config.Container) migrateContainer {
	return migrateContainer{
		Name:                  c.Name,
		Virtualization:        c.Runtime(),
		Template:              c.Template,
		VCPU:                  c.VCPU,
		RAMMB:                 c.RAMMB,
		DiskGB:                c.DiskGB,
		DataDiskGB:            c.DataDiskGB,
		DataDiskMountPath:     c.DataDiskMountPath,
		NetworkBWMbps:         c.NetworkBWMbps,
		NetworkDownMbps:       c.NetworkDownMbps,
		NetworkUpMbps:         c.NetworkUpMbps,
		MonthlyTrafficGB:      c.MonthlyTrafficGB,
		TrafficMode:           c.TrafficMode,
		TrafficInGB:           c.TrafficInGB,
		TrafficOutGB:          c.TrafficOutGB,
		IOSpeedMBps:           c.IOSpeedMBps,
		IOReadMBps:            c.IOReadMBps,
		IOWriteMBps:           c.IOWriteMBps,
		LANIPv4Mode:           c.LANIPv4Mode,
		LANInterface:          c.LANInterface,
		LANIPv4Address:        c.LANIPv4Address,
		LANIPv4PrefixLen:      c.LANIPv4PrefixLen,
		LANIPv4Gateway:        c.LANIPv4Gateway,
		PublicIPv4s:           append([]config.PublicIPv4Assignment(nil), c.PublicIPv4s...),
		IPv6Addresses:         append([]config.IPv6Assignment(nil), c.IPv6Addresses...),
		PortMappings:          append([]config.PortMapping(nil), c.PortMappings...),
		PortMappingLimit:      c.PortMappingLimit,
		FirewallEnabled:       c.FirewallEnabled,
		FirewallDefaultAction: c.FirewallDefaultAction,
		FirewallRules:         append([]config.FirewallRule(nil), c.FirewallRules...),
		SSHPassword:           c.SSHPassword,
		CloudInitUserData:     c.CloudInitUserData,
		SnapshotLimit:         c.SnapshotLimit,
		Tenant:                c.Tenant,
		ExpiresAt:             c.ExpiresAt,
		CreatedAt:             c.CreatedAt,
	}
}

// HandleContainerMigrateExport downloads the portable configuration of a
// container so it can be imported on another EYVESCLOUD node.
func HandleContainerMigrateExport(w http.ResponseWriter, r *http.Request, id int) {
	// Migration bundles contain the SSH password and full network config, so
	// export is restricted to the administrator (sub-users and narrow API keys
	// are denied even though they hold container:read).
	if !isAdminRequest(r) {
		jsonResponse(w, http.StatusForbidden, APIResponse{Success: false, Message: "Administrator permission required"})
		return
	}
	if !requireScope(w, r, "container:read") {
		return
	}
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	bundle := migrateBundle{
		Format:     migrateFormat,
		Version:    migrateBundleVersion,
		ExportedAt: time.Now().Format(time.RFC3339),
		Container:  buildMigrateContainer(*c),
	}
	if sum, err := checksumMigrateContainer(bundle.Container); err == nil {
		bundle.ChecksumSHA256 = sum
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.migrate.json", c.Name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	_, _ = w.Write(data)
	auditRequest(r, "container.migrate_export", c.Name, fmt.Sprintf("导出迁移配置 v%d", bundle.Version), true, "")
}

// HandleMigrateImport recreates a container on this node from a migration
// bundle produced by HandleContainerMigrateExport.
func HandleMigrateImport(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, "container:create") {
		return
	}
	var bundle migrateBundle
	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid migration bundle"})
		return
	}
	if bundle.Format != migrateFormat {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Unsupported migration format"})
		return
	}
	// 断点/完整性校验：若导出带 checksum，则导入时校验，防止传输/篡改损坏。
	if bundle.ChecksumSHA256 != "" {
		want := strings.ToLower(strings.TrimSpace(bundle.ChecksumSHA256))
		got, err := checksumMigrateContainer(bundle.Container)
		if err != nil || got != want {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Migration bundle checksum mismatch (data corrupted in transit?)"})
			return
		}
	}
	mc := bundle.Container
	if strings.TrimSpace(mc.Name) == "" || strings.TrimSpace(mc.Template) == "" {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Migration bundle is missing name or template"})
		return
	}
	if config.FindContainerByName(mc.Name) != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "A container with this name already exists on this node"})
		return
	}
	virtualization := config.NormalizeVirtualization(mc.Virtualization)
	if !isImageEnabledAndDownloaded(mc.Template, virtualization) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Template is not enabled or downloaded on this node"})
		return
	}
	cfg := lxc.ContainerConfig{
		Name:              mc.Name,
		Virtualization:    virtualization,
		TemplateID:        mc.Template,
		VCPU:              mc.VCPU,
		RAMMB:             mc.RAMMB,
		DiskGB:            mc.DiskGB,
		DataDiskGB:        mc.DataDiskGB,
		DataDiskMountPath: mc.DataDiskMountPath,
		NetworkBWMbps:     mc.NetworkBWMbps,
		NetworkDownMbps:   mc.NetworkDownMbps,
		NetworkUpMbps:     mc.NetworkUpMbps,
		MonthlyTrafficGB:  mc.MonthlyTrafficGB,
		TrafficMode:       mc.TrafficMode,
		TrafficInGB:       mc.TrafficInGB,
		TrafficOutGB:      mc.TrafficOutGB,
		IOSpeedMBps:       mc.IOSpeedMBps,
		IOReadMBps:        mc.IOReadMBps,
		IOWriteMBps:       mc.IOWriteMBps,
		LANIPv4Mode:       mc.LANIPv4Mode,
		LANInterface:      mc.LANInterface,
		LANIPv4Address:    mc.LANIPv4Address,
		LANIPv4PrefixLen:  mc.LANIPv4PrefixLen,
		LANIPv4Gateway:    mc.LANIPv4Gateway,
		SnapshotLimit:     mc.SnapshotLimit,
		SSHAuthMode:       mc.SSHAuthMode,
		SSHPassword:       mc.SSHPassword,
		SSHPublicKey:      mc.SSHPublicKey,
		CloudInitUserData: mc.CloudInitUserData,
		ExpiresAt:         mc.ExpiresAt,
		PublicIPv4s:       publicIPv4Addresses(mc.PublicIPv4s),
		IPv6Addresses:     ipv6AddressStrings(mc.IPv6Addresses),
		NATPortMappings:   mc.PortMappings,
	}
	if len(mc.PublicIPv4s) > 0 {
		cfg.AssignIPv4 = true
		cfg.IPv4Count = len(mc.PublicIPv4s)
	}
	if len(mc.IPv6Addresses) > 0 {
		cfg.AssignIPv6 = true
		cfg.IPv6Count = len(mc.IPv6Addresses)
	}
	if len(mc.PortMappings) > 0 {
		cfg.AssignNAT = boolPtr(true)
		cfg.PortMappingCount = len(mc.PortMappings)
	}
	if cfg.VCPU <= 0 {
		cfg.VCPU = 1
	}
	if cfg.RAMMB < 128 {
		cfg.RAMMB = 512
	}
	if cfg.DiskGB <= 0 {
		cfg.DiskGB = 5
	}
	if err := validateRuntimeResourceRequest(cfg.Virtualization, cfg.TemplateID, cfg.VCPU, cfg.RAMMB, cfg.DiskGB); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := validateCreateStoragePool(&cfg); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if virtualization != config.VirtualizationKVM && !kvm.IsWindowsImage(cfg.TemplateID) {
		if _, err := lxc.ResolveCreateSSHAccess(cfg); err != nil {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
	}
	if err := lxc.ValidateCreateNATPortAvailability(cfg); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if err := createByRuntime(cfg); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}
	created := config.FindContainerByName(mc.Name)
	if created != nil && mc.Tenant != "" {
		config.SetContainerTenant(created.ID, mc.Tenant)
	}
	auditRequest(r, "container.migrate_import", mc.Name, fmt.Sprintf("从迁移包导入 (format=%s)", bundle.Format), true, "")
	jsonResponse(w, http.StatusCreated, APIResponse{Success: true, Message: "Container imported successfully", Data: map[string]string{"name": mc.Name}})
}

func publicIPv4Addresses(assignments []config.PublicIPv4Assignment) []string {
	values := make([]string, 0, len(assignments))
	for _, item := range assignments {
		if strings.TrimSpace(item.Address) != "" {
			values = append(values, strings.TrimSpace(item.Address))
		}
	}
	return values
}

func ipv6AddressStrings(assignments []config.IPv6Assignment) []string {
	values := make([]string, 0, len(assignments))
	for _, item := range assignments {
		if strings.TrimSpace(item.Address) != "" {
			values = append(values, strings.TrimSpace(item.Address))
		}
	}
	return values
}

func boolPtr(value bool) *bool {
	return &value
}
