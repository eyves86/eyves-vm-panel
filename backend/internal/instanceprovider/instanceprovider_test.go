package instanceprovider

import (
	"testing"
	"time"
)

type fakeProvider struct {
	kind ProviderKind
}

func (f *fakeProvider) Kind() ProviderKind                            { return f.kind }
func (f *fakeProvider) Create(Spec) (string, error)                  { return "i-1", nil }
func (f *fakeProvider) Start(string) error                            { return nil }
func (f *fakeProvider) Stop(string) error                             { return nil }
func (f *fakeProvider) Delete(string) error                          { return nil }
func (f *fakeProvider) Rebuild(string) error                          { return nil }
func (f *fakeProvider) Migrate(string, string) error                 { return nil }
func (f *fakeProvider) GetState(string) (InstanceState, error)       { return StateRunning, nil }

func TestProviderImplementsInterface(t *testing.T) {
	var p Provider = &fakeProvider{kind: ProviderLXC}
	if p.Kind() != ProviderLXC {
		t.Fatal("Kind must report provider kind")
	}
	if _, err := p.Create(Spec{Name: "ct-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestTemplateVersionValidate(t *testing.T) {
	good := TemplateVersion{Version: "1.0", Provider: ProviderLXC, ImageID: "img", Status: TemplateDraft}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := good
	bad.Version = ""
	if err := bad.Validate(); err == nil {
		t.Fatal("empty version must error")
	}
	bad = good
	bad.Provider = "k8s"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid provider must error")
	}
	bad = good
	bad.Status = "weird"
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid status must error")
	}
}

func TestRegistryUpsertGetList(t *testing.T) {
	r := NewTemplateRegistry()
	r.UpsertTemplate(Template{ID: "t1", Name: "Alpine"})
	r.UpsertTemplate(Template{ID: "t2", Name: "Ubuntu"})
	got, ok := r.GetTemplate("t1")
	if !ok || got.Name != "Alpine" {
		t.Fatal("GetTemplate missing")
	}
	all := r.ListTemplates()
	if len(all) != 2 {
		t.Fatalf("list = %d, want 2", len(all))
	}
}

func TestPublishVersionLifecycle(t *testing.T) {
	r := NewTemplateRegistry()
	r.UpsertTemplate(Template{
		ID: "t1", Name: "X", Visibility: "global",
		Versions: []TemplateVersion{
			{Version: "1.0", Provider: ProviderLXC, ImageID: "i1", Status: TemplateDraft},
			{Version: "1.1", Provider: ProviderLXC, ImageID: "i2", Status: TemplateDraft},
		},
	})
	if err := r.PublishVersion("t1", "1.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.PublishVersion("t1", "1.1"); err != nil {
		t.Fatal(err)
	}
	t1, _ := r.GetTemplate("t1")
	v10, _ := t1.FindVersion("1.0")
	if v10.Status != TemplateDeprecated {
		t.Fatalf("v1.0 status = %v, want deprecated", v10.Status)
	}
	v11, _ := t1.FindVersion("1.1")
	if v11.Status != TemplatePublished {
		t.Fatalf("v1.1 status = %v, want published", v11.Status)
	}
	if !v11.PublishedAt.After(time.Time{}) {
		t.Fatal("published_at must be set")
	}
}

func TestPublishVersionRejectsDoublePublish(t *testing.T) {
	r := NewTemplateRegistry()
	r.UpsertTemplate(Template{
		ID: "t1", Name: "X",
		Versions: []TemplateVersion{
			{Version: "1.0", Provider: ProviderLXC, ImageID: "i", Status: TemplateDraft},
		},
	})
	if err := r.PublishVersion("t1", "1.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.PublishVersion("t1", "1.0"); err != ErrAlreadyPublished {
		t.Fatalf("double publish error = %v, want ErrAlreadyPublished", err)
	}
}

func TestPublishVersionNotFound(t *testing.T) {
	r := NewTemplateRegistry()
	if err := r.PublishVersion("missing", "1.0"); err != ErrTemplateNotFound {
		t.Fatalf("missing template = %v", err)
	}
	r.UpsertTemplate(Template{ID: "t1", Name: "X"})
	if err := r.PublishVersion("t1", "missing"); err != ErrVersionNotFound {
		t.Fatalf("missing version = %v", err)
	}
}

func TestIsVisibleTo(t *testing.T) {
	global := Template{Visibility: "global"}
	tenant := Template{Visibility: "tenant:A"}
	if !global.IsVisibleTo("X") {
		t.Fatal("global must be visible to all")
	}
	if !tenant.IsVisibleTo("A") {
		t.Fatal("tenant A must see tenant:A")
	}
	if tenant.IsVisibleTo("B") {
		t.Fatal("tenant B must not see tenant:A")
	}
}

func TestLatestPublishedVersion(t *testing.T) {
	t0 := Template{
		ID: "t",
		Versions: []TemplateVersion{
			{Version: "1.0", Provider: ProviderLXC, ImageID: "i", Status: TemplateDeprecated, PublishedAt: time.Now().Add(-time.Hour)},
			{Version: "2.0", Provider: ProviderLXC, ImageID: "i", Status: TemplatePublished, PublishedAt: time.Now()},
			{Version: "3.0", Provider: ProviderLXC, ImageID: "i", Status: TemplateDraft, PublishedAt: time.Time{}},
		},
	}
	v, ok := t0.LatestPublishedVersion()
	if !ok || v.Version != "2.0" {
		t.Fatalf("latest published = %+v ok=%v, want 2.0", v, ok)
	}
}