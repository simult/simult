package config

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	yaml "gopkg.in/yaml.v3"
)

// Config stores configuration
type Config struct {
	Global struct {
		PromResetOnReload bool
		RlimitNofile      uint64
	}
	Defaults struct {
		TLSParams        *TLSParams
		RequestTimeout   time.Duration
		MaxKeepAliveReqs int
		KeepAliveTimeout time.Duration
		ConnectTimeout   time.Duration
	}
	Frontends map[string]struct {
		MaxConn          int
		MaxIdleConn      int
		Timeout          time.Duration
		RequestTimeout   time.Duration
		MaxKeepAliveReqs int
		KeepAliveTimeout time.Duration
		DefaultBackend   string
		DefaultBackup    string
		Routes           []struct {
			Host         string
			Path         string
			Backend      string
			Backup       string
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
		MaxConn           int
		ServerMaxConn     int
		ServerMaxIdleConn int
		Timeout           time.Duration
		ConnectTimeout    time.Duration
		ReqHeaders        map[string]string
		ServerHashSecret  string
		HealthCheck       string
		Mode              string
		AffinityKey       struct {
			Source     string
			MaxServers int
			Threshold  int
		}
		OverrideErrors string
		Servers        []string
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

// LoadFrom loads configuration from reader, decodes and returns as Config type
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

// LoadFromFile takes yaml file as input, decodes and returns as Config type
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
