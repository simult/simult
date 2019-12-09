package config

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/goinsane/xlog"
	"github.com/goinsane/xlog/gelfoutput"
)

type AccessLogger struct {
	mu sync.Mutex
	*xlog.Logger
	facility         string
	file             *os.File
	gelfOutput       *gelfoutput.GelfOutput
	gelfOutputQueued *xlog.QueuedOutput
}

func NewAccessLogger(facility string) (l *AccessLogger) {
	l = &AccessLogger{
		Logger:   xlog.New(nil, xlog.SeverityInfo, 0),
		facility: facility,
	}
	return
}

func (l *AccessLogger) close() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	if l.gelfOutputQueued != nil {
		ctx, ctxCancel := context.WithTimeout(context.Background(), 2*time.Second)
		if e := l.gelfOutputQueued.WaitForEmpty(ctx); e != nil {
			xlog.Warning("access logger: logs are lost while shutting down gelf output")
		}
		ctxCancel()
		l.gelfOutputQueued.Close()
		l.gelfOutputQueued = nil
	}
	if l.gelfOutput != nil {
		l.gelfOutput.Close()
		l.gelfOutput = nil
	}
}

func (l *AccessLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.close()
}

func (l *AccessLogger) Update(cfg *Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	old := *l
	defer old.close()
	l.file = nil
	l.gelfOutput = nil
	l.gelfOutputQueued = nil
	if cfg.Global.AccessLog == nil {
		l.Logger.SetOutput(nil)
		return
	}
	outputs := make([]xlog.Output, 0, 4)
	if cfg.Global.AccessLog.File != "" {
		var e error
		if l.file, e = os.OpenFile(cfg.Global.AccessLog.File, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644); e != nil {
			xlog.Warningf("access logger: setting file: %v", e)
		} else {
			outputs = append(outputs, xlog.NewTextOutput(l.file, xlog.OutputFlagDate|xlog.OutputFlagTime|xlog.OutputFlagFields))
		}
	}
	if cfg.Global.AccessLog.Gelf != nil {
		var e error
		var writerType gelfoutput.GelfWriterType
		switch strings.ToLower(cfg.Global.AccessLog.Gelf.Type) {
		case "udp":
			writerType = gelfoutput.GelfWriterTypeUDP
		case "tcp":
			fallthrough
		default:
			writerType = gelfoutput.GelfWriterTypeTCP
		}
		opts := gelfoutput.GelfOptions{
			Host:     cfg.Global.AccessLog.Gelf.Source,
			Facility: l.facility,
		}
		if l.gelfOutput, e = gelfoutput.NewGelfOutput(writerType, cfg.Global.AccessLog.Gelf.Address, opts); e != nil {
			xlog.Warningf("access logger: setting gelf: %v", e)
		} else {
			l.gelfOutputQueued = xlog.NewQueuedOutput(l.gelfOutput, 1000)
			outputs = append(outputs, l.gelfOutputQueued)
		}
	}
	l.Logger.SetOutput(xlog.MultiOutput(outputs...))
}
