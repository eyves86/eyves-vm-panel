package api

import (
	"encoding/json"
	"net/http"
)

func HandleIPv6Status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "ipv6:read") {
		return
	}
	status := lxcManager.DetectIPv6Status()
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Data: status})
}

func assignIPv6(w http.ResponseWriter, r *http.Request, id int) {
	c, err := assignIPv6ByRuntime(id)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "IPv6 assigned", Data: c})
}

type ipAssignmentRequest struct {
	Mode      string   `json:"mode"`
	Auto      *bool    `json:"auto,omitempty"`
	Count     int      `json:"count,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

func (req ipAssignmentRequest) allocation() ([]string, int, bool) {
	auto := req.Mode == "random" || req.Mode == "auto"
	if req.Mode == "custom" {
		auto = false
	}
	if req.Mode == "clear" || req.Mode == "none" {
		return nil, 0, false
	}
	if req.Auto != nil {
		auto = *req.Auto
	}
	return req.Addresses, req.Count, auto
}

func updatePublicIPv4(w http.ResponseWriter, r *http.Request, id int) {
	var req ipAssignmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	addresses, count, auto := req.allocation()
	c, err := updatePublicIPv4ByRuntime(id, addresses, count, auto)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Public IPv4 assignments updated", Data: c})
}

func updateIPv6Addresses(w http.ResponseWriter, r *http.Request, id int) {
	var req ipAssignmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid request body"})
		return
	}
	addresses, count, auto := req.allocation()
	c, err := updateIPv6ByRuntime(id, addresses, count, auto)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "IPv6 assignments updated", Data: c})
}
