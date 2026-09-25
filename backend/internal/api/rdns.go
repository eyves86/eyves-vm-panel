package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"eyvescloud/internal/config"
)

// rdns.go 实现终端用户自助的反向 DNS（PTR）管理：
//
//	GET /api/containers/{id}/rdns  列出容器已分配公网 IP 及其 PTR 主机名
//	PUT /api/containers/{id}/rdns  批量设置 PTR 主机名（空值清除）
//
// 安全约束：只为该容器实际持有的公网 IPv4 / IPv6 地址设置 PTR，未知地址一律拒绝，
// 防止越权改写其它用户 IP 的反向解析。

// rdnsRecord 是单条反向 DNS 记录。
type rdnsRecord struct {
	Address  string `json:"address"`
	Family   string `json:"family"` // "ipv4" 或 "ipv6"
	Hostname string `json:"hostname"`
}

// getReverseDNS 返回容器所有公网 IP 的 PTR 记录（含未设置的空值，便于前端渲染）。
func getReverseDNS(w http.ResponseWriter, r *http.Request, id int) {
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]interface{}{"records": collectRDNSRecords(c)},
	})
}

// updateReverseDNS 批量更新容器公网 IP 的 PTR 主机名。
func updateReverseDNS(w http.ResponseWriter, r *http.Request, id int) {
	c := config.FindContainer(id)
	if c == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "Container not found"})
		return
	}

	var req struct {
		Records []rdnsRecord `json:"records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}

	// 容器实际持有的公网地址集合，用于越权校验。
	owned := collectRDNSRecords(c)
	ownedSet := make(map[string]bool, len(owned))
	for _, rec := range owned {
		ownedSet[rec.Address] = true
	}

	desired := make(map[string]string, len(req.Records))
	var unknown []string
	for _, rec := range req.Records {
		address := strings.TrimSpace(rec.Address)
		hostname := strings.TrimSpace(rec.Hostname)
		if address == "" {
			jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Each record requires an address"})
			return
		}
		if !ownedSet[address] {
			unknown = append(unknown, address)
			continue
		}
		if hostname != "" {
			if err := validateRDNSHostname(hostname); err != nil {
				jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid hostname for " + address + ": " + err.Error()})
				return
			}
		}
		desired[address] = hostname
	}
	if len(unknown) > 0 {
		jsonResponse(w, http.StatusBadRequest, APIResponse{
			Success: false,
			Message: "Address is not assigned to this container: " + strings.Join(unknown, ", "),
		})
		return
	}

	// 校验通过后再原子落库，避免部分应用。
	containerUUID := c.UUID
	applied := make([]rdnsRecord, 0, len(desired))
	if err := config.MutateGlobal(func(cfg *config.EyvescloudConfig) {
		for i := range cfg.Containers {
			if cfg.Containers[i].UUID != containerUUID {
				continue
			}
			cont := &cfg.Containers[i]
			for j := range cont.PublicIPv4s {
				if host, ok := desired[cont.PublicIPv4s[j].Address]; ok {
					cont.PublicIPv4s[j].RDNS = host
				}
			}
			for j := range cont.IPv6Addresses {
				if host, ok := desired[cont.IPv6Addresses[j].Address]; ok {
					cont.IPv6Addresses[j].RDNS = host
				}
			}
			applied = collectRDNSRecords(cont)
			break
		}
	}); err != nil {
		jsonResponse(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to save reverse DNS: " + err.Error()})
		return
	}

	auditRequest(r, "container.rdns", c.Name, "updated reverse DNS records", true, "")
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Reverse DNS updated", Data: map[string]interface{}{"records": applied}})
}

// collectRDNSRecords 汇总容器的公网 IPv4 / IPv6 反向解析记录。
func collectRDNSRecords(c *config.Container) []rdnsRecord {
	records := make([]rdnsRecord, 0, len(c.PublicIPv4s)+len(c.IPv6Addresses))
	for _, ip := range c.PublicIPv4s {
		if strings.TrimSpace(ip.Address) == "" {
			continue
		}
		records = append(records, rdnsRecord{Address: ip.Address, Family: "ipv4", Hostname: ip.RDNS})
	}
	for _, ip := range c.IPv6Addresses {
		if strings.TrimSpace(ip.Address) == "" {
			continue
		}
		records = append(records, rdnsRecord{Address: ip.Address, Family: "ipv6", Hostname: ip.RDNS})
	}
	return records
}

// validateRDNSHostname 校验 PTR 主机名为合法 FQDN（RFC 1123 的宽松子集）。
func validateRDNSHostname(host string) error {
	h := strings.TrimSuffix(strings.TrimSpace(host), ".")
	if h == "" {
		return errors.New("hostname is empty")
	}
	if len(h) > 253 {
		return errors.New("hostname is too long (max 253 characters)")
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return errors.New("hostname must be a fully qualified domain name")
	}
	for _, label := range labels {
		if label == "" {
			return errors.New("hostname has an empty label")
		}
		if len(label) > 63 {
			return errors.New("hostname label is too long (max 63 characters)")
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
			if !isAlnum && ch != '-' {
				return errors.New("hostname may only contain letters, digits and hyphens")
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("hostname labels may not start or end with a hyphen")
		}
	}
	return nil
}
