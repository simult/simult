package config

import (
	"io"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	yaml "gopkg.in/yaml.v3"
)

type Config struct {
	Global struct {
		RlimitNofile uint64
	}
	Defaults struct {
		TLSParams *TLSParams
	}
	Frontends map[string]struct {
		Timeout        time.Duration
		DefaultBackend string
		Routes         []struct {
			Host    string
			Path    string
			Backend string
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
		Servers     []string
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
		err = errors.WithStack(err)
		return
	}
	return
}

func LoadFromFile(fileName string) (cfg *Config, err error) {
	f, err := os.Open(fileName)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer f.Close()
	return LoadFrom(f)
}

var (
	nameRgx *regexp.Regexp

	validationsInitialized uint32
)

func InitializeValidations(name *regexp.Regexp) {
	if !atomic.CompareAndSwapUint32(&validationsInitialized, 0, 1) {
		panic("validations already initialized")
	}
	nameRgx = name
}
