package main

import "testing"

func TestBusinessParsing(t *testing.T) {
	for _, args := range [][]string{{"assignments", "list", "--course-id", "123", "--class-id", "456", "--limit", "7", "--json"}, {"assignments.list", "--course", "example"}, {"assignments", "get", "1:2:3"}} {
		r, ok, e := parseBusiness(args)
		if !ok || e != nil || r.Tool == "" {
			t.Fatal(r, ok, e)
		}
	}
	for _, args := range [][]string{{"assignments", "list", "--limit", "1.5"}, {"assignments", "list", "--limit", "7", "--limit", "8"}, {"assignments", "list", "--url", "http://example.com"}, {"assignments", "get"}, {"assignments", "list", "--class-id", "2"}} {
		_, ok, e := parseBusiness(args)
		if !ok || e == nil {
			t.Fatal(args)
		}
	}
}
