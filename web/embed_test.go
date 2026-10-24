package web

import (
	"regexp"
	"testing"
)

func TestVersionFromPackageJSON(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`).MatchString(Version) {
		t.Fatalf("invalid version %q", Version)
	}
}
