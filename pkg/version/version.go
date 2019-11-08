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

// Full returns version with build
func Full() string {
	s := version
	if s != "" {
		s += "+" + build
	}
	return s
}
