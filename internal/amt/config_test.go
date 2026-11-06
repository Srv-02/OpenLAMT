package amt

import "testing"

func TestConfigAddress(t *testing.T) {
	if got := (Config{Host: "device.example.com", UseTLS: true}).Address(); got != "device.example.com:16993" {
		t.Fatalf("tls address = %q", got)
	}
	if got := (Config{Host: "192.0.2.10", UseTLS: false}).Address(); got != "192.0.2.10:16992" {
		t.Fatalf("plain address = %q", got)
	}
}

func TestConfigRedirectionAddress(t *testing.T) {
	if got := (Config{Host: "device.example.com", UseTLS: true}).RedirectionAddress(); got != "device.example.com:16995" {
		t.Fatalf("tls address = %q", got)
	}
	if got := (Config{Host: "192.0.2.10", UseTLS: false}).RedirectionAddress(); got != "192.0.2.10:16994" {
		t.Fatalf("plain address = %q", got)
	}
}
