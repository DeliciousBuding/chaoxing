package main

import "testing"

func TestLocationConfigValidation(t *testing.T) {
	valid := &locationConfig{Enabled: true, CenterLat: 39.9042, CenterLng: 116.4074, Zoom: 17, TimeoutSecond: 5}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, item := range []*locationConfig{
		{Enabled: true, CenterLat: 91},
		{Enabled: true, CenterLng: -181},
		{Enabled: true, Zoom: 22},
		{Enabled: true, TimeoutSecond: 31},
		{Enabled: true, BaiduAK: string(make([]byte, 129))},
	} {
		if err := item.validate(); err == nil {
			t.Fatalf("accepted %+v", item)
		}
	}
}
