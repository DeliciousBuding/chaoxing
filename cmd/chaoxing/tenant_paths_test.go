package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTenantPathIsolation(t *testing.T) {
	d := t.TempDir()
	base := config{StateDir: filepath.Join(d, "root"), KeyPath: filepath.Join(d, "root.key")}
	for _, tc := range []tenantConfig{
		{ID: "a", StateDir: filepath.Join(d, "root", "child"), KeyPath: filepath.Join(d, "a.key"), OwnerPasswordHash: "h"},
		{ID: "a", StateDir: filepath.Join(d, "root", "..", "root"), KeyPath: filepath.Join(d, "a.key"), OwnerPasswordHash: "h"},
		{ID: "a", StateDir: filepath.Join(d, "other"), KeyPath: filepath.Join(d, "root", "key"), OwnerPasswordHash: "h"},
	} {
		c := base
		c.Tenants = []tenantConfig{tc}
		if err := validateTenants(c); err == nil {
			t.Fatalf("accepted overlapping paths: %+v", tc)
		}
	}
	c := base
	c.Tenants = []tenantConfig{
		{ID: "a", StateDir: filepath.Join(d, "a"), KeyPath: filepath.Join(d, "a.key"), OwnerPasswordHash: "h"},
		{ID: "b", StateDir: filepath.Join(d, "a"), KeyPath: filepath.Join(d, "b.key"), OwnerPasswordHash: "h"},
	}
	if err := validateTenants(c); err == nil {
		t.Fatal("tenant-tenant overlap accepted")
	}
	c.Tenants[1].StateDir = filepath.Join(d, "b")
	c.Tenants[1].KeyPath = c.Tenants[0].KeyPath
	if err := validateTenants(c); err == nil {
		t.Fatal("shared tenant key accepted")
	}
	c.Tenants[1].KeyPath = filepath.Join(d, "b.key")
	if err := validateTenants(c); err != nil {
		t.Fatal(err)
	}
}

func TestTenantSymlinkAncestorAndHardlinkRejected(t *testing.T) {
	d := t.TempDir()
	root := filepath.Join(d, "root")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(d, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip("symlink unavailable")
	}
	c := config{StateDir: root, KeyPath: filepath.Join(d, "root.key"), Tenants: []tenantConfig{{ID: "a", StateDir: filepath.Join(alias, "new-child"), KeyPath: filepath.Join(d, "a.key"), OwnerPasswordHash: "h"}}}
	if err := validateTenants(c); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
	if err := os.WriteFile(c.KeyPath, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(c.KeyPath, c.Tenants[0].KeyPath); err != nil {
		t.Skip("hardlink unavailable")
	}
	c.Tenants[0].StateDir = filepath.Join(d, "other")
	if err := validateTenants(c); err == nil {
		t.Fatal("hardlink key accepted")
	}
}
