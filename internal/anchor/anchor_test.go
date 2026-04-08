package anchor

import (
	"reflect"
	"testing"
)

func TestExtractIDs(t *testing.T) {
	md := "Hello\n<!-- wiki:anchor:hero -->\nBody\n<!--wiki:anchor:section_2-->\n<!-- wiki:anchor:item-3 -->"
	got := ExtractIDs(md)
	want := []string{"hero", "section_2", "item-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractIDs = %#v, want %#v", got, want)
	}
}

func TestStripForPreview(t *testing.T) {
	md := "A\n<!-- wiki:anchor:hero -->\nB\n<!-- wiki:anchor:section -->"
	got := StripForPreview(md)
	want := "A\n\nB\n"
	if got != want {
		t.Fatalf("StripForPreview = %q, want %q", got, want)
	}
}
