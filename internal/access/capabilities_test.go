package access

import (
	"context"
	"testing"
)

func TestCIMDNotAdvertisedOrFetchedByDefault(t *testing.T) {
	cfg := config(t)
	s, h := setup(t, cfg)
	v := bodyMap(t, request(h, "GET", "/.well-known/oauth-authorization-server", "", ""))
	if v["client_id_metadata_document_supported"] != false {
		t.Fatal("unconfigured capability advertised")
	}
	s.fetch = func(context.Context, string) (client, error) {
		t.Fatal("metadata fetch while disabled")
		return client{}, nil
	}
	if _, ok := s.resolveClient(context.Background(), "https://chatgpt.com/oauth/client.json"); ok {
		t.Fatal("disabled CIMD accepted")
	}
}
func TestCIMDExplicitOptInAdvertised(t *testing.T) {
	cfg := config(t)
	cfg.EnableCIMD = true
	_, h := setup(t, cfg)
	v := bodyMap(t, request(h, "GET", "/.well-known/oauth-authorization-server", "", ""))
	if v["client_id_metadata_document_supported"] != true {
		t.Fatal("configured capability absent")
	}
}
