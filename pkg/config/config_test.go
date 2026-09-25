package config

import "testing"

func TestResolveImageAspect(t *testing.T) {
	for in, want := range map[string]string{"square": "square", "portrait": "portrait", "4:3": "4:3"} {
		got, err := ResolveImageAspect(in, "")
		if err != nil || got != want {
			t.Fatalf("%s -> %s,%v; want %s", in, got, err, want)
		}
	}
	if _, err := ResolveImageAspect("", "1024x1024"); err != nil {
		t.Fatalf("size compat failed: %v", err)
	}
	if _, err := ResolveImageAspect("panorama", ""); err == nil {
		t.Fatalf("unknown aspect must fail, not fall back to landscape")
	}
}

func TestValidateBindHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "localhost", "::1"} {
		if err := ValidateBindHost(h); err != nil {
			t.Fatalf("%s must be allowed: %v", h, err)
		}
	}
	if err := ValidateBindHost("0.0.0.0"); err == nil {
		t.Fatalf("wildcard bind must be rejected")
	}
}

func TestRequireProjectID(t *testing.T) {
	c := &Config{}
	if _, err := c.RequireProjectID(); err == nil {
		t.Fatalf("missing project must fail with guidance")
	}
}
