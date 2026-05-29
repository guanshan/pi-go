package cautils

import (
	"fmt"
	"runtime"
	"strings"
)

func GetPiUserAgent(version string) string {
	return fmt.Sprintf("pi/%s (%s; go/%s; %s)", strings.TrimSpace(version), runtime.GOOS, strings.TrimPrefix(runtime.Version(), "go"), runtime.GOARCH)
}
