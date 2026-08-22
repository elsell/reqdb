package buildinfo

import "fmt"

// These values are replaced with release metadata by the build.
var (
	Version   = "dev"
	Revision  = "unknown"
	BuildDate = "unknown"
)

func String() string {
	return fmt.Sprintf("reqdb %s (revision %s, built %s)", Version, Revision, BuildDate)
}
