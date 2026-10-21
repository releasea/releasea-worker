package deploy

import "testing"

func TestShouldReusePromotedImage(t *testing.T) {
	if !shouldReusePromotedImage(map[string]interface{}{"reuseImage": true}, "registry.local/app:abc") {
		t.Fatal("expected a promoted image to be reused")
	}
	if shouldReusePromotedImage(map[string]interface{}{"reuseImage": false}, "registry.local/app:abc") {
		t.Fatal("expected a normal deploy to build")
	}
	if shouldReusePromotedImage(map[string]interface{}{"reuseImage": true}, "  ") {
		t.Fatal("expected a missing image to build")
	}
}
