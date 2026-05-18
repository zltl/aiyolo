package app

import (
	"reflect"
	"testing"
)

func TestPublishObjectKeys(t *testing.T) {
	keys := publishObjectKeys("windows/aiyolo.exe", "v0.1.0")
	want := []string{"windows/v0.1.0/aiyolo.exe", "windows/latest/aiyolo.exe", "windows/aiyolo.exe"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("publishObjectKeys() = %#v, want %#v", keys, want)
	}
}

func TestPublishObjectKeysWithoutVersion(t *testing.T) {
	keys := publishObjectKeys("windows/aiyolo.exe", "")
	want := []string{"windows/aiyolo.exe"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("publishObjectKeys() = %#v, want %#v", keys, want)
	}
}
