package main

import (
	"encoding/json"
	"testing"
)

func TestBodyRemainsJSON(t *testing.T) {
	body, err := parseBody(`{"slot_id":7,"service_id":1}`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]int
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("body was wrapped as a JSON string: %s", encoded)
	}
	if fields["slot_id"] != 7 || fields["service_id"] != 1 {
		t.Fatal(fields)
	}
	if _, err := parseBody(`{"slot_id":`); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	if body, err := parseBody(""); err != nil || body != nil {
		t.Fatal("absent body should remain nil")
	}
}
