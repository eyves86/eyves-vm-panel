// Package instanceprovider 统一 VM/容器生命周期 Provider 接口（P5-2）：
//
//   - Provider 接口：Create/Start/Stop/Delete/Rebuild/Migrate/GetState，
//     KVM 与 LXC 两个实现收敛进同一对外动作语义；
//   - ProviderKind：lxc / kvm；
//   - Template 模型：版本化、变更日志、状态机 draft→published→deprecated；
//   - TemplateRegistry：注册 + 版本管理 + 可见性控制（租户级 / 全局级）；
//   - 实例记录 template_version_id，便于安全回溯。
//
// 本包只定义接口 + 状态机；真实实现（kvm/lxc）由 api/storage/lxc/kvm
// 包提供，遵循 Provider 接口签名。
package instanceprovider

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProviderKind 区分 KVM/LXC 实例。
type ProviderKind string

const (
	ProviderLXC ProviderKind = "lxc"
	ProviderKVM ProviderKind = "kvm"
)

// InstanceState 实例状态。
type InstanceState string

const (
	StateNone    InstanceState = ""
	StateCreated InstanceState = "created"
	StateRunning InstanceState = "running"
	StateStopped InstanceState = "stopped"
	StateDeleted InstanceState = "deleted"
)

// Spec 是实例规格（创建请求）。
type Spec struct {
	Name     string
	Template string
	VCPU     int
	RAMMB    int
	DiskGB   float64
	ImageID  string
	Backend  string // "dir" / "zfs" / "lvm" / "rbd" / "nfs"
	Metadata map[string]string
}

// Provider 统一生命周期接口（KVM 与 LXC 两个实现收敛）。
type Provider interface {
	Kind() ProviderKind
	Create(spec Spec) (instanceID string, err error)
	Start(instanceID string) error
	Stop(instanceID string) error
	Delete(instanceID string) error
	Rebuild(instanceID string) error
	Migrate(instanceID string, targetNode string) error
	GetState(instanceID string) (InstanceState, error)
}

// ---- 模板市场 ----

// TemplateStatus 模板状态。
type TemplateStatus string

const (
	TemplateDraft     TemplateStatus = "draft"
	TemplatePublished TemplateStatus = "published"
	TemplateDeprecated TemplateStatus = "deprecated"
)

// Template 模板定义（一个 Template 多个 Version）。
type Template struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Visibility  string    `json:"visibility"`           // "global" / "tenant:<tenant_id>"
	Versions    []TemplateVersion `json:"versions"`
	CreatedAt   time.Time `json:"created_at"`
}

// TemplateVersion 单版本。
type TemplateVersion struct {
	Version      string    `json:"version"`         // "1.0.0"
	Provider     ProviderKind `json:"provider"`    // lxc / kvm
	ImageID      string    `json:"image_id"`        // 镜像引用
	Checksum     string    `json:"checksum"`        // sha256 + 可选 ed25519 签名（P5-3）
	Status       TemplateStatus `json:"status"`
	Changelog    string    `json:"changelog,omitempty"`
	PublishedAt  time.Time `json:"published_at,omitempty"`
}

// Validate 校验模板字段（ID/Name/Version 非空）。
func (t TemplateVersion) Validate() error {
	if strings.TrimSpace(t.Version) == "" {
		return errors.New("instanceprovider: version required")
	}
	if t.Provider != ProviderLXC && t.Provider != ProviderKVM {
		return errors.New("instanceprovider: provider must be lxc or kvm")
	}
	if strings.TrimSpace(t.ImageID) == "" {
		return errors.New("instanceprovider: image_id required")
	}
	switch t.Status {
	case TemplateDraft, TemplatePublished, TemplateDeprecated:
	default:
		return errors.New("instanceprovider: invalid status")
	}
	return nil
}

// TemplateRegistry 模板注册表。
type TemplateRegistry struct {
	mu        sync.Mutex
	templates map[string]*Template
}

// NewTemplateRegistry 创建空注册表。
func NewTemplateRegistry() *TemplateRegistry {
	return &TemplateRegistry{templates: map[string]*Template{}}
}

// UpsertTemplate 注册或更新模板。
func (r *TemplateRegistry) UpsertTemplate(t Template) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t.CreatedAt = time.Now()
	r.templates[t.ID] = &t
}

// GetTemplate 查询模板。
func (r *TemplateRegistry) GetTemplate(id string) (Template, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.templates[id]
	if !ok {
		return Template{}, false
	}
	return *t, true
}

// ListTemplates 按名称升序列出模板（调用方可按 Visibility 二次过滤）。
func (r *TemplateRegistry) ListTemplates() []Template {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Template, 0, len(r.templates))
	for _, t := range r.templates {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PublishVersion 发布一个模板版本：仅允许从 draft 转到 published；
// 一次只能有一个 published 版本（发布新版时旧 published 转到 deprecated）。
//
// ErrAlreadyPublished / ErrInvalidTransition 显式错误。
var (
	ErrAlreadyPublished   = errors.New("instanceprovider: version already published")
	ErrInvalidTransition   = errors.New("instanceprovider: invalid status transition")
	ErrTemplateNotFound    = errors.New("instanceprovider: template not found")
	ErrVersionNotFound     = errors.New("instanceprovider: version not found")
)

// PublishVersion 把指定模板的指定版本推到 published。
func (r *TemplateRegistry) PublishVersion(templateID, versionStr string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.templates[templateID]
	if !ok {
		return ErrTemplateNotFound
	}
	found := false
	for vi, v := range t.Versions {
		if v.Version != versionStr {
			continue
		}
		found = true
		if v.Status != TemplateDraft {
			return ErrAlreadyPublished
		}
		t.Versions[vi].Status = TemplatePublished
		t.Versions[vi].PublishedAt = time.Now()
	}
	if !found {
		return ErrVersionNotFound
	}
	// 旧 published → deprecated
	for vi, v := range t.Versions {
		if v.Version != versionStr && v.Status == TemplatePublished {
			t.Versions[vi].Status = TemplateDeprecated
		}
	}
	return nil
}

// IsVisibleTo 判断模板对给定租户是否可见（global / tenant:<id>）。
func (t Template) IsVisibleTo(tenantID string) bool {
	if t.Visibility == "global" {
		return true
	}
	return strings.TrimPrefix(t.Visibility, "tenant:") == tenantID
}

// FindVersion 返回模板指定版本。
func (t Template) FindVersion(versionStr string) (TemplateVersion, bool) {
	for _, v := range t.Versions {
		if v.Version == versionStr {
			return v, true
		}
	}
	return TemplateVersion{}, false
}

// LatestPublishedVersion 返回 published 最新版本（按 PublishedAt 降序）。
func (t Template) LatestPublishedVersion() (TemplateVersion, bool) {
	var best TemplateVersion
	has := false
	for _, v := range t.Versions {
		if v.Status != TemplatePublished {
			continue
		}
		if !has || v.PublishedAt.After(best.PublishedAt) {
			best = v
			has = true
		}
	}
	return best, has
}