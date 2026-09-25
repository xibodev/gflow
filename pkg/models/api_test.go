package models

import "testing"

func TestValidateSeedPresence(t *testing.T) {
	if _, ok, err := ValidateSeed(nil); err != nil || ok {
		t.Fatalf("nil seed must be absent without error")
	}
	zero := int64(0)
	v, ok, err := ValidateSeed(&zero)
	if err != nil || !ok || v != 0 {
		t.Fatalf("explicit zero must be preserved")
	}
	neg := int64(-1)
	if _, _, err := ValidateSeed(&neg); err == nil {
		t.Fatalf("negative seed must fail")
	}
	over := MaxSeed + 1
	if _, _, err := ValidateSeed(&over); err == nil {
		t.Fatalf("oversized seed must fail")
	}
}

func TestValidateVideoSubmitResolution(t *testing.T) {
	base := &VideoSubmitRequest{Prompt: "p", Duration: 4}
	if err := ValidateVideoSubmit(base); err != nil {
		t.Fatalf("native submit must pass: %v", err)
	}
	bad := &VideoSubmitRequest{Prompt: "p", Duration: 4, Resolution: "1080p"}
	if err := ValidateVideoSubmit(bad); err == nil {
		t.Fatalf("1080p at submit must be rejected")
	}
	endOnly := &VideoSubmitRequest{Prompt: "p", Duration: 4, EndImage: "x"}
	if err := ValidateVideoSubmit(endOnly); err == nil {
		t.Fatalf("end without start must fail")
	}
}

func TestValidateImageRequest(t *testing.T) {
	if err := ValidateImageRequest(&ImageRequest{}); err == nil {
		t.Fatalf("empty prompt must fail")
	}
	if err := ValidateImageRequest(&ImageRequest{Prompt: "p", N: 5}); err == nil {
		t.Fatalf("n=5 must fail")
	}
	if err := ValidateImageRequest(&ImageRequest{Prompt: "p", N: 1, ResponseFormat: "weird"}); err == nil {
		t.Fatalf("bad response_format must fail")
	}
}
