package config

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	yaml "gopkg.in/yaml.v3"
)

type Config struct {
	Global struct {
		RlimitNofile      uint64
		PromResetOnReload bool
	}
	Defaults struct {
		TLSParams        *TLSParams
		KeepAliveTimeout time.Duration
	}
	Frontends map[string]struct {
		Timeout          time.Duration
		KeepAliveTimeout time.Duration
		DefaultBackend   string
		Routes           []struct {
			Host         string
			Path         string
			Backend      string
			Restrictions []struct {
				Network  string
				Path     string
				Invert   bool
				AndAfter bool
			}
		}
		Listeners []struct {
			Address   string
			TLS       bool
			TLSParams *TLSParams
		}
	}
	Backends map[string]struct {
		Timeout     time.Duration
		ReqHeaders  map[string]string
		HealthCheck string
		Mode        string
		AffinityKey struct {
			Source     string
			MaxServers int
			Threshold  int
		}
		Servers []string
	}
	HealthChecks map[string]struct {
		HTTP *struct {
			Path, Host        string
			Interval, Timeout time.Duration
			Fall, Rise        int
			Resp              string
		}
	}
}

func LoadFrom(r io.Reader) (cfg *Config, err error) {
	cfg = &Config{}
	d := yaml.NewDecoder(r)
	err = d.Decode(cfg)
	if err != nil {
		err = fmt.Errorf("yaml decode error: %w", err)
		return
	}
	return
}

// LoadFromFile takes opened yaml file as input, decodes and returns as Config type
func LoadFromFile(fileName string) (cfg *Config, err error) {
	f, err := os.Open(fileName)
	if err != nil {
		err = fmt.Errorf("file %q open error: %w", fileName, err)
		return
	}
	defer f.Close()
	return LoadFrom(f)
}

var (
	nameRgx *regexp.Regexp = regexp.MustCompile(`^[a-zA-Z_\-]([a-zA-Z0-9_\-])*$`)
)
