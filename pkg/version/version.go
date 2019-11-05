package version

var (
	version string
	build   string
)

// Version returns version defined by -ldflags
func Version() string {
	return version
}

// Build returns build defined by -ldflags
func Build() string {
	return build
}
